package javdbapp

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

// TestLiveActors 仅显式启用时读取源站演员，不写数据库，不提交下载。
func TestLiveActors(t *testing.T) {
	if os.Getenv("BYTEMUSE_LIVE_JAVDB") != "1" {
		t.Skip("需要显式启用实网验证")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if raw := os.Getenv("BYTEMUSE_COLLECTION_PROXY"); raw != "" {
		proxy, err := url.Parse(raw)
		if err != nil {
			t.Fatal("代理格式无效")
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	defer transport.CloseIdleConnections()
	for _, host := range []string{"https://jdforrepam.com", "https://apidd.spthgb.com", "https://apidd.czssdgz.com", "https://javdb.com"} {
		t.Run(host, func(t *testing.T) {
			client, err := NewClient(&http.Client{Transport: transport, Timeout: 15 * time.Second}, host, "")
			if err != nil {
				t.Fatal(err)
			}
			actors, err := client.MovieActors(context.Background(), "DRJeGM")
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("actors=%d", len(actors))
			movie, err := client.Detail(context.Background(), "DRJeGM")
			if err != nil {
				t.Fatal(err)
			}
			if len(movie.Actors) == 0 || movie.Actors[0].ID == "" {
				t.Fatal("actor identity missing")
			}
			works, err := client.ActorMovies(context.Background(), movie.Actors[0].ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("actor_works=%d", len(works))
			resources, err := client.Search(context.Background(), movie.Number)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("bt_resources=%d", len(resources))
		})
	}
}
