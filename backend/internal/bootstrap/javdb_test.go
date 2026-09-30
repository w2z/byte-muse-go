package bootstrap

import (
	"bytemuse/backend/internal/platform/javdbapp"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestJavDBResourceSearcherReusesClient 验证装配入口保存客户端，不为每次资源搜索重置设备身份。
func TestJavDBResourceSearcherReusesClient(t *testing.T) {
	device := ""
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		current := r.URL.Query().Get("device_uuid")
		if current == "" || (device != "" && current != device) {
			t.Error("device identity changed")
		}
		device = current
		w.Write([]byte(`{"success":1,"data":{"movies":[]}}`))
	}))
	defer server.Close()
	client, _ := javdbapp.NewClient(server.Client(), server.URL, "")
	s := &javdbResourceSearcher{client: client, load: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }}
	for i := 0; i < 2; i++ {
		items, err := s.Search(context.Background(), "TEST-001")
		if err != nil || len(items) != 0 {
			t.Fatalf("%+v %v", items, err)
		}
	}
	if requests != 2 || s.client != client {
		t.Fatal("client not reused")
	}
	s.load = func(context.Context) (map[string]string, error) { return map[string]string{"PROXY": "invalid"}, nil }
	if _, err := s.Search(context.Background(), "TEST-001"); err == nil {
		t.Fatal("invalid proxy accepted")
	}
}
