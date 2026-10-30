package torrentsearch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type retryRoundTripFunc func(*http.Request) (*http.Response, error)

func (f retryRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type temporaryRetryError struct{}

func (temporaryRetryError) Error() string   { return "temporary unavailable" }
func (temporaryRetryError) Timeout() bool   { return true }
func (temporaryRetryError) Temporary() bool { return true }

func TestRetryTransportUsesProxyAfterThreeDirectFailures(t *testing.T) {
	directCalls, proxyCalls := 0, 0
	transport := &retryTransport{
		direct: retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
			directCalls++
			return nil, temporaryRetryError{}
		}),
		proxy: retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
			proxyCalls++
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
		}),
	}

	response, err := transport.RoundTrip(mustRetryRequest(t))
	if err != nil || response == nil || response.StatusCode != http.StatusOK {
		t.Fatalf("response=%v err=%v", response, err)
	}
	_ = response.Body.Close()
	if directCalls != 3 || proxyCalls != 1 {
		t.Fatalf("direct=%d proxy=%d, want direct=3 proxy=1", directCalls, proxyCalls)
	}
}

func TestRetryTransportSkipsProxyForNonRetryableResponse(t *testing.T) {
	directCalls, proxyCalls := 0, 0
	transport := &retryTransport{
		direct: retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
			directCalls++
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: http.NoBody, Header: make(http.Header)}, nil
		}),
		proxy: retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
			proxyCalls++
			return nil, errors.New("proxy must not be called")
		}),
	}

	response, err := transport.RoundTrip(mustRetryRequest(t))
	if err != nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("response=%v err=%v", response, err)
	}
	_ = response.Body.Close()
	if directCalls != 1 || proxyCalls != 0 {
		t.Fatalf("direct=%d proxy=%d, want direct=1 proxy=0", directCalls, proxyCalls)
	}
}

func TestRetryTransportStopsAfterThreeProxyFailures(t *testing.T) {
	directCalls, proxyCalls := 0, 0
	transport := &retryTransport{
		direct: retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
			directCalls++
			return nil, temporaryRetryError{}
		}),
		proxy: retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
			proxyCalls++
			return nil, temporaryRetryError{}
		}),
	}

	if _, err := transport.RoundTrip(mustRetryRequest(t)); err == nil {
		t.Fatal("expected final proxy error")
	}
	if directCalls != 3 || proxyCalls != 3 {
		t.Fatalf("direct=%d proxy=%d, want direct=3 proxy=3", directCalls, proxyCalls)
	}
}

func TestRetryTransportRetriesServerErrorsAndKeepsRequestBody(t *testing.T) {
	calls := 0
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test/resource", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	transport := &retryTransport{direct: retryRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil || string(body) != "payload" {
			t.Fatalf("body=%q err=%v", body, readErr)
		}
		if calls < 3 {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody, Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
	})}
	response, err := transport.RoundTrip(request)
	if err != nil || response == nil || calls != 3 {
		t.Fatalf("response=%v err=%v calls=%d", response, err, calls)
	}
	_ = response.Body.Close()
}

func TestRetryTransportDoesNotRetryDeterministicError(t *testing.T) {
	calls := 0
	transport := &retryTransport{direct: retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("invalid TLS configuration")
	})}
	if _, err := transport.RoundTrip(mustRetryRequest(t)); err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestRetryTransportDoesNotRetryRateLimitResponse(t *testing.T) {
	calls := 0
	transport := &retryTransport{direct: retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusTooManyRequests, Body: http.NoBody, Header: make(http.Header)}, nil
	}), proxy: retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("rate limit must not switch to proxy")
		return nil, errors.New("unreachable")
	})}
	response, err := transport.RoundTrip(mustRetryRequest(t))
	if err != nil || response == nil || response.StatusCode != http.StatusTooManyRequests || calls != 1 {
		t.Fatalf("response=%v err=%v calls=%d", response, err, calls)
	}
	_ = response.Body.Close()
}

func TestNewPTSearchClientValidatesProxyAndDisablesEnvironmentProxy(t *testing.T) {
	if _, err := NewPTSearchClient(nil, "file:///tmp/proxy"); err == nil {
		t.Fatal("expected invalid proxy error")
	}
	client, err := NewPTSearchClient(nil, "http://proxy.example:7890")
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.Transport.(*retryTransport)
	if !ok {
		t.Fatalf("transport=%T", client.Transport)
	}
	direct, ok := transport.direct.(*http.Transport)
	if !ok || direct.Proxy != nil {
		t.Fatalf("direct transport=%T proxy configured=%t", transport.direct, direct.Proxy != nil)
	}
}

func TestRetryTransportStopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	transport := &retryTransport{direct: retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		cancel()
		return nil, temporaryRetryError{}
	})}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.test/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(request); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestRetryTransportRequiresReplayableBodyBeforeProxyFallback(t *testing.T) {
	calls := 0
	transport := &retryTransport{direct: retryRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, temporaryRetryError{}
	}), proxy: retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("non-replayable body must not reach proxy")
		return nil, errors.New("unreachable")
	})}
	request := mustRetryRequest(t)
	request.Body = io.NopCloser(strings.NewReader("payload"))
	request.GetBody = nil
	if _, err := transport.RoundTrip(request); err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func mustRetryRequest(t *testing.T) *http.Request {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
