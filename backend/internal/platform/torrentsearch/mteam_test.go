package torrentsearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMTeamSearchUsesKeyAndParsesAdultResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/torrent/search" || r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("request path or API key invalid")
		}
		body := make([]byte, 512)
		n, _ := r.Body.Read(body)
		if !strings.Contains(string(body[:n]), "SSIS-001") || !strings.Contains(string(body[:n]), "adult") {
			t.Errorf("search body=%s", string(body[:n]))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"code\":0,\"data\":{\"data\":[{\"id\":\"123\",\"name\":\"SSIS-001 中文字幕\",\"size\":\"2147483648\",\"seeders\":\"15\",\"discount\":\"FREE\"},{\"id\":\"124\",\"name\":\"SSIS-0011 wrong\"}]}}"))
	}))
	defer server.Close()
	result, e := NewMTeamSearcher(server.Client(), server.URL, "test-key").Search(context.Background(), "SSIS-001")
	if e != nil {
		t.Fatal(e)
	}
	if len(result) != 1 || result[0].Kind != "pt" || result[0].URI != "mteam:123" || result[0].SizeMB != 2048 || result[0].Seeders != 15 || !result[0].Free {
		t.Fatalf("result=%#v", result)
	}
}

func TestMTeamSearchRejectsMissingCredentialAndBadResponse(t *testing.T) {
	s := NewMTeamSearcher(nil, "", " ")
	if _, e := s.Search(context.Background(), "SSIS-001"); e == nil {
		t.Fatal("missing key accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"code\":401,\"message\":\"bad key\",\"data\":null}"))
	}))
	defer server.Close()
	if _, e := NewMTeamSearcher(server.Client(), server.URL, "test-key").Search(context.Background(), "SSIS-001"); e == nil {
		t.Fatal("API failure treated as empty results")
	}
}

func TestMTeamDownloadFetchesTorrentAndComputesInfoHash(t *testing.T) {
	torrent := []byte("d8:announce14:http://tracker4:infod4:name4:testee")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/torrent/genDlToken" {
			if r.URL.Query().Get("id") != "123" || r.Header.Get("x-api-key") != "test-key" {
				t.Error("incorrect private token request")
			}
			_, _ = w.Write([]byte("{\"code\":0,\"data\":\"" + server.URL + "/download/123.torrent\"}"))
			return
		}
		if r.URL.Path == "/download/123.torrent" {
			_, _ = w.Write(torrent)
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	defer server.Close()
	client := NewMTeamSearcher(server.Client(), server.URL, "test-key")
	got, hash, downloadURL, e := client.Download(context.Background(), "mteam:123")
	if e != nil {
		t.Fatal(e)
	}
	if downloadURL != server.URL+"/download/123.torrent" {
		t.Fatal("actual download URL missing")
	}
	if string(got) != string(torrent) || hash != "1ade8a1a581f338e4fce4ce784da3f7d03f81f3a" {
		t.Fatalf("torrent hash=%s", hash)
	}
}
