package bootstrap

import (
	"bytemuse/backend/internal/platform/javdbapp"
	"bytemuse/backend/internal/platform/torrentsearch"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// javdbResourceSearcher 在进程中复用线路和设备身份，代理设置改变时重建传输。
// 同一实例供订阅下载与 Agent 搜索复用；它只读取磁力，不提交下载。
type javdbResourceSearcher struct {
	mu        sync.Mutex
	load      func(context.Context) (map[string]string, error)
	client    *javdbapp.Client
	transport *http.Transport
	proxy     string
}

func (s *javdbResourceSearcher) Search(ctx context.Context, code string) ([]torrentsearch.Resource, error) {
	values, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(values["PROXY"])
	s.mu.Lock()
	if s.client == nil || s.proxy != raw {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		if raw != "" {
			proxy, e := url.Parse(raw)
			if e != nil || proxy.Host == "" || (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") {
				s.mu.Unlock()
				return nil, errors.New("JavDB 代理配置无效")
			}
			transport.Proxy = http.ProxyURL(proxy)
		}
		client, e := javdbapp.NewAutomatic(&http.Client{Transport: transport, Timeout: 20 * time.Second})
		if e != nil {
			s.mu.Unlock()
			return nil, e
		}
		if s.transport != nil {
			s.transport.CloseIdleConnections()
		}
		s.client = client
		s.transport = transport
		s.proxy = raw
	}
	client := s.client
	s.mu.Unlock()
	return client.Search(ctx, code)
}
