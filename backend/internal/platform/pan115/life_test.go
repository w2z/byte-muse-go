package pan115

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLifeEventsProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/behavior/detail" || r.URL.Query().Get("offset") != "64" || r.Header.Get("Cookie") != "UID=123_test; CID=test; SEID=test" || r.Header.Get("Authorization") != "" {
			t.Errorf("incorrect event request")
		}
		fmt.Fprint(w, `{"state":true,"data":{"count":"65","list":[{"id":"9007199254740993","type":"2","update_time":1700000000,"file_id":"42","parent_id":3}]}}`)
	}))
	defer server.Close()
	client := New(server.Client())
	client.life = server.URL
	page, err := client.LifeEvents(context.Background(), "UID=123_test; CID=test; SEID=test", 64, 64)
	if err != nil || page.Total != 65 || len(page.Events) != 1 || page.Events[0].ID != 9007199254740993 || page.Events[0].Type != 2 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestLifeEventsRejectInvalidResponse(t *testing.T) {
	for _, body := range []string{`{"state":false,"error":"do not expose me"}`, `{"state":true}`, `{"state":true,"data":{"count":1,"list":[{"id":"bad","type":2,"update_time":1}]}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		client := New(server.Client())
		client.life = server.URL
		_, err := client.LifeEvents(context.Background(), "UID=123_test; CID=test; SEID=test", 0, 64)
		server.Close()
		if err == nil {
			t.Fatal("invalid response accepted")
		}
	}
}

func TestLifeEventsDoesNotFollowRedirect(t *testing.T) {
	called := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer origin.Close()
	client := New(origin.Client())
	client.life = origin.URL
	if _, err := client.LifeEvents(context.Background(), "UID=123_test; CID=test; SEID=test", 0, 64); err == nil {
		t.Fatal("redirect accepted")
	}
	if called {
		t.Fatal("cookie followed redirect")
	}
}
