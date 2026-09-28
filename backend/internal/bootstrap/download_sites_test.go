package bootstrap

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type siteTransport func(*http.Request) (*http.Response, error)

func (f siteTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// 验证每批设置选择真正控制站点请求，不把隐藏 Cookie 当密钥模式的回退。
func TestConfiguredDownloadSites(t *testing.T) {
	for _, tc := range []struct{ prefix, host, path, ref string }{
		{"PTFANS", "ptfans.cc", "/api/v1/torrents/cas", "ptfans:12"},
		{"NICEPT", "www.nicept.net", "/api/v1/torrents", "nicept:12"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: siteTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != tc.host || r.URL.Path != tc.path || r.Header.Get("Authorization") != "Bearer fake-key" || r.Header.Get("Cookie") != "" {
					t.Errorf("incorrect configured request host=%s path=%s", r.URL.Host, r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ret":0,"data":{"data":[{"id":12,"name":"TEST-123","size":1048576,"seeders":1}],"meta":{"current_page":1,"last_page":1}}}`))}, nil
			})}
			values := map[string]string{tc.prefix + "_API_KEY": "fake-key", tc.prefix + "_COOKIE": "old-cookie"}
			searchers, private := configuredPrivateSites(values, client)
			if len(searchers) != 1 || len(private) != 1 {
				t.Fatalf("sources=%d private=%d", len(searchers), len(private))
			}
			items, err := searchers[0].Search(context.Background(), "TEST-123")
			if err != nil || len(items) != 1 || items[0].URI != tc.ref || calls != 1 {
				t.Fatalf("items=%v err=%v calls=%d", items, err, calls)
			}
			delete(values, tc.prefix+"_API_KEY")
			searchers, private = configuredPrivateSites(values, client)
			if len(searchers) != 0 || len(private) != 0 {
				t.Fatal("key mode fell back to hidden cookie")
			}
		})
	}
}

func TestPTTimeAlwaysUsesCookie(t *testing.T) {
	values := map[string]string{"PTT_AUTH_TYPE": "key", "PTT_PASSKEY": "legacy-unused-key", "PTT_UID": "12", "PTT_COOKIE": "session=fake"}
	calls := 0
	client := &http.Client{Transport: siteTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "www.pttime.org" || r.URL.Path != "/download.php" || r.URL.Query().Get("passkey") != "" || r.Header.Get("Cookie") != "session=fake" {
			t.Error("PTTime did not use cookie-only adapter")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("d8:announce14:http://tracker4:infod4:name4:testee"))}, nil
	})}
	sources, private := configuredPrivateSites(values, client)
	if len(sources) != 1 || private["pttime"] == nil {
		t.Fatal("PTTime missing")
	}
	_, hash, err := private["pttime"].Download(context.Background(), "pttime:12")
	if err != nil || hash == "" || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestRousiProSelectedCredentials(t *testing.T) {
	for _, mode := range []string{"key", "cookie"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: siteTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body := `{"items":[],"total":0}`
				if mode == "key" {
					if r.Header.Get("Authorization") != "Bearer fake-key" || r.Header.Get("Cookie") != "" {
						t.Error("wrong key auth")
					}
					body = `{"code":0,"data":{"page":1,"page_size":100,"total":0,"total_pages":0,"torrents":[]}}`
				} else if r.Header.Get("Cookie") != "session=fake" || r.Header.Get("Authorization") != "" {
					t.Error("wrong cookie auth")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			sources, private := configuredPrivateSites(map[string]string{"ROUSIPRO_AUTH_TYPE": mode, "ROUSIPRO_API_KEY": "fake-key", "ROUSIPRO_COOKIE": "session=fake"}, client)
			if len(sources) != 1 || private["rousipro"] == nil {
				t.Fatal("missing RousiPro adapter")
			}
			items, err := sources[0].Search(context.Background(), "TEST-123")
			if err != nil || len(items) != 0 || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}
