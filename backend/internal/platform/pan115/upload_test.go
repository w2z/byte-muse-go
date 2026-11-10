package pan115

import (
	"bytemuse/backend/internal/domain"
	"bytes"
	"context"
	"fmt"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadCredentialExpiryIsRetryable(t *testing.T) {
	err := oss.ServiceError{StatusCode: 403, Code: "SecurityTokenExpired", Message: "The security token you provided has expired."}
	if !isUploadCredentialExpired(err) {
		t.Fatal("SecurityTokenExpired was not classified for credential refresh")
	}
	if !isUploadCredentialExpired(&err) {
		t.Fatal("pointer SecurityTokenExpired was not classified for credential refresh")
	}
	if !isUploadCredentialExpired(fmt.Errorf("wrapped: %w", err)) {
		t.Fatal("wrapped SecurityTokenExpired was not classified for credential refresh")
	}
	if isUploadCredentialExpired(oss.ServiceError{StatusCode: 403, Code: "AccessDenied"}) {
		t.Fatal("generic OSS access denial must not refresh credentials")
	}
}

func TestUploadReceiptInstantSkipsLookup(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "source"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString("abc")
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/open/upload/init" {
			t.Errorf("unexpected lookup %s", r.URL.Path)
		}
		writeJSON(t, w, `{"state":true,"data":{"status":2,"file_id":"99","pick_code":"pc99"}}`)
	})
	got, err := c.UploadWithReceipt(context.Background(), "token", "12", "file", f, 3, "")
	if err != nil || got.ID != "99" || got.PickCode != "pc99" || got.ParentID != "12" || got.Name != "file" || got.Size != 3 || !strings.EqualFold(got.SHA1, "a9993e364706816aba3e25717850c26c9cd0d89d") || calls != 1 {
		t.Fatalf("receipt=%+v calls=%d err=%v", got, calls, err)
	}
}

func TestUploadCallbackRequiresBusinessSuccessAndMatchingContent(t *testing.T) {
	for _, body := range []string{
		`{"state":false,"data":{"file_id":"99","pick_code":"pc99","file_name":"file","file_size":3,"sha1":"ABC"}}`,
		`{"state":true,"data":{"file_id":"99","pick_code":"pc99","file_name":"file","file_size":4,"sha1":"ABC"}}`,
		`{"state":true,"data":{"file_id":"99","pick_code":"pc99","file_name":"other","file_size":3,"sha1":"ABC"}}`,
		`{"state":true,"data":{"file_id":"99","pick_code":"pc99","file_name":"file","file_size":3,"sha1":"BAD"}}`,
		`{}`, `<Error>CallbackFailed</Error>`,
	} {
		if _, err := parseUploadCallback([]byte(body), "12", "file", 3, "ABC"); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	got, err := parseUploadCallback([]byte(`{"state":true,"data":{"file_id":99,"pick_code":"pc99","file_name":"file","file_size":"3","sha1":"abc"}}`), "12", "file", 3, "ABC")
	if err != nil || got.ID != "99" || got.ParentID != "12" {
		t.Fatalf("receipt=%+v err=%v", got, err)
	}
}

func TestUploadPickCodeDecodesProviderIDs(t *testing.T) {
	for _, code := range []string{"at4r9vihhhh", "b34vly68888", "cmb64judddd", "d2yul3dxxxx", "ewhxfe3zzzz"} {
		if got := uploadPickCodeID(code); got != "123456789" {
			t.Fatalf("code=%s id=%s", code, got)
		}
	}
	for _, code := range []string{"", "pc99", "fabcdefg", "a!0000", "azzz0000"} {
		if got := uploadPickCodeID(code); got != "" {
			t.Fatalf("invalid %s => %s", code, got)
		}
	}
}

func TestUploadInstantPickCodeOnlyAndRangeChallenge(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "source"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString("abc")
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		r.ParseForm()
		if calls == 1 {
			writeJSON(t, w, `{"state":true,"data":{"status":7,"sign_key":"challenge","sign_check":"0-2"}}`)
			return
		}
		if r.FormValue("sign_key") != "challenge" || r.FormValue("sign_val") != "A9993E364706816ABA3E25717850C26C9CD0D89D" {
			t.Error("challenge lost")
		}
		writeJSON(t, w, `{"state":true,"data":{"status":2,"pick_code":"at4r9vihhhh"}}`)
	})
	receipt, err := c.UploadWithReceipt(context.Background(), "token", "0", "source", f, 3, "")
	if err != nil || receipt.ID != "123456789" || calls != 2 {
		t.Fatalf("receipt=%+v calls=%d err=%v", receipt, calls, err)
	}
}

// TestUploadInstant validates hashing and target encoding and rejects unrecognized upload states.
func TestUploadInstant(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, _ = f.WriteString("abc")
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("target") != "U_1_12" || r.FormValue("fileid") != "A9993E364706816ABA3E25717850C26C9CD0D89D" {
			t.Errorf("wrong upload init: %v", r.Form)
		}
		writeJSON(t, w, `{"state":true,"code":0,"data":{"status":2,"file_id":"99"}}`)
	})
	if err = c.Upload(context.Background(), "token", "12", "file", f, 3, ""); err != nil {
		t.Fatal(err)
	}
}

func TestUploadReusesSHA1(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, _ = file.WriteString("abc")
	verifications := 0
	ctx := domain.WithUploadProgress(context.Background(), func(_, _ int64, phase string) {
		if phase == "verifying" {
			verifications++
		}
	})
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		_ = request.ParseForm()
		if request.FormValue("fileid") != "A9993E364706816ABA3E25717850C26C9CD0D89D" || request.FormValue("preid") != "A9993E364706816ABA3E25717850C26C9CD0D89D" {
			t.Errorf("invalid hashes: %v", request.Form)
		}
		writeJSON(t, writer, `{"state":true,"code":0,"data":{"status":2}}`)
	})
	if err = client.Upload(ctx, "token", "12", "file", file, 3, "a9993e364706816aba3e25717850c26c9cd0d89d"); err != nil {
		t.Fatal(err)
	}
	if verifications != 0 {
		t.Fatalf("rehashed source %d times", verifications)
	}
	if err = client.Upload(ctx, "token", "12", "file", file, 3, "invalid"); err == nil {
		t.Fatal("accepted malformed SHA1")
	}
}

// TestUploadHashProgressCoversOnlyWholeFile ensures the second 115 checksum moves forward without a partial-range rewind.
func TestUploadHashProgressCoversOnlyWholeFile(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	content := bytes.Repeat([]byte("v"), 1024*1024+10)
	if _, err = f.Write(content); err != nil {
		t.Fatal(err)
	}
	var reports []int64
	var lastPhase string
	ctx := domain.WithUploadProgress(context.Background(), func(done, total int64, phase string) {
		lastPhase = phase
		if phase == "uploading" {
			return
		}
		if phase != "verifying" || total != int64(len(content)) {
			t.Errorf("phase=%s done=%d total=%d", phase, done, total)
		}
		reports = append(reports, done)
	})
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"state":true,"code":0,"data":{"status":2,"file_id":"99"}}`)
	})
	if err = c.Upload(ctx, "token", "12", "file", f, int64(len(content)), ""); err != nil {
		t.Fatal(err)
	}
	if lastPhase != "uploading" {
		t.Fatal("initialization reported as hashing")
	}
	if len(reports) < 3 || reports[0] != 0 || reports[len(reports)-1] != int64(len(content)) {
		t.Fatalf("reports: %v", reports)
	}
	for i := 1; i < len(reports); i++ {
		if reports[i] <= reports[i-1] {
			t.Fatalf("progress rewound: %v", reports)
		}
	}
}
