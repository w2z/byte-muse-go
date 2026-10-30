package torrentsearch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const (
	searchRetryAttempts   = 3
	searchAttemptTimeout  = 20 * time.Second
	maxRetryResponseBytes = 8 << 20
)

// retryTransport 为 PT 搜索提供“直连三次后代理三次”的请求级回退。
// 它只处理传输错误和 5xx；业务认证、参数和响应结构错误交给站点适配器判断。
// 请求体必须可通过 GetBody 重放，否则失败请求不会被强行重复提交。
type retryTransport struct {
	direct http.RoundTripper
	proxy  http.RoundTripper
}

// NewPTSearchClient 创建只用于 PT 搜索的直连/代理回退客户端。
// 首轮显式关闭环境代理；proxy 为空时只执行直连三次。客户端总超时按六次尝试预留，
// 避免原有单次 20 秒超时在直连失败后截断代理回退。非法代理返回错误且不泄露代理内容。
func NewPTSearchClient(client *http.Client, rawProxy string) (*http.Client, error) {
	directClient, directTransport, err := cloneSearchClient(client, false, nil)
	if err != nil {
		return nil, err
	}

	rawProxy = strings.TrimSpace(rawProxy)
	var proxyTransport http.RoundTripper
	if rawProxy != "" {
		proxyURL, err := url.Parse(rawProxy)
		if err != nil || proxyURL.Host == "" || !supportedProxyScheme(proxyURL.Scheme) {
			return nil, errors.New("PT 搜索代理配置无效")
		}
		_, proxyTransport, err = cloneSearchClient(client, true, proxyURL)
		if err != nil {
			return nil, err
		}
	}

	result := *directClient
	result.Transport = &retryTransport{direct: directTransport, proxy: proxyTransport}
	if result.Timeout == 0 {
		result.Timeout = searchAttemptTimeout * (searchRetryAttempts * 2)
	} else {
		result.Timeout *= searchRetryAttempts * 2
	}
	return &result, nil
}

func cloneSearchClient(client *http.Client, useProxy bool, proxyURL *url.URL) (*http.Client, http.RoundTripper, error) {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	transport := clone.Transport
	if transport == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, nil, errors.New("PT 搜索默认传输配置无效")
		}
		transport = base.Clone()
	}
	if base, ok := transport.(*http.Transport); ok {
		base = base.Clone()
		if !useProxy {
			base.Proxy = nil
		} else {
			base.Proxy = http.ProxyURL(proxyURL)
		}
		transport = base
	}
	clone.Transport = transport
	return &clone, transport, nil
}

func supportedProxyScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "http", "https", "socks5", "socks5h":
		return true
	default:
		return false
	}
}

func (t *retryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, errors.New("PT 搜索请求为空")
	}
	var lastErr error
	for _, transport := range []http.RoundTripper{t.direct, t.proxy} {
		if transport == nil {
			continue
		}
		for attempt := 0; attempt < searchRetryAttempts; attempt++ {
			if err := request.Context().Err(); err != nil {
				return nil, err
			}
			if attempt > 0 || lastErr != nil {
				if err := resetRequestBody(request); err != nil {
					return nil, lastErrOr(err, lastErr)
				}
			}
			attemptCtx, cancel := context.WithTimeout(request.Context(), searchAttemptTimeout)
			attemptRequest := request.Clone(attemptCtx)
			retryableResponse := false
			response, err := transport.RoundTrip(attemptRequest)
			if err == nil && response != nil {
				if retryableStatus(response.StatusCode) {
					status := response.StatusCode
					_ = response.Body.Close()
					response = nil
					err = fmt.Errorf("PT 搜索 HTTP %d", status)
					retryableResponse = true
				} else {
					response, err = bufferResponse(response)
					if err == nil {
						cancel()
						return response, nil
					}
				}
			}
			cancel()
			if err == nil && response == nil {
				err = errors.New("PT 搜索传输未返回响应")
			}
			lastErr = err
			if err != nil && !retryableResponse && !retryableAttemptError(err, attemptCtx) {
				return nil, err
			}
			if err := request.Context().Err(); err != nil {
				return nil, err
			}
		}
	}
	if lastErr == nil {
		return nil, errors.New("PT 搜索传输失败")
	}
	return nil, lastErr
}

func bufferResponse(response *http.Response) (*http.Response, error) {
	if response.Body == nil {
		response.Body = http.NoBody
		response.ContentLength = 0
		return response, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRetryResponseBytes+1))
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(body) > maxRetryResponseBytes {
		return nil, errors.New("PT 搜索响应过大")
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	return response, nil
}

func resetRequestBody(request *http.Request) error {
	if request.Body == nil {
		return nil
	}
	if request.GetBody == nil {
		return errors.New("PT 搜索请求体不可重放")
	}
	_ = request.Body.Close()
	body, err := request.GetBody()
	if err != nil {
		return fmt.Errorf("重建 PT 搜索请求体失败: %w", err)
	}
	request.Body = body
	return nil
}

func retryableStatus(status int) bool {
	return status >= http.StatusInternalServerError && status <= 599
}

func retryableTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ETIMEDOUT)
}

func retryableAttemptError(err error, attemptCtx context.Context) bool {
	if retryableTransportError(err) {
		return true
	}
	return errors.Is(attemptCtx.Err(), context.DeadlineExceeded)
}

func lastErrOr(current, previous error) error {
	if previous != nil {
		return previous
	}
	return current
}
