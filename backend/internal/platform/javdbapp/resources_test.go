package javdbapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchMagnetsRequiresExactUniqueMovieAndBTKind(t *testing.T) {
	ambiguous := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/search":
			if ambiguous {
				w.Write([]byte(`{"success":1,"data":{"movies":[{"id":"m1","number":"TEST-001"},{"id":"m2","number":"TEST-001"}]}}`))
				return
			}
			w.Write([]byte(`{"success":1,"data":{"movies":[{"id":"wrong","number":"TEST-0010"},{"id":"m1","number":"TEST-001"}]}}`))
		case "/api/v1/movies/m1/magnets":
			w.Write([]byte(`{"success":1,"data":{"magnets":[{"hash":"0123456789abcdef0123456789abcdef01234567","name":"测试资源","size":1024,"cnsub":true},{"hash":"invalid","name":"错误"}]}}`))
		default:
			t.Errorf("wrong movie request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, _ := NewClient(server.Client(), server.URL, "device")
	items, err := c.Search(context.Background(), "test-001")
	if err != nil || len(items) != 1 {
		t.Fatalf("%+v %v", items, err)
	}
	if items[0].Kind != "bt" || items[0].Site != "JavDB" || !items[0].Chinese || items[0].SizeMB != 1024 || items[0].URI != "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("%+v", items[0])
	}
	ambiguous = true
	if _, err = c.Search(context.Background(), "TEST-001"); err == nil {
		t.Fatal("ambiguous movie accepted")
	}
}
