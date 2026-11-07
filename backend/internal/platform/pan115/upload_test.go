package pan115

import (
	"bytemuse/backend/internal/domain"
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

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
	if err = c.Upload(context.Background(), "token", "12", "file", f, 3); err != nil {
		t.Fatal(err)
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
	if err = c.Upload(ctx, "token", "12", "file", f, int64(len(content))); err != nil {
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
