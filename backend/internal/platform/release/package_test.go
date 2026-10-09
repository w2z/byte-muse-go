package release

import (
	"archive/tar"
	"bytemuse/backend/internal/ports"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPackageFallback 校验直连损坏包会删除后通过代理完整重下，成功与未配置时不多发请求。
func TestPackageFallback(t *testing.T) {
	for _, mode := range []string{"direct", "proxy", "unconfigured", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			body := "verified release package"
			checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls []string
			client := func(route string) *http.Client {
				return &http.Client{Transport: packageTransport(func(r *http.Request) (*http.Response, error) {
					kind, payload := "package", body
					if strings.HasSuffix(r.URL.Path, ".sha256") {
						kind, payload = "checksum", checksum
					}
					calls = append(calls, route+":"+kind)
					if route == "direct" && kind == "package" && mode != "direct" {
						payload = "corrupt"
					}
					if mode == "cancel" {
						cancel()
						return nil, context.Canceled
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
				})}
			}
			installer := &PackageInstaller{Client: client("direct"), ProxyClient: func(context.Context) (*http.Client, error) {
				if mode == "unconfigured" {
					return nil, nil
				}
				return client("proxy"), nil
			}}
			archive := filepath.Join(t.TempDir(), "package.tgz")
			err := installer.download(ctx, "https://github.com/w2z/byte-muse-go/releases/download/v0.1.101/package.tgz", archive)
			want := map[string]string{"direct": "direct:checksum,direct:package", "proxy": "direct:checksum,direct:package,proxy:checksum,proxy:package", "unconfigured": "direct:checksum,direct:package", "cancel": "direct:checksum"}[mode]
			if strings.Join(calls, ",") != want {
				t.Fatalf("calls=%v", calls)
			}
			if mode == "direct" || mode == "proxy" {
				if err != nil {
					t.Fatal(err)
				}
				actual, _ := os.ReadFile(archive)
				if string(actual) != body {
					t.Fatal("升级包不是完整的校验结果")
				}
			} else if err == nil {
				t.Fatal("失败应当返回错误")
			}
		})
	}
}

type packageTransport func(*http.Request) (*http.Response, error)

// TestDownloadByteProgress 验证真实字节读取过程可见，未知总量不虚构总数。
func TestDownloadByteProgress(t *testing.T) {
	body := strings.Repeat("release", 20000)
	for _, total := range []int64{int64(len(body)), -1} {
		client := &http.Client{Transport: packageTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), ContentLength: total, Header: make(http.Header)}, nil
		})}
		var values []int64
		err := downloadFile(context.Background(), client, "https://github.com/w2z/byte-muse-go/package", filepath.Join(t.TempDir(), "package"), fmt.Sprintf("%x", sha256.Sum256([]byte(body))), func(done, size int64) {
			if size != total {
				t.Errorf("total=%d want=%d", size, total)
			}
			values = append(values, done)
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(values) < 3 || values[0] != 0 || values[len(values)-1] != int64(len(body)) {
			t.Fatalf("values=%v", values)
		}
		for i := 1; i < len(values); i++ {
			if values[i] <= values[i-1] {
				t.Fatalf("进度未递增: %v", values)
			}
		}
	}
}

func (f packageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestStageProgress 使用真实运行包完成暂存，验证阶段报告与持久化一致，且不会提前重启。
func TestStageProgress(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("升级安装器只在 Linux 运行")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"bytemuse": binary, "web/index.html": []byte("html"), "version.json": []byte(`{"version":"0.1.22","protocol":1}`)} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0700, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	transport := packageTransport(func(r *http.Request) (*http.Response, error) {
		body := archive.Bytes()
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			body = []byte(fmt.Sprintf("%x", sha256.Sum256(body)))
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Header: make(http.Header)}, nil
	})
	installer := &PackageInstaller{Root: t.TempDir(), Repo: "w2z/byte-muse-go", Client: &http.Client{Transport: transport}}
	var phases []string
	lastProgress := map[string]int{}
	intermediatePhases := map[string]bool{}
	var intermediate bool
	err = installer.Stage(context.Background(), "0.1.22", func(state ports.UpgradeStatus) {
		previous, seen := lastProgress[state.Phase]
		if (!seen && state.PhaseProgressPercent != 0) || state.PhaseProgressPercent < previous || state.PhaseProgressPercent > 100 {
			t.Errorf("步骤进度未独立从零递增: %+v previous=%d", state, previous)
		}
		lastProgress[state.Phase] = state.PhaseProgressPercent
		if state.PhaseProgressPercent > 0 && state.PhaseProgressPercent < 100 {
			intermediatePhases[state.Phase] = true
		}
		if state.ProgressPercent > state.CompletedSteps*25 {
			intermediate = true
		}
		if state.ProgressPercent < state.CompletedSteps*25 || state.ProgressPercent >= (state.CompletedSteps+1)*25 {
			t.Errorf("progress=%+v", state)
		}
		if persisted := installer.Status(); persisted.Phase != state.Phase || persisted.CompletedSteps != state.CompletedSteps {
			t.Errorf("persisted=%+v state=%+v", persisted, state)
		}
		if _, err := os.Stat(filepath.Join(installer.Root, "pending.json")); !os.IsNotExist(err) && !(state.Phase == "installing" && state.PhaseProgressPercent == 100) {
			t.Error("重启请求提前提交")
		}
		if len(phases) == 0 || phases[len(phases)-1] != state.Phase {
			phases = append(phases, state.Phase)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(phases, ",") != "downloading,extracting,installing" {
		t.Fatalf("phases=%v", phases)
	}
	if !intermediate {
		t.Fatal("缺少阶段内部实际进度")
	}
	for _, phase := range phases {
		if lastProgress[phase] != 100 || !intermediatePhases[phase] {
			t.Errorf("%s 未独立完成: progress=%d intermediate=%v", phase, lastProgress[phase], intermediatePhases[phase])
		}
	}
	if _, err := readActivation(installer.Root, "pending.json"); err != nil {
		t.Fatal(err)
	}
}

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
		err := downloadFile(context.Background(), nil, server.URL+tc.path, filepath.Join(t.TempDir(), "package"), tc.hash)
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
