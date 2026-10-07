package clouddrive

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type uploadTransport func(*http.Request) (*http.Response, error)

func (f uploadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestUploadChecksCloudCompletion verifies CD2 acceptance, short writes and cloud failures independently.
func TestUploadChecksCloudCompletion(t *testing.T) {
	for _, scenario := range []string{"complete", "short_write", "cloud_failure"} {
		t.Run(scenario, func(t *testing.T) {
			file, err := os.Create(filepath.Join(t.TempDir(), "video"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err = file.WriteString("abc"); err != nil {
				t.Fatal(err)
			}
			waited := false
			client := New(Config{BaseURL: "https://cd2.invalid", Username: "test", Password: "test", HTTP: &http.Client{Transport: uploadTransport(func(r *http.Request) (*http.Response, error) {
				payload := &protoWriter{}
				switch strings.TrimPrefix(r.URL.Path, servicePath+"/") {
				case "CreateFile":
					payload.number(1, 42)
				case "WriteToFile":
					if scenario == "short_write" {
						payload.number(1, 2)
					} else {
						payload.number(1, 3)
					}
				case "CloseFile", "DeleteFile":
					payload.boolean(1, true)
				case "GetUploadFileList":
					waited = true
					row := &protoWriter{}
					row.str(2, "/cloud/stage")
					row.number(3, 3)
					row.number(4, 3)
					if scenario == "cloud_failure" {
						row.number(8, 9)
					} else {
						row.number(8, 5)
					}
					payload.bytes(2, row.buf)
				default:
					t.Errorf("unexpected RPC %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{grpcWebContentType}}, Body: io.NopCloser(strings.NewReader(string(frame(0, payload.buf))))}, nil
			})}})
			client.token, client.expiry = "test", time.Now().Add(time.Hour)
			err = client.Upload(context.Background(), "/cloud", "stage", file, 3)
			if scenario == "complete" && (err != nil || !waited) {
				t.Fatalf("completion=%v waited=%v", err, waited)
			}
			if scenario != "complete" && err == nil {
				t.Fatal("failed transfer reported success")
			}
		})
	}
}
