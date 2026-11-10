package pan115

import (
	"bytemuse/backend/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUploadHTTPClientBoundsStallsWithoutTotalDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/stall" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Length", "8")
		for i := 0; i < 8; i++ {
			_, _ = w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			time.Sleep(30 * time.Millisecond)
		}
	}))
	defer server.Close()
	client := uploadHTTPClient(nil, 150*time.Millisecond)
	defer client.CloseIdleConnections()
	if client.Timeout != 0 {
		t.Fatal("ordinary multipart transfers must not have a total deadline")
	}
	resp, err := client.Get(server.URL + "/progress")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || len(body) != 8 {
		t.Fatalf("active transfer timed out: %q %v", body, err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	go func() {
		defer writer.Close()
		for i := 0; i < 12; i++ {
			if _, err := writer.Write([]byte("x")); err != nil {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	}()
	resp, err = client.Post(server.URL, "application/octet-stream", reader)
	if err != nil {
		t.Fatalf("active upload timed out while awaiting response: %v", err)
	}
	resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/stall", nil)
	if _, err = client.Do(req); err == nil || ctx.Err() != nil {
		t.Fatalf("stall not bounded independently of task context: %v", err)
	}
}

const multipartTestHash = "A9993E364706816ABA3E25717850C26C9CD0D89D"

// multipartTransport keeps the real OSS signing, XML codec and request construction in tests.
type multipartTransport func(*http.Request) (*http.Response, error)

func (f multipartTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// multipartClient routes only the provider's OSS host to an in-memory HTTP peer.
func multipartClient(t *testing.T, initAllowed bool, handler http.HandlerFunc) (*Client, *int, *int) {
	t.Helper()
	initializations, tokens := 0, 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open/upload/init":
			initializations++
			if !initAllowed {
				writeJSON(t, w, `{"state":false,"code":400,"message":"must resume existing session"}`)
				return
			}
			writeJSON(t, w, `{"state":true,"data":{"status":1,"bucket":"test-bucket","object":"test-object","callback":{"callback":"callback","callback_var":"vars"}}}`)
		case "/open/upload/get_token":
			tokens++
			writeJSON(t, w, fmt.Sprintf(`{"state":true,"data":{"endpoint":"https://oss-cn-test.aliyuncs.com","AccessKeyId":"test-id","AccessKeySecret":"test-secret","SecurityToken":"test-token-%d"}}`, tokens))
		default:
			t.Errorf("unexpected provider path %s", r.URL.Path)
		}
	})
	base := c.http.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.http.Transport = multipartTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Hostname(), ".aliyuncs.com") {
			recorder := httptest.NewRecorder()
			handler(recorder, r)
			return recorder.Result(), nil
		}
		return base.RoundTrip(r)
	})
	return c, &initializations, &tokens
}

// multipartSource allocates test bytes without storing any provider data on disk.
func multipartSource(t *testing.T, size int64) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "source"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if err = f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	return f
}

func multipartSession() *domain.UploadMultipart {
	return &domain.UploadMultipart{Account: "account", Parent: "12", Name: "file", SHA1: multipartTestHash, Size: 6, Modified: 7, Bucket: "test-bucket", Object: "test-object", UploadID: "upload-one", PartSize: 3, Callback: "callback", CallbackVar: "vars"}
}

func multipartList(parts, tail string) string {
	return `<ListPartsResult><Bucket>test-bucket</Bucket><Key>test-object</Key><UploadId>upload-one</UploadId>` + parts + tail + `</ListPartsResult>`
}

func multipartPart(number, size int) string {
	return fmt.Sprintf(`<Part><PartNumber>%d</PartNumber><ETag>etag-%d</ETag><Size>%d</Size></Part>`, number, number, size)
}

func multipartCallback(size int64) string {
	return fmt.Sprintf(`{"state":true,"data":{"file_id":"99","file_name":"file","file_size":%d,"sha1":"%s"}}`, size, multipartTestHash)
}

// TestUploadResumesMultipart catches reinitialization, resending persisted parts and progress rewind.
func TestUploadResumesMultipart(t *testing.T) {
	session := multipartSession()
	var progress []int64
	var uploaded []string
	c, initializations, _ := multipartClient(t, false, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("uploadId") != "upload-one" {
			t.Errorf("wrong session: %s", r.URL.RawQuery)
		}
		switch r.Method {
		case "GET":
			io.WriteString(w, multipartList(multipartPart(1, 3), ""))
		case "PUT":
			uploaded = append(uploaded, r.URL.Query().Get("partNumber"))
			io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", "etag-2")
		case "POST":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "etag-1") || !strings.Contains(string(body), "etag-2") {
				t.Errorf("missing remote etags: %s", body)
			}
			io.WriteString(w, multipartCallback(6))
		default:
			t.Errorf("unexpected OSS method %s", r.Method)
		}
	})
	journal := &domain.UploadMultipartJournal{Account: "account", Modified: 7, Session: session, Save: func(context.Context, *domain.UploadMultipart) error {
		t.Error("resume must not overwrite checkpoint")
		return nil
	}}
	ctx := domain.WithUploadProgress(domain.WithUploadMultipart(context.Background(), journal), func(done, total int64, phase string) {
		if phase == "uploading" {
			progress = append(progress, done)
		}
	})
	receipt, err := c.UploadWithReceipt(ctx, "token", "12", "file", multipartSource(t, 6), 6, multipartTestHash)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ID != "99" || *initializations != 0 || strings.Join(uploaded, ",") != "2" {
		t.Fatalf("receipt=%+v init=%d parts=%v", receipt, *initializations, uploaded)
	}
	if len(progress) == 0 || progress[0] != 3 || progress[len(progress)-1] != 6 {
		t.Fatalf("progress=%v", progress)
	}
	if journal.Session == nil {
		t.Fatal("protocol cleared journal before caller saved receipt")
	}
}

// TestUploadMultipartBindingRejectsWrongSource prevents old session reuse across source/account versions.
func TestUploadMultipartBindingRejectsWrongSource(t *testing.T) {
	for _, field := range []string{"account", "parent", "name", "hash", "size", "modified"} {
		t.Run(field, func(t *testing.T) {
			s := multipartSession()
			switch field {
			case "account":
				s.Account = "other"
			case "parent":
				s.Parent = "other"
			case "name":
				s.Name = "other"
			case "hash":
				s.SHA1 = strings.Repeat("B", 40)
			case "size":
				s.Size++
			case "modified":
				s.Modified++
			}
			c, initializations, tokens := multipartClient(t, false, func(http.ResponseWriter, *http.Request) { t.Error("invalid binding reached OSS") })
			ctx := domain.WithUploadMultipart(context.Background(), &domain.UploadMultipartJournal{Account: "account", Modified: 7, Session: s})
			if err := c.Upload(ctx, "token", "12", "file", multipartSource(t, 6), 6, multipartTestHash); err == nil {
				t.Fatal("accepted invalid binding")
			}
			if *initializations != 0 || *tokens != 0 {
				t.Fatal("invalid binding reached provider")
			}
		})
	}
}

// TestUploadMultipartInterruptedPersistsBeforeBytes verifies crash recovery uses the original ID.
func TestUploadMultipartInterruptedPersistsBeforeBytes(t *testing.T) {
	const size = int64(16*1024*1024 + 3)
	var saved *domain.UploadMultipart
	var partNumbers []string
	interrupted := false
	aborts := 0
	c, initializations, _ := multipartClient(t, true, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.Method == "POST" && q.Has("uploads"):
			io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>test-bucket</Bucket><Key>test-object</Key><UploadId>upload-one</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == "GET":
			io.WriteString(w, multipartList(multipartPart(1, 16*1024*1024), ""))
		case r.Method == "PUT":
			if saved == nil {
				t.Fatal("sent bytes before checkpoint")
			}
			if q.Get("uploadId") != "upload-one" {
				t.Error("session changed")
			}
			partNumbers = append(partNumbers, q.Get("partNumber"))
			io.Copy(io.Discard, r.Body)
			if q.Get("partNumber") == "2" && !interrupted {
				interrupted = true
				w.WriteHeader(503)
				io.WriteString(w, `<Error><Code>ServiceUnavailable</Code></Error>`)
				return
			}
			w.Header().Set("ETag", "etag-"+q.Get("partNumber"))
		case r.Method == "POST":
			io.WriteString(w, multipartCallback(size))
		case r.Method == "DELETE":
			aborts++
			w.WriteHeader(204)
		}
	})
	// Convert the interruption response into an actual transport failure after the peer consumed part 2.
	transport := c.http.Transport
	c.http.Transport = multipartTransport(func(r *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(r)
		if response != nil && response.StatusCode == 503 {
			response.Body.Close()
			return nil, io.ErrUnexpectedEOF
		}
		return response, err
	})
	journal := &domain.UploadMultipartJournal{Account: "account", Modified: 7, Save: func(_ context.Context, s *domain.UploadMultipart) error { copy := *s; saved = &copy; return nil }}
	ctx := domain.WithUploadMultipart(context.Background(), journal)
	f := multipartSource(t, size)
	if err := c.Upload(ctx, "token", "12", "file", f, size, multipartTestHash); err == nil {
		t.Fatal("expected interrupted transfer")
	}
	if saved == nil || aborts != 0 {
		t.Fatalf("checkpoint=%+v aborts=%d", saved, aborts)
	}
	encoded, _ := json.Marshal(saved)
	if strings.Contains(string(encoded), "test-token") || strings.Contains(string(encoded), "test-secret") {
		t.Fatal("persisted credentials")
	}
	journal.Session = saved
	if err := c.Upload(ctx, "token", "12", "file", f, size, multipartTestHash); err != nil {
		t.Fatal(err)
	}
	if *initializations != 1 || strings.Join(partNumbers, ",") != "1,2,2" || aborts != 0 {
		t.Fatalf("inits=%d parts=%v aborts=%d", *initializations, partNumbers, aborts)
	}
}

// TestUploadMultipartSaveFailureSendsNoBytes prevents unjournaled parts after a database failure.
func TestUploadMultipartSaveFailureSendsNoBytes(t *testing.T) {
	want := errors.New("storage unavailable")
	aborts, saves := 0, 0
	c, _, _ := multipartClient(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && r.URL.Query().Get("uploadId") == "upload-one" {
			aborts++
			w.WriteHeader(204)
			return
		}
		if r.Method != "POST" || !r.URL.Query().Has("uploads") {
			t.Errorf("sent request after checkpoint failure: %s", r.Method)
		}
		io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>test-bucket</Bucket><Key>test-object</Key><UploadId>upload-one</UploadId></InitiateMultipartUploadResult>`)
	})
	journal := &domain.UploadMultipartJournal{Account: "account", Modified: 7, Save: func(context.Context, *domain.UploadMultipart) error { saves++; return want }}
	ctx := domain.WithUploadMultipart(context.Background(), journal)
	if err := c.Upload(ctx, "token", "12", "file", multipartSource(t, 6), 6, multipartTestHash); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
	if saves != 2 || aborts != 1 || journal.Session == nil {
		t.Fatalf("saves=%d aborts=%d session=%+v", saves, aborts, journal.Session)
	}
}

// TestUploadMultipartSaveCancellationRecoversJournal preserves the newly issued ID when pause races initiation.
func TestUploadMultipartSaveCancellationRecoversJournal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	saves := 0
	c, _, _ := multipartClient(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !r.URL.Query().Has("uploads") {
			t.Errorf("paused new session reached %s", r.Method)
		}
		cancel()
		io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>test-bucket</Bucket><Key>test-object</Key><UploadId>upload-one</UploadId></InitiateMultipartUploadResult>`)
	})
	journal := &domain.UploadMultipartJournal{Account: "account", Modified: 7, Save: func(saveCtx context.Context, s *domain.UploadMultipart) error {
		saves++
		if saves == 1 {
			return saveCtx.Err()
		}
		if saveCtx.Err() != nil || s.UploadID != "upload-one" {
			t.Error("recovery could not save paused session")
		}
		return nil
	}}
	if err := c.Upload(domain.WithUploadMultipart(ctx, journal), "token", "12", "file", multipartSource(t, 6), 6, multipartTestHash); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if saves != 2 || journal.Session == nil {
		t.Fatalf("saves=%d session=%+v", saves, journal.Session)
	}
}

// TestUploadMultipartPersistentExpiryIsBounded prevents provider denial from becoming an unbounded retry loop.
func TestUploadMultipartPersistentExpiryIsBounded(t *testing.T) {
	calls := 0
	c, _, tokens := multipartClient(t, false, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(403)
		io.WriteString(w, `<Error><Code>SecurityTokenExpired</Code></Error>`)
	})
	ctx := domain.WithUploadMultipart(context.Background(), &domain.UploadMultipartJournal{Account: "account", Modified: 7, Session: multipartSession()})
	err := c.Upload(ctx, "token", "12", "file", multipartSource(t, 6), 6, multipartTestHash)
	if !isUploadCredentialExpired(err) || calls != 2 || *tokens != 2 {
		t.Fatalf("err=%v calls=%d tokens=%d", err, calls, *tokens)
	}
}

// TestUploadAbortRejectsUntrustedEndpoint protects persisted coordinates from credential exfiltration.
func TestUploadAbortRejectsUntrustedEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://oss-cn-test.aliyuncs.com", "https://aliyuncs.com.attacker.test", "https://user@oss-cn-test.aliyuncs.com", "https://oss-cn-test.aliyuncs.com/private", "https://oss-cn-test.aliyuncs.com:8443"} {
		t.Run(endpoint, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, fmt.Sprintf(`{"state":true,"data":{"endpoint":%q,"AccessKeyId":"id","AccessKeySecret":"secret","SecurityToken":"token"}}`, endpoint))
			})
			base := c.http.Transport
			c.http.Transport = multipartTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Hostname(), "aliyuncs") {
					t.Error("untrusted endpoint received credentials")
					return nil, errors.New("forbidden")
				}
				return base.RoundTrip(r)
			})
			if err := c.AbortUpload(context.Background(), "token", *multipartSession()); err == nil {
				t.Fatal("accepted untrusted endpoint")
			}
		})
	}
}

// TestUploadMultipartExpiryRetriesOnlyFailedOperation protects identity and reader rewind on every STS boundary.
func TestUploadMultipartExpiryRetriesOnlyFailedOperation(t *testing.T) {
	for _, operation := range []string{"initiate", "list", "part", "complete", "abort"} {
		t.Run(operation, func(t *testing.T) {
			attempts := map[string]int{}
			c, initializations, tokens := multipartClient(t, operation == "initiate", func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				current := map[string]string{"GET": "list", "PUT": "part", "POST": "complete", "DELETE": "abort"}[r.Method]
				if q.Has("uploads") {
					current = "initiate"
				} else if q.Get("uploadId") != "upload-one" {
					t.Error("credential refresh changed upload ID")
				}
				attempts[current]++
				if current == "part" {
					body, err := io.ReadAll(r.Body)
					if err != nil || len(body) != 6 {
						t.Errorf("retry did not rewind part: len=%d err=%v", len(body), err)
					}
				}
				if current == operation && attempts[current] == 1 {
					w.WriteHeader(403)
					io.WriteString(w, `<Error><Code>SecurityTokenExpired</Code><Message>expired</Message></Error>`)
					return
				}
				if attempts[operation] >= 1 && r.Header.Get("X-Oss-Security-Token") != "test-token-2" {
					t.Errorf("stale credential for %s: %s", current, r.Header.Get("X-Oss-Security-Token"))
				}
				switch current {
				case "initiate":
					io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>test-bucket</Bucket><Key>test-object</Key><UploadId>upload-one</UploadId></InitiateMultipartUploadResult>`)
				case "list":
					io.WriteString(w, multipartList("", ""))
				case "part":
					w.Header().Set("ETag", "part-etag")
				case "complete":
					io.WriteString(w, multipartCallback(6))
				case "abort":
					w.WriteHeader(204)
				}
			})
			s := multipartSession()
			s.PartSize = 6
			journal := &domain.UploadMultipartJournal{Account: "account", Modified: 7, Session: s, Save: func(context.Context, *domain.UploadMultipart) error { return nil }}
			if operation == "initiate" {
				journal.Session = nil
			}
			var err error
			if operation == "abort" {
				err = c.AbortUpload(context.Background(), "token", *s)
			} else {
				err = c.Upload(domain.WithUploadMultipart(context.Background(), journal), "token", "12", "file", multipartSource(t, 6), 6, multipartTestHash)
			}
			if err != nil {
				t.Fatal(err)
			}
			if *tokens != 2 || attempts[operation] != 2 {
				t.Fatalf("tokens=%d operations=%v", *tokens, attempts)
			}
			if operation != "initiate" && *initializations != 0 {
				t.Fatal("STS expiry restarted 115 upload")
			}
		})
	}
}

// TestUploadMultipartMalformedListDoesNotSendBytes rejects corrupted, duplicate, foreign and looping listings.
func TestUploadMultipartMalformedListDoesNotSendBytes(t *testing.T) {
	for _, fixture := range []string{
		multipartList(multipartPart(0, 3), ""),
		multipartList(multipartPart(3, 3), ""),
		multipartList(multipartPart(1, 2), ""),
		multipartList(multipartPart(1, 3)+multipartPart(1, 3), ""),
		multipartList(`<Part><PartNumber>1</PartNumber><Size>3</Size></Part>`, ""),
		multipartList(multipartPart(1, 3), `<IsTruncated>true</IsTruncated><NextPartNumberMarker>0</NextPartNumberMarker>`),
		multipartList(multipartPart(1, 3), `<IsTruncated>true</IsTruncated><NextPartNumberMarker>2</NextPartNumberMarker>`),
		strings.Replace(multipartList("", ""), "upload-one", "foreign-session", 1),
	} {
		t.Run(fmt.Sprint(len(fixture)), func(t *testing.T) {
			c, _, _ := multipartClient(t, false, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("malformed list reached %s", r.Method)
				}
				io.WriteString(w, fixture)
			})
			ctx := domain.WithUploadMultipart(context.Background(), &domain.UploadMultipartJournal{Account: "account", Modified: 7, Session: multipartSession()})
			if err := c.Upload(ctx, "token", "12", "file", multipartSource(t, 6), 6, multipartTestHash); err == nil {
				t.Fatal("accepted malformed listing")
			}
		})
	}
}

// TestUploadMultipartPaginationUsesRemoteETags verifies completion includes all OSS pages without part retransmission.
func TestUploadMultipartPaginationUsesRemoteETags(t *testing.T) {
	var markers []string
	c, _, _ := multipartClient(t, false, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			marker := r.URL.Query().Get("part-number-marker")
			markers = append(markers, marker)
			if marker == "0" {
				io.WriteString(w, multipartList(multipartPart(1, 3), `<IsTruncated>true</IsTruncated><NextPartNumberMarker>1</NextPartNumberMarker>`))
			} else {
				io.WriteString(w, multipartList(multipartPart(2, 3), ""))
			}
		case "POST":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "etag-1") || !strings.Contains(string(body), "etag-2") {
				t.Errorf("missing part: %s", body)
			}
			io.WriteString(w, multipartCallback(6))
		default:
			t.Errorf("completed parts should not be retransmitted: %s", r.Method)
		}
	})
	ctx := domain.WithUploadMultipart(context.Background(), &domain.UploadMultipartJournal{Account: "account", Modified: 7, Session: multipartSession()})
	if err := c.Upload(ctx, "token", "12", "file", multipartSource(t, 6), 6, multipartTestHash); err != nil {
		t.Fatal(err)
	}
	if strings.Join(markers, ",") != "0,1" {
		t.Fatalf("markers=%v", markers)
	}
}

// TestUploadMultipartMissingMustReconcile distinguishes ambiguous completion from idempotent stop cleanup.
func TestUploadMultipartMissingMustReconcile(t *testing.T) {
	c, initializations, _ := multipartClient(t, false, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "DELETE" {
			t.Errorf("unexpected operation %s", r.Method)
		}
		if r.URL.Query().Get("uploadId") != "upload-one" || r.URL.Path != "/test-object" {
			t.Errorf("wrong cleanup target %s", r.URL.String())
		}
		w.WriteHeader(404)
		io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
	})
	journal := &domain.UploadMultipartJournal{Account: "account", Modified: 7, Session: multipartSession()}
	ctx := domain.WithUploadMultipart(context.Background(), journal)
	if err := c.Upload(ctx, "token", "12", "file", multipartSource(t, 6), 6, multipartTestHash); !errors.Is(err, ErrUploadSessionMissing) {
		t.Fatalf("err=%v", err)
	}
	if err := c.AbortUpload(context.Background(), "token", *journal.Session); err != nil {
		t.Fatal(err)
	}
	if *initializations != 0 || journal.Session == nil {
		t.Fatal("missing session was silently restarted or cleared")
	}
}

// TestUploadMultipartCancellationRetainsSession makes pause/restart safe without issuing a remote abort.
func TestUploadMultipartCancellationRetainsSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _, _ := multipartClient(t, false, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, multipartList("", ""))
			return
		}
		if r.Method == "PUT" {
			cancel()
			io.WriteString(w, "cancelled")
			w.Header().Set("ETag", "etag")
			return
		}
		t.Errorf("cancelled upload reached %s", r.Method)
	})
	journal := &domain.UploadMultipartJournal{Account: "account", Modified: 7, Session: multipartSession()}
	err := c.Upload(domain.WithUploadMultipart(ctx, journal), "token", "12", "file", multipartSource(t, 6), 6, multipartTestHash)
	if err == nil || journal.Session == nil {
		t.Fatalf("err=%v journal=%+v", err, journal)
	}
}
