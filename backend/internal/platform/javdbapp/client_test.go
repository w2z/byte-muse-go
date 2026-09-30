package javdbapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientMovieActorsUsesAppEnvelopeAndSignature(t *testing.T) {
	var gotPath, gotSignature string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSignature = r.Header.Get("jdsignature")
		if r.URL.Query().Get("app_version_number") != "10928" || r.URL.Query().Get("device_uuid") == "" {
			t.Fatalf("missing app query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"movie":{"actors":[{"id":"a1","name":"Actor","name_zht":"演員","avatar_url":"https://img.example/a.jpg"}]}}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.Client(), server.URL, "device-test")
	if err != nil {
		t.Fatal(err)
	}
	actors, err := client.MovieActors(context.Background(), "movie-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v4/movies/movie-1" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotSignature, ".lpw6vgqzsp.") {
		t.Fatalf("signature = %q", gotSignature)
	}
	if len(actors) != 1 || actors[0].Name != "Actor" || actors[0].NameZHT != "演員" || actors[0].AvatarURL == "" {
		t.Fatalf("actors = %#v", actors)
	}
}

func TestClientMovieActorsRejectsMalformedEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"data":{}}`))
	}))
	defer server.Close()
	client, err := NewClient(server.Client(), server.URL, "device-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.MovieActors(context.Background(), "movie-1"); err == nil {
		t.Fatal("expected malformed envelope error")
	}
}

func TestClientDoesNotFollowRedirectOrMutateCaller(t *testing.T) {
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"success":true,"data":{"movie":{"actors":[]}}}`))
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	original := redirect.Client()
	client, err := NewClient(original, redirect.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	if original.Timeout != 0 {
		t.Fatal("caller client mutated")
	}
	if _, err = client.MovieActors(context.Background(), "m1"); err == nil {
		t.Fatal("redirect accepted")
	}
	if hits != 0 {
		t.Fatal("redirect reached another origin")
	}
}

func TestAutomaticRouteFailoverDoesNotRetryRateLimit(t *testing.T) {
	status := 503
	first, second := 0, 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { first++; w.WriteHeader(status) }))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		second++
		w.Write([]byte(`{"success":1,"data":{"movie":{"actors":[]}}}`))
	}))
	defer good.Close()
	c, _ := NewClient(bad.Client(), bad.URL, "device")
	c.routes = []string{bad.URL, good.URL}
	if _, err := c.MovieActors(context.Background(), "m1"); err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 1 {
		t.Fatalf("attempts=%d,%d", first, second)
	}
	if _, err := c.MovieActors(context.Background(), "m1"); err != nil {
		t.Fatal(err)
	}
	if first != 1 {
		t.Fatal("healthy route not retained")
	}
	c.host = bad.URL
	status = 429
	if _, err := c.MovieActors(context.Background(), "m1"); err == nil {
		t.Fatal("rate limit accepted")
	}
	if second != 2 {
		t.Fatal("429 retried on another route")
	}
	before := first
	if _, err := c.MovieActors(context.Background(), "m1"); err == nil {
		t.Fatal("cooldown not enforced")
	}
	if first != before {
		t.Fatal("cooldown sent another request")
	}
}
