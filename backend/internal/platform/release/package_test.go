package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDownloadChecksum 验证损坏或尚未发布的升级包不会被接受。
func TestDownloadChecksum(t *testing.T) {
	body := "release package"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	for _, tc := range []struct {
		path, hash string
		valid      bool
	}{{"/ok", checksum, true}, {"/corrupt", strings.Repeat("0", 64), false}, {"/missing", checksum, false}} {
		err := downloadFile(context.Background(), server.URL+tc.path, filepath.Join(t.TempDir(), "package"), tc.hash)
		if (err == nil) != tc.valid {
			t.Fatalf("path=%s err=%v", tc.path, err)
		}
	}
}

// TestExtractPackage 验证升级包只能写入独立目录，拒绝链接、越界和不完整包。
func TestExtractPackage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		extra   string
		kind    byte
		missing bool
		valid   bool
	}{
		{name: "complete", valid: true},
		{name: "traversal", extra: "../escape"},
		{name: "absolute", extra: "/escape"},
		{name: "link", extra: "web/link", kind: tar.TypeSymlink},
		{name: "missing web", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			files := map[string]string{"bytemuse": "binary", "version.json": `{"version":"0.1.22","protocol":1}`}
			if !tc.missing {
				files["web/index.html"] = "html"
			}
			for name, body := range files {
				if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body))}); err != nil {
					t.Fatal(err)
				}
				_, _ = tw.Write([]byte(body))
			}
			if tc.extra != "" {
				_ = tw.WriteHeader(&tar.Header{Name: tc.extra, Typeflag: tc.kind, Linkname: "/etc/passwd"})
			}
			_ = tw.Close()
			_ = gz.Close()
			root := t.TempDir()
			archive := filepath.Join(root, "package.tgz")
			_ = os.WriteFile(archive, buf.Bytes(), 0600)
			err := extractPackage(archive, filepath.Join(root, "candidate"), "0.1.22")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
				t.Fatal("archive escaped destination")
			}
		})
	}
}
