package javdbapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetailAndActorWorksContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/movies/m1":
			w.Write([]byte(`{"success":1,"data":{"movie":{"id":"m1","number":"TEST-001","title":"测试影片","actors":[{"id":"actor1","name":"演员甲","avatar_url":"https://img.example/a.jpg"}]}}}`))
		case "/api/v1/movies/tags":
			if r.URL.Query().Get("filter_by") != ":a:actor1" || r.URL.Query().Get("page") != "2" {
				t.Errorf("wrong actor query: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"success":true,"data":{"movies":[{"id":"m1","number":"TEST-001","title":"测试影片"}]}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, err := NewClient(server.Client(), server.URL, "device")
	if err != nil {
		t.Fatal(err)
	}
	movie, err := c.Detail(context.Background(), "m1")
	if err != nil || movie.Number != "TEST-001" || len(movie.Actors) != 1 || movie.Actors[0].ID != "actor1" {
		t.Fatalf("%+v %v", movie, err)
	}
	movies, err := c.ActorMovies(context.Background(), "actor1", 2)
	if err != nil || len(movies) != 1 || movies[0].ID != "m1" {
		t.Fatalf("%+v %v", movies, err)
	}
	for _, id := range []string{"", "..", "a:b", "a/b", "a?b"} {
		if _, err = c.ActorMovies(context.Background(), id, 1); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
}

func TestDetailRejectsDifferentMovie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":1,"data":{"movie":{"id":"wrong","number":"TEST-001","title":"测试影片"}}}`))
	}))
	defer server.Close()
	c, _ := NewClient(server.Client(), server.URL, "device")
	if _, err := c.Detail(context.Background(), "m1"); err == nil {
		t.Fatal("different movie accepted")
	}
}
