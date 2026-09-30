package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bytemuse/backend/internal/application"
)

// TestScanStreamErrorAndJSON 验证原 JSON 错误语义与流式错误终态，避免 200 被误判为任务成功。
func TestScanStreamErrorAndJSON(t *testing.T) {
	for _, stream := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodPost, "/strm/scan", nil)
		if stream {
			r.Header.Set("Accept", "application/x-ndjson")
		}
		w := httptest.NewRecorder()
		streamScan(w, r, func(context.Context) (int, error) { return 0, application.ErrStrmNotConfigured }, writeStrmError)
		if !stream {
			if w.Code != 409 {
				t.Fatalf("JSON status=%d", w.Code)
			}
			continue
		}
		if !w.Flushed || w.Header().Get("Content-Type") != "application/x-ndjson" {
			t.Fatal("没有刷新流")
		}
		var last struct {
			Type  string
			Error struct{ Code string }
		}
		for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
			if err := json.Unmarshal([]byte(line), &last); err != nil {
				t.Fatal(err)
			}
		}
		if last.Type != "error" || last.Error.Code != "strm_not_configured" {
			t.Fatalf("错误终态: %+v", last)
		}
	}
}

// TestScanStreamResult 验证流末尾保留真实业务结果，空扫描也发送终态。
func TestScanStreamResult(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/strm/scan", nil)
	r.Header.Set("Accept", "application/x-ndjson")
	w := httptest.NewRecorder()
	streamScan(w, r, func(context.Context) (map[string]int, error) { return map[string]int{"files": 0}, nil }, writeStrmError)
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	var last struct {
		Type   string
		Result map[string]int
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Type != "result" || last.Result["files"] != 0 {
		t.Fatalf("结果丢失: %+v", last)
	}
}
