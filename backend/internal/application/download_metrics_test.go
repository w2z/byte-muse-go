package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"errors"
	"testing"
	"time"
)

type metricsClient struct {
	calls int
	fail  bool
}

func (c *metricsClient) Capabilities(context.Context) ([]string, error) { return nil, nil }
func (c *metricsClient) Observe(context.Context, string) (*ports.TransferState, error) {
	return nil, nil
}
func (c *metricsClient) Control(context.Context, string, string) error { return nil }
func (c *metricsClient) ReadMetrics(_ context.Context, hashes []string) (map[string]*domain.DownloadMetrics, error) {
	c.calls++
	if len(hashes) != 2 {
		panic("expected one batch for both tasks")
	}
	if c.fail {
		return nil, errors.New("offline")
	}
	zero := int64(0)
	return map[string]*domain.DownloadMetrics{"a": {DownloadSpeed: &zero}}, nil
}

// TestDecorateMetrics 验证同下载器只批量查询一次，缺失任务和失败不伪造零值。
func TestDecorateMetrics(t *testing.T) {
	name, hashA, hashB, kind := "qbittorrent", "A", "b", "bt"
	for _, fail := range []bool{false, true} {
		client := &metricsClient{fail: fail}
		tasks := []domain.DownloadTask{{Downloader: &name, InfoHash: &hashA, SourceKind: &kind}, {Downloader: &name, InfoHash: &hashB}}
		decorateMetrics(context.Background(), tasks, map[string]ports.DownloadController{name: client})
		if client.calls != 1 || tasks[1].Metrics != nil {
			t.Fatal(tasks)
		}
		if fail && tasks[0].Metrics != nil {
			t.Fatal(tasks)
		}
		if !fail && (tasks[0].Metrics == nil || *tasks[0].Metrics.DownloadSpeed != 0) {
			t.Fatal(tasks)
		}
		if tasks[0].Seeding.Status != "not_applicable" {
			t.Fatal(tasks)
		}
	}
}

// TestSeedingAssessment 覆盖阈值、未知数据和限期证据，禁止把下载完成当做种达标。
func TestSeedingAssessment(t *testing.T) {
	now := time.Now().UTC()
	added := now.Add(-time.Hour)
	for _, tc := range []struct {
		site, kind string
		seconds    int64
		ratio      float64
		completed  bool
		want       string
	}{
		{"RousiPro", "pt", 86400, 0, true, "completed"},
		{"RousiPro", "pt", 0, 1, true, "completed"},
		{"RousiPro", "pt", 86399, .99, true, "pending"},
		{"NicePT", "pt", 0, 2, true, "completed"},
		{"NicePT", "pt", 0, 2, false, "pending"},
		{"PTFans", "pt", 604800, 0, true, "completed"},
		{"馒头", "pt", 0, 0, false, "not_required"},
		{"other", "pt", 999999, 9, true, "unknown"},
		{"Nyaa BT", "bt", 0, 0, false, "not_applicable"},
	} {
		t.Run(tc.site+tc.want, func(t *testing.T) {
			task := domain.DownloadTask{SourceSite: &tc.site, SourceKind: &tc.kind}
			metrics := &domain.DownloadMetrics{SeedingSeconds: &tc.seconds, ShareRatio: &tc.ratio, Complete: tc.completed, AddedAt: &added}
			if got := assessSeeding(task, metrics, now); got.Status != tc.want {
				t.Fatalf("got=%+v want=%s", got, tc.want)
			}
		})
	}
	site, kind := "PTFans", "pt"
	task := domain.DownloadTask{SourceSite: &site, SourceKind: &kind}
	if got := assessSeeding(task, nil, now); got.Status != "unknown" {
		t.Fatal(got)
	}
	old := now.Add(-36 * 24 * time.Hour)
	seconds := int64(604800)
	if got := assessSeeding(task, &domain.DownloadMetrics{Complete: true, AddedAt: &old, SeedingSeconds: &seconds}, now); got.Status != "unknown" {
		t.Fatal(got)
	}
}
