package collector

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
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
