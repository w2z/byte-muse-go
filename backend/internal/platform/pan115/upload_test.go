package pan115

import (
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
