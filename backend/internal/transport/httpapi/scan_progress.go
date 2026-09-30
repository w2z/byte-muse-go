package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"bytemuse/backend/internal/application"
)

// streamScan 按 Accept 协商 NDJSON 进度流；未请求流时保留原 JSON 与 HTTP 错误语义。
// 每个请求独立追踪，断开连接取消扫描，不重放已产生副作用的请求。
func streamScan[T any](w http.ResponseWriter, r *http.Request, run func(context.Context) (T, error), writeFailure func(http.ResponseWriter, error)) {
	if !strings.Contains(r.Header.Get("Accept"), "application/x-ndjson") {
		result, err := run(r.Context())
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(w)
	controller := http.NewResponseController(w)
	send := func(event any) {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := encoder.Encode(event); err != nil {
			cancel()
			return
		}
		if err := controller.Flush(); err != nil {
			cancel()
		}
	}
	var last time.Time
	var phase string
	ctx = application.WithScanProgress(ctx, func(p application.ScanProgress) {
		// 保留阶段边界与最终计数，批量文件逐项处理时最多每 100ms 推送一次。
		if phase == p.Phase && p.Processed != p.Total && time.Since(last) < 100*time.Millisecond {
			return
		}
		last, phase = time.Now(), p.Phase
		send(map[string]any{"type": "progress", "progress": p})
	})
	send(map[string]any{"type": "progress", "progress": application.ScanProgress{Phase: "waiting"}})
	result, err := run(ctx)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		writeFailure(&scanErrorWriter{header: make(http.Header), send: send}, err)
		return
	}
	send(map[string]any{"type": "result", "result": result})
}

// scanErrorWriter 复用现有错误映射，将脱敏错误 JSON 封装为流末尾的 error 事件。
type scanErrorWriter struct {
	header http.Header
	send   func(any)
}

func (w *scanErrorWriter) Header() http.Header { return w.header }
func (w *scanErrorWriter) WriteHeader(int)     {}
func (w *scanErrorWriter) Write(data []byte) (int, error) {
	w.send(map[string]any{"type": "error", "error": json.RawMessage(data)})
	return len(data), nil
}
