package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type columnRepository struct {
	items []domain.DownloadTask
	all   bool
}

// TestDownloadSourceSites covers all stored categories, missing sources and exact multi-selection.
func TestDownloadSourceSites(t *testing.T) {
	r := &columnRepository{}
	for i, site := range []string{"Site A", "Site A", "site a", "Site AB", "Other", ""} {
		r.items = append(r.items, domain.DownloadTask{ID: fmt.Sprint(i), SourceSite: &site})
	}
	r.items = append(r.items, domain.DownloadTask{ID: "null"})
	s := NewDownloadService(r)
	sites, err := s.SourceSites(context.Background())
	want := []DownloadSourceSite{{Value: "site a", Label: "Site A", Count: 3}, {Value: "site ab", Label: "Site AB", Count: 1}, {Value: "other", Label: "Other", Count: 1}, {Value: "", Label: "未识别", Count: 2}}
	// Categories are ordered by label, with the missing-source category last.
	want[0], want[1], want[2] = want[2], want[0], want[1]
	if err != nil || !r.all || !reflect.DeepEqual(sites, want) {
		t.Fatalf("sites=%+v err=%v all=%v", sites, err, r.all)
	}
	q := ports.DownloadListQuery{ColumnFilters: map[string][]string{"source_site": {"SITE A", ""}}}
	p, err := s.ListFiltered(context.Background(), 2, 2, q)
	if err != nil || p.Total != 5 || len(p.Items) != 2 || p.Items[0].ID != "2" || p.Items[1].ID != "5" {
		t.Fatalf("page=%+v err=%v", p, err)
	}
	empty, err := NewDownloadService(&columnRepository{}).SourceSites(context.Background())
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
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
		{ColumnFilters: map[string][]string{"downloader": {"bogus"}}}, {ColumnFilters: map[string][]string{"size_bytes": {"NaN", ""}}},
		{ColumnFilters: map[string][]string{"size_bytes": {"5", "2"}}}, {ColumnFilters: map[string][]string{"share_ratio": {"-1", ""}}},
		{ColumnFilters: map[string][]string{"transfer_status": {"bogus"}}}, {ColumnFilters: map[string][]string{"added_at": {"bad", ""}}},
	} {
		if validateDownloadColumns(q) == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
}

// TestDownloadCodeAndDownloaderFilters checks case-insensitive code search, exact client choices and global pagination.
func TestDownloadCodeAndDownloaderFilters(t *testing.T) {
	r := &columnRepository{}
	for i, name := range []string{"qbittorrent", "transmission", "aria2", "qbittorrent-copy"} {
		code := "TEST-001"
		r.items = append(r.items, domain.DownloadTask{ID: fmt.Sprint(i), Code: &code, Downloader: &name})
	}
	r.items = append(r.items, domain.DownloadTask{ID: "missing"})
	s := NewDownloadService(r)
	q := ports.DownloadListQuery{SortBy: "code", ColumnFilters: map[string][]string{"code": {"test-"}, "downloader": {"qbittorrent", "transmission"}}}
	p, err := s.ListFiltered(context.Background(), 2, 1, q)
	if err != nil || p.Total != 2 || len(p.Items) != 1 || p.Items[0].ID != "1" || !r.all {
		t.Fatalf("page=%+v all=%t err=%v", p, r.all, err)
	}
}
