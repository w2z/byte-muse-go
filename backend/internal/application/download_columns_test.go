package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"errors"
	"fmt"
	"testing"
)

type columnRepository struct {
	items []domain.DownloadTask
	all   bool
}

func (r *columnRepository) List(_ context.Context, q ports.DownloadListQuery) (domain.DownloadPage, error) {
	r.all = q.All
	return domain.DownloadPage{Items: r.items, Total: len(r.items)}, nil
}

type columnClient struct {
	metrics map[string]*domain.DownloadMetrics
	count   int
	fail    bool
}

func (c *columnClient) Capabilities(context.Context) ([]string, error) { return nil, nil }
func (c *columnClient) Observe(context.Context, string) (*ports.TransferState, error) {
	return nil, nil
}
func (c *columnClient) Control(context.Context, string, string) error { return nil }
func (c *columnClient) ReadMetrics(_ context.Context, hashes []string) (map[string]*domain.DownloadMetrics, error) {
	c.count = len(hashes)
	if c.fail {
		return nil, errors.New("offline")
	}
	return c.metrics, nil
}

// TestDownloadColumnsGlobalSnapshot proves metric sorting sees records beyond the first page and preserves nulls.
func TestDownloadColumnsGlobalSnapshot(t *testing.T) {
	r := &columnRepository{}
	c := &columnClient{metrics: map[string]*domain.DownloadMetrics{}}
	name := "qbittorrent"
	for i := 0; i < 30; i++ {
		hash := fmt.Sprint(i)
		code := fmt.Sprintf("TEST-%02d", i)
		site := "site"
		n := int64(i) * 1024
		r.items = append(r.items, domain.DownloadTask{ID: hash, Code: &code, SourceSite: &site, Downloader: &name, InfoHash: &hash})
		c.metrics[hash] = &domain.DownloadMetrics{SizeBytes: &n}
	}
	r.items = append(r.items, domain.DownloadTask{ID: "missing"})
	s := NewDownloadService(r)
	s.clients = func(context.Context) (map[string]ports.DownloadController, error) {
		return map[string]ports.DownloadController{name: c}, nil
	}
	q := ports.DownloadListQuery{SortBy: "size_bytes", SortOrder: "desc", ColumnFilters: map[string][]string{"source_site": {"SITE"}, "size_bytes": {"10240", ""}}}
	page, err := s.ListFiltered(context.Background(), 2, 5, q)
	if err != nil || page.Total != 20 || len(page.Items) != 5 || page.Items[0].ID != "24" || c.count != 30 || !r.all {
		t.Fatalf("page=%+v read=%d all=%t err=%v", page, c.count, r.all, err)
	}
	q.ColumnFilters = nil
	for _, order := range []string{"asc", "desc"} {
		q.SortOrder = order
		p, e := s.ListFiltered(context.Background(), 7, 5, q)
		if e != nil || len(p.Items) != 1 || p.Items[0].ID != "missing" {
			t.Fatalf("null order: %+v %v", p, e)
		}
	}
	c.fail = true
	if _, err = s.ListFiltered(context.Background(), 1, 5, q); err == nil {
		t.Fatal("live failure returned partial result")
	}
}

// TestDownloadColumnValidation prevents malformed ranges and arbitrary column names.
func TestDownloadColumnValidation(t *testing.T) {
	for _, q := range []ports.DownloadListQuery{
		{SortBy: "DROP TABLE"}, {SortOrder: "desc"}, {SortBy: "code", SortOrder: "sideways"},
		{ColumnFilters: map[string][]string{"code": {"x"}}}, {ColumnFilters: map[string][]string{"size_bytes": {"NaN", ""}}},
		{ColumnFilters: map[string][]string{"size_bytes": {"5", "2"}}}, {ColumnFilters: map[string][]string{"share_ratio": {"-1", ""}}},
		{ColumnFilters: map[string][]string{"transfer_status": {"bogus"}}}, {ColumnFilters: map[string][]string{"added_at": {"bad", ""}}},
	} {
		if validateDownloadColumns(q) == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
}
