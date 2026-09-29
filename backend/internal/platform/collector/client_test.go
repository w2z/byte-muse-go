package collector

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFetchNeverFollowsCrossOriginRedirect(t *testing.T) {
	calls := 0
	c := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})})
	if _, e := c.Get(context.Background(), "https://javdb.com/search"); e == nil {
		t.Fatal("redirect accepted")
	}
	if calls != 1 {
		t.Fatalf("cross-origin requests=%d", calls)
	}
}

func TestFetchRateLimitAndCancellationAreExplicit(t *testing.T) {
	c := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})})
	if _, e := c.Get(context.Background(), "https://javdb.com/search"); !errors.Is(e, ErrRateLimited) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.Get(ctx, "https://javdb.com/search"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRejectsChallengeEvenWithHTTP200(t *testing.T) {
	c := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`<title>Just a moment...</title><div id="cf-chl">challenge</div>`)), Header: make(http.Header), Request: r}, nil
	})})
	_, err := c.Get(context.Background(), "https://javdb.com/")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected blocked, got %v", err)
	}
}

func TestFetchUsesFlareSolverrAfterCloudflareChallenge(t *testing.T) {
	calls := 0
	client := NewClientWithBypass(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "javdb.com" {
			return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("<title>Just a moment</title>")), Header: make(http.Header), Request: r}, nil
		}
		if r.URL.Path != "/v1" || r.Method != http.MethodPost {
			t.Fatalf("unexpected bypass request: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"ok","solution":{"url":"https://javdb.com/","status":200,"response":"<html>valid</html>"}}`)), Header: make(http.Header), Request: r}, nil
	}), Timeout: 2 * time.Second}, "flaresolverr", "https://bypass.test")
	client.bypass.http.Transport = client.http.Transport
	body, err := client.Get(context.Background(), "https://javdb.com/")
	if err != nil || string(body) != "<html>valid</html>" || calls != 2 {
		t.Fatalf("body=%q err=%v calls=%d", body, err, calls)
	}
}

func TestFetchReportsCookieRequiredSeparately(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("login required")), Header: make(http.Header), Request: r}, nil
	})})
	if _, err := client.Get(context.Background(), "https://javdb.com/"); !errors.Is(err, ErrCookieRequired) {
		t.Fatalf("expected cookie required, got %v", err)
	}
}

func TestFetchRejectsUnapprovedOriginBeforeTransport(t *testing.T) {
	calls := 0
	c := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected request") })})
	for _, u := range []string{"http://127.0.0.1/", "https://javdb.com.evil.test/", "https://javdb.com:443/", "https://user:pass@javdb.com/"} {
		if _, err := c.Get(context.Background(), u); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s: %v", u, err)
		}
	}
	if calls != 0 {
		t.Fatalf("transport called %d times", calls)
	}
}

func TestFetchRedactsTransportSecrets(t *testing.T) {
	c := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, errors.New("proxy password secret-token") })})
	_, err := c.Get(context.Background(), "https://javdb.com/")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe error: %v", err)
	}
}

// TestMetadataOrigins 限定新增来源为精确 HTTPS 域名，不接受 ThisAV 或任意子域。
func TestMetadataOrigins(t *testing.T) {
	for _, host := range []string{"www.javlibrary.com", "jable.tv", "supjav.com"} {
		c := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), Request: r}, nil
		})})
		if _, err := c.Get(context.Background(), "https://"+host+"/"); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
	c := NewClient(nil)
	for _, raw := range []string{"https://thisav.com/", "https://supjav.com.evil.test/", "https://jable.tv:443/", "https://user@www.javlibrary.com/"} {
		if _, err := c.Get(context.Background(), raw); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("unexpected request %s: %v", raw, err)
		}
	}
}
