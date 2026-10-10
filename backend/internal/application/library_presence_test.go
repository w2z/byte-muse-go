package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLibraryPresencePan115 区分正常空页与网络错误，多个来源任意存在即可满足。
func TestLibraryPresencePan115(t *testing.T) {
	pan := &libraryScanPan115Stub{files: map[string][]domain.Pan115File{"parent": {{ID: "file", Name: "TEST-001.mp4"}}}}
	checker := NewLibraryPresenceService(pan, nil, nil, nil)
	sources := []ports.LibrarySource{{Kind: "115", Location: "parent", ItemID: "file"}}
	found, err := checker.Check(context.Background(), "TEST-001", sources)
	if err != nil || found == nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
	pan.files["parent"] = nil
	found, err = checker.Check(context.Background(), "TEST-001", sources)
	if err != nil || found != nil {
		t.Fatalf("absent=%v err=%v", found, err)
	}
	pan.failure = map[string]error{"parent": errors.New("offline")}
	if _, err = checker.Check(context.Background(), "TEST-001", sources); err == nil {
		t.Fatal("offline must remain unknown")
	}
	root := t.TempDir()
	file := filepath.Join(root, "TEST-001.mp4")
	if err = os.WriteFile(file, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	sources = append(sources, ports.LibrarySource{Kind: "local", Location: file})
	found, err = checker.Check(context.Background(), "TEST-001", sources)
	if err != nil || found == nil || found.Kind != "local" {
		t.Fatalf("multiple sources=%v err=%v", found, err)
	}
}

// TestLibraryPresenceSources 检查真实文件、服务器结果、错误与番号误匹配。
func TestLibraryPresenceSources(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "TEST-001.mp4")
	if err := os.WriteFile(file, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	checker := NewLibraryPresenceService(nil, nil, map[string]string{}, nil)
	found, err := checker.Check(context.Background(), "TEST-001", []ports.LibrarySource{{Kind: "local", Location: file}})
	if err != nil || found == nil {
		t.Fatalf("local presence=%v err=%v", found, err)
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	found, err = checker.Check(context.Background(), "TEST-001", []ports.LibrarySource{{Kind: "local", Location: file}})
	if err != nil || found != nil {
		t.Fatalf("removed presence=%v err=%v", found, err)
	}
	for _, kind := range []string{"emby", "jellyfin", "plex"} {
		t.Run(kind, func(t *testing.T) {
			status, code := 200, "TEST-001"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status != 200 {
					w.WriteHeader(status)
					return
				}
				if kind == "plex" {
					fmt.Fprintf(w, "<MediaContainer size='1'><Video ratingKey='1' title='%s'><Media><Part file='/video/%s.mp4'/></Media></Video></MediaContainer>", code, code)
				} else {
					fmt.Fprintf(w, `{"Items":[{"Id":"1","Name":"%s","Path":"/video/%s.mp4"}],"TotalRecordCount":1}`, code, code)
				}
			}))
			defer server.Close()
			settings := map[string]string{strings.ToUpper(kind) + "_URL": server.URL, strings.ToUpper(kind) + "_API_KEY": "test", "PLEX_TOKEN": "test"}
			checker := NewLibraryPresenceService(nil, nil, settings, server.Client())
			found, err := checker.Check(context.Background(), "TEST-001", nil)
			if err != nil || found == nil || found.Kind != kind {
				t.Fatalf("server presence=%v err=%v", found, err)
			}
			code = "TEST-0012"
			found, err = checker.Check(context.Background(), "TEST-001", nil)
			if found != nil {
				t.Fatal("partial code must not match")
			}
			status = 503
			if _, err = checker.Check(context.Background(), "TEST-001", nil); err == nil {
				t.Fatal("unavailable server must remain unknown")
			}
		})
	}
}

// TestLibraryPresenceServerBoundary 不完整响应保持未知，已记录 ID 不受标题搜索影响。
func TestLibraryPresenceServerBoundary(t *testing.T) {
	for _, body := range []string{"{}", "<wrong/>"} {
		t.Run(body, func(t *testing.T) {
			kind := "emby"
			if body[0] == '<' {
				kind = "plex"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			checker := NewLibraryPresenceService(nil, nil, map[string]string{strings.ToUpper(kind) + "_URL": server.URL, strings.ToUpper(kind) + "_API_KEY": "test", "PLEX_TOKEN": "test"}, server.Client())
			if _, err := checker.Check(context.Background(), "TEST-001", []ports.LibrarySource{{Kind: kind, Location: server.URL, ItemID: "1"}}); err == nil {
				t.Fatal("invalid response must be unknown")
			}
		})
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("Ids") == "1" {
			fmt.Fprint(w, `{"Items":[{"Id":"1","Name":"renamed","Path":"/video/TEST-001.mp4"}],"TotalRecordCount":1}`)
			return
		}
		if r.URL.Query().Get("StartIndex") == "0" {
			fmt.Fprint(w, `{"Items":[{"Id":"2","Path":"/video/OTHER-001.mp4"}],"TotalRecordCount":2}`)
		} else {
			fmt.Fprint(w, `{"Items":[{"Id":"1","Path":"/video/TEST-001.mp4"}],"TotalRecordCount":2}`)
		}
	}))
	defer server.Close()
	checker := NewLibraryPresenceService(nil, nil, map[string]string{"EMBY_URL": server.URL, "EMBY_API_KEY": "test"}, server.Client())
	if found, err := checker.Check(context.Background(), "TEST-001", nil); err != nil || found == nil || calls != 2 {
		t.Fatalf("pagination found=%v err=%v calls=%d", found, err, calls)
	}
	if found, err := checker.Check(context.Background(), "TEST-001", []ports.LibrarySource{{Kind: "emby", Location: server.URL, ItemID: "1"}}); err != nil || found == nil {
		t.Fatalf("id found=%v err=%v", found, err)
	}
}
