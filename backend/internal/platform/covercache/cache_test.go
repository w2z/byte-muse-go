package covercache

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngHeader 是最小 PNG 文件头：既能让下载校验通过，也能被内容嗅探识别成 image/png。
var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// imageServer 是返回固定内容的测试图床，同时记录被请求次数。
func imageServer(t *testing.T, contentType string, body []byte) (*httptest.Server, *int) {
	t.Helper()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		if contentType != "" {
			response.Header().Set("Content-Type", contentType)
		}
		_, _ = response.Write(body)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

// TestCoverNameNormalizesCode 验证文件名主干只保留字母、数字、连字符与下划线并统一小写，
// 例如 SONS-1223 -> sons-1223；无法安全命名的番号返回 ErrInvalidCode。
func TestCoverNameNormalizesCode(t *testing.T) {
	for _, testCase := range []struct{ code, want string }{
		{code: "SONS-1223", want: "sons-1223"},
		{code: "  Ssis_001  ", want: "ssis_001"},
		{code: "AB CD", want: "abcd"},
		{code: "ABC/../X", want: "abcx"},
	} {
		got, err := coverName(testCase.code)
		if err != nil || got != testCase.want {
			t.Fatalf("coverName(%q)=%q,%v，期望 %q", testCase.code, got, err, testCase.want)
		}
	}
	for _, code := range []string{"", "   ", "-_-", strings.Repeat("a", maxCoverNameLength+1)} {
		if _, err := coverName(code); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("coverName(%q) 期望 ErrInvalidCode，实际 %v", code, err)
		}
	}
}

// TestEnsureDownloadsOnceThenReusesLocalFile 验证首次下载按番号落盘、扩展名取图片真实格式，
// 之后命中本地文件不再访问源站，并且不残留临时文件。
func TestEnsureDownloadsOnceThenReusesLocalFile(t *testing.T) {
	server, requests := imageServer(t, "image/png", pngHeader)
	dir := t.TempDir()
	cache := New(dir, nil)

	file, err := cache.Ensure(context.Background(), "SONS-1223", server.URL+"/cover.jpg")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "sons-1223.png")
	if file.Path != want || file.ContentType != "image/png" {
		t.Fatalf("file=%+v，期望 %s 与 image/png", file, want)
	}
	if body, readErr := os.ReadFile(want); readErr != nil || !bytes.Equal(body, pngHeader) {
		t.Fatalf("落盘内容不符: %v", readErr)
	}

	again, err := cache.Ensure(context.Background(), "SONS-1223", server.URL+"/cover.jpg")
	if err != nil || again.Path != want {
		t.Fatalf("缓存未命中: %+v %v", again, err)
	}
	if *requests != 1 {
		t.Fatalf("源站请求次数=%d，期望 1", *requests)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("缓存目录应只保留一个文件，实际 %v（%v）", entries, err)
	}
}

// TestEnsureSniffsImageType 验证源站把类型写成二进制流时按图片内容判定落盘格式。
func TestEnsureSniffsImageType(t *testing.T) {
	server, _ := imageServer(t, "application/octet-stream", pngHeader)
	dir := t.TempDir()
	file, err := New(dir, nil).Ensure(context.Background(), "SSIS-001", server.URL+"/cover")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "ssis-001.png"); file.Path != want {
		t.Fatalf("file=%+v，期望 %s", file, want)
	}
}

// TestEnsureRejectsUnusableSource 验证不可用源地址、非图片响应、404 与超限封面都返回 ErrSource，
// 调用方据此回退源站地址，而不是落盘一个坏文件或半张图片。
func TestEnsureRejectsUnusableSource(t *testing.T) {
	dir := t.TempDir()
	cache := New(dir, nil)
	for _, source := range []string{"", "ftp://example.test/a.png", "/relative/a.png", "https://user:pass@example.test/a.png"} {
		if _, err := cache.Ensure(context.Background(), "SONS-1223", source); !errors.Is(err, ErrSource) {
			t.Fatalf("源地址 %q 期望 ErrSource，实际 %v", source, err)
		}
	}
	if _, err := New("", nil).Ensure(context.Background(), "SONS-1223", "https://example.test/a.png"); !errors.Is(err, ErrSource) {
		t.Fatalf("未配置目录期望 ErrSource，实际 %v", err)
	}

	notImage, _ := imageServer(t, "text/html", []byte("<html></html>"))
	if _, err := cache.Ensure(context.Background(), "SONS-1223", notImage.URL+"/a.html"); !errors.Is(err, ErrSource) {
		t.Fatalf("非图片响应期望 ErrSource，实际 %v", err)
	}
	missing := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(missing.Close)
	if _, err := cache.Ensure(context.Background(), "SONS-1223", missing.URL+"/a.png"); !errors.Is(err, ErrSource) {
		t.Fatalf("404 期望 ErrSource，实际 %v", err)
	}
	oversized, _ := imageServer(t, "image/png", append(append([]byte{}, pngHeader...), bytes.Repeat([]byte{0}, maxCoverBytes)...))
	if _, err := cache.Ensure(context.Background(), "SONS-1223", oversized.URL+"/a.png"); !errors.Is(err, ErrSource) {
		t.Fatalf("超限封面期望 ErrSource，实际 %v", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("失败请求不应落盘: %v %v", entries, err)
	}
}
