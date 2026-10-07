package release

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain 提供隔离子服务进程，只写测试临时目录，不连接真实数据库或第三方。
func TestMain(m *testing.M) {
	if os.Getenv("BYTEMUSE_TEST_SERVICE") == "1" && len(os.Args) == 2 && os.Args[1] == "serve" {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer cancel()
		server := &http.Server{Addr: os.Getenv("BYTEMUSE_TEST_ADDR"), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })}
		_ = os.WriteFile(os.Getenv("BYTEMUSE_TEST_MARKER"), []byte(fmt.Sprintf("%d:%s:%s", os.Getpid(), os.Getenv("BYTEMUSE_VERSION"), os.Getenv("WEB_STATIC_DIR"))), 0600)
		go server.ListenAndServe()
		<-ctx.Done()
		_ = server.Close()
		return
	}
	os.Exit(m.Run())
}

// TestSupervisorSwitch 验证真实子进程退出并替换，父进程保持不变，版本与静态资源目录同步切换。
func TestSupervisorSwitch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("容器启动器只在 Linux 运行")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "child")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	t.Setenv("BYTEMUSE_TEST_SERVICE", "1")
	t.Setenv("BYTEMUSE_TEST_MARKER", marker)
	t.Setenv("BYTEMUSE_TEST_ADDR", address)
	executable, _ := os.Executable()
	bytes, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "releases", "staging-test")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bytemuse"), bytes, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Supervise(ctx, root, executable, "/original/web", "0.1.21", address, time.Second, io.Discard)
	}()
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("等待进程切换超时")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	wait(func() bool { body, _ := os.ReadFile(marker); return strings.Contains(string(body), ":0.1.21:") })
	before, _ := os.ReadFile(marker)
	parent := os.Getpid()
	if err := writeRecord(root, "pending.json", Activation{Directory: "staging-test", Version: "0.1.22"}); err != nil {
		t.Fatal(err)
	}
	installer := &PackageInstaller{Root: root}
	wait(func() bool { return installer.Status().Phase == "success" })
	after, _ := os.ReadFile(marker)
	if strings.Split(string(before), ":")[0] == strings.Split(string(after), ":")[0] || os.Getpid() != parent || !strings.Contains(string(after), ":0.1.22:"+filepath.Join(directory, "web")) {
		t.Fatalf("before=%s after=%s", before, after)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
