package downloadclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const thunderTestMagnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"

// thunderFakeGateway 复刻旧版 pan-xunlei-com 网关的响应结构：首页内联 uiauth、设备任务、磁力解析和任务创建。
func thunderFakeGateway(t *testing.T, existingURL string, created *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token" {
			t.Errorf("missing authorization header")
		}
		switch r.URL.Path {
		case thunderAPIPrefix + "/":
			_, _ = w.Write([]byte("<script>function uiauth(options) {\n  return \"pan-auth-value\";\n}</script>"))
		case thunderAPIPrefix + "/drive/v1/tasks":
			if r.Header.Get("pan-auth") != "pan-auth-value" {
				t.Errorf("missing pan-auth header")
			}
			switch r.URL.Query().Get("type") {
			case "user#runner":
				_, _ = w.Write([]byte(`{"tasks":[{"params":{"target":"device-1"}}]}`))
			case "user#download-url":
				if existingURL == "" {
					_, _ = w.Write([]byte(`{"tasks":[]}`))
				} else {
					payload, _ := json.Marshal(map[string]any{"tasks": []map[string]any{{"params": map[string]string{"url": existingURL}}}})
					_, _ = w.Write(payload)
				}
			default:
				t.Errorf("unexpected task type %s", r.URL.Query().Get("type"))
			}
		case thunderAPIPrefix + "/drive/v1/resource/list":
			_, _ = w.Write([]byte(`{"list":{"resources":[{"name":"sample","dir":{"resources":[{"name":"big.mp4","file_size":5000000000},{"name":"small.mp4","file_size":1000},{"name":"also-big.mp4","file_size":2000000000}]}}]}}`))
		case thunderAPIPrefix + "/drive/v1/task":
			raw, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(raw, created); err != nil {
				t.Errorf("task payload: %v", err)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
}

func TestThunderSubmitsMagnetWithOnlyLargeFiles(t *testing.T) {
	var created map[string]any
	server := thunderFakeGateway(t, "", &created)
	defer server.Close()
	client := NewThunder(server.URL, "folder-9", "token", server.Client())
	if err := client.Submit(context.Background(), thunderTestMagnet); err != nil {
		t.Fatal(err)
	}
	params, ok := created["params"].(map[string]any)
	if !ok {
		t.Fatalf("payload=%v", created)
	}
	if params["parent_folder_id"] != "folder-9" || params["target"] != "device-1" || params["url"] != thunderTestMagnet {
		t.Fatalf("params=%v", params)
	}
	// 只提交大于 1GB 的文件：索引 0 与 2，跳过索引 1。
	if params["sub_file_index"] != "0,2" || params["total_file_count"] != "3" {
		t.Fatalf("selection=%v", params)
	}
	if created["file_size"] != "7000000000" || created["type"] != "user#download-url" || created["space"] != "device-1" {
		t.Fatalf("payload=%v", created)
	}
}

func TestThunderRechecksHashBeforeSubmission(t *testing.T) {
	var created map[string]any
	server := thunderFakeGateway(t, thunderTestMagnet, &created)
	defer server.Close()
	client := NewThunder(server.URL, "folder-9", "token", server.Client())
	found, err := client.HasHash(context.Background(), "0123456789abcdef0123456789abcdef01234567")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if created != nil {
		t.Fatalf("duplicate submission: %v", created)
	}
}

func TestThunderRejectsUnconfiguredAndInvalidMagnet(t *testing.T) {
	if err := NewThunder("", "", "", nil).Submit(context.Background(), thunderTestMagnet); err == nil {
		t.Fatal("unconfigured client must fail")
	}
	var created map[string]any
	server := thunderFakeGateway(t, "", &created)
	defer server.Close()
	if err := NewThunder(server.URL, "folder-9", "token", server.Client()).Submit(context.Background(), "http://example.com/a.torrent"); err == nil {
		t.Fatal("non-magnet must fail")
	}
}
