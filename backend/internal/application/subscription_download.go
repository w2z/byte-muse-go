package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"bytemuse/backend/internal/platform/torrentsearch"
	"bytemuse/backend/internal/ports"
)

// ResourceSearcher finds torrent candidates for one media code.
type ResourceSearcher interface {
	Search(context.Context, string) ([]torrentsearch.Resource, error)
}

// MultiResourceSearcher merges available PT and BT candidates while preserving partial site results.
type MultiResourceSearcher struct{ Sources []ResourceSearcher }

// Search queries configured sources and only fails when every source fails.
func (m MultiResourceSearcher) Search(ctx context.Context, code string) ([]torrentsearch.Resource, error) {
	var results []torrentsearch.Resource
	var successes int
	var last error
	for _, source := range m.Sources {
		items, e := source.Search(ctx, code)
		if e != nil {
			last = e
			continue
		}
		successes++
		results = append(results, items...)
	}
	if successes == 0 && last != nil {
		return nil, last
	}
	return results, nil
}

// PrivateTorrentSource resolves a selected PT reference to authenticated .torrent bytes.
type PrivateTorrentSource interface {
	Download(context.Context, string) ([]byte, string, error)
}

// ErrUnknownPrivateTorrentSource means a selected reference has no configured source adapter.
var ErrUnknownPrivateTorrentSource = errors.New("unknown private torrent source")

// PrivateTorrentSources resolves a source-qualified reference to its authenticated adapter.
type PrivateTorrentSources struct {
	Sources map[string]PrivateTorrentSource
}

// Download dispatches only exact source prefixes; no private URL is exposed to the downloader.
func (p PrivateTorrentSources) Download(ctx context.Context, reference string) ([]byte, string, error) {
	prefix, _, ok := strings.Cut(reference, ":")
	if !ok || p.Sources[prefix] == nil {
		return nil, "", ErrUnknownPrivateTorrentSource
	}
	return p.Sources[prefix].Download(ctx, reference)
}

// TorrentFileDownloader uploads a private .torrent file without leaking its URL to the download client.
type TorrentFileDownloader interface {
	SubmitTorrent(context.Context, []byte) error
}

// MagnetDownloader submits resources and rechecks ambiguous outcomes by info hash.
type MagnetDownloader interface {
	HasHash(context.Context, string) (bool, error)
	Submit(context.Context, string) error
}

// SubscriptionDownloadService coordinates durable search and safe submission.
type SubscriptionDownloadService struct {
	tasks          ports.SubscriptionDownloadRepository
	searcher       ResourceSearcher
	private        PrivateTorrentSource
	downloaders    map[string]MagnetDownloader
	settings       func(context.Context) (map[string]string, error)
	runtimeFactory func(map[string]string) (ResourceSearcher, PrivateTorrentSource, map[string]MagnetDownloader)
	notifier       Notifier
}

// SetNotifier 注入业务通知出口；未注入时下载流程不发送任何通知。
func (s *SubscriptionDownloadService) SetNotifier(notifier Notifier) { s.notifier = notifier }

// notifyDownloadStart 通知订阅下载已提交；标题与正文只包含番号、标题与站点，
// 配图使用任务快照里的封面，都不含任何凭据。
func (s *SubscriptionDownloadService) notifyDownloadStart(ctx context.Context, code, title, site, cover string) {
	if s.notifier == nil {
		return
	}
	lines := make([]string, 0, 2)
	if value := strings.TrimSpace(title); value != "" {
		lines = append(lines, value)
	}
	if value := strings.TrimSpace(site); value != "" {
		lines = append(lines, "站点："+value)
	}
	s.notifier.Notify(ctx, NotificationDownloadStart, NotificationMessage{
		Title:    NotificationHeadline(code, "开始下载"),
		Text:     strings.Join(lines, "\n"),
		CoverURL: cover,
	})
}

// notifyDownloadFailed 通知订阅下载任务失败；reason 与落库的失败原因保持一致。
func (s *SubscriptionDownloadService) notifyDownloadFailed(ctx context.Context, code, title, cover, reason string) {
	if s.notifier == nil {
		return
	}
	lines := make([]string, 0, 2)
	if value := strings.TrimSpace(title); value != "" {
		lines = append(lines, value)
	}
	lines = append(lines, "原因："+reason)
	s.notifier.Notify(ctx, NotificationDownloadFailed, NotificationMessage{
		Title:    NotificationHeadline(code, "下载失败"),
		Text:     strings.Join(lines, "\n"),
		CoverURL: cover,
	})
}

// failSearch 结束一次失败的搜索并推送失败通知；落库原因与通知文案使用同一个字符串，两处不会漂移。
func (s *SubscriptionDownloadService) failSearch(ctx context.Context, a *ports.SubscriptionDownloadAttempt, reason string) {
	_ = s.tasks.FinishSearch(ctx, *a, reason)
	s.notifyDownloadFailed(ctx, a.Code, a.Title, a.Cover, reason)
}

// SetRuntimeFactory rebuilds site and downloader clients from the current settings snapshot for each batch.
func (s *SubscriptionDownloadService) SetRuntimeFactory(factory func(map[string]string) (ResourceSearcher, PrivateTorrentSource, map[string]MagnetDownloader)) {
	s.runtimeFactory = factory
}

func (s *SubscriptionDownloadService) runtime(ctx context.Context) (map[string]string, ResourceSearcher, PrivateTorrentSource, map[string]MagnetDownloader, error) {
	settings, err := s.settings(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if s.runtimeFactory != nil {
		searcher, private, downloaders := s.runtimeFactory(settings)
		return settings, searcher, private, downloaders, nil
	}
	return settings, s.searcher, s.private, s.downloaders, nil
}

// SetPrivateTorrentSource enables authenticated PT resources on the same durable workflow.
func (s *SubscriptionDownloadService) SetPrivateTorrentSource(source PrivateTorrentSource) {
	s.private = source
}

// NewSubscriptionDownloadService injects live settings so changes take effect without a restart.
func NewSubscriptionDownloadService(tasks ports.SubscriptionDownloadRepository, searcher ResourceSearcher, downloaders map[string]MagnetDownloader, settings func(context.Context) (map[string]string, error)) *SubscriptionDownloadService {
	return &SubscriptionDownloadService{tasks: tasks, searcher: searcher, downloaders: downloaders, settings: settings}
}

// Enqueue schedules one active subscription and returns the existing task on repeated requests.
func (s *SubscriptionDownloadService) Enqueue(ctx context.Context, id string) (string, error) {
	task, e := s.tasks.Enqueue(ctx, id)
	return task.ID, e
}

// RunActive enqueues all active subscriptions and drains a bounded batch of durable tasks.
func (s *SubscriptionDownloadService) RunActive(ctx context.Context) (int, error) {
	n, e := s.tasks.EnqueueActive(ctx)
	if e != nil {
		return n, e
	}
	return n, s.Process(ctx, 100)
}

// Process handles a bounded batch, always reconciling ambiguous external submissions first.
func (s *SubscriptionDownloadService) Process(ctx context.Context, limit int) error {
	settings, searcher, private, downloaders, e := s.runtime(ctx)
	if e != nil {
		return e
	}
	for range limit {
		p, e := s.tasks.ClaimPending(ctx, time.Now())
		if e != nil {
			return e
		}
		if p == nil {
			break
		}
		client := downloaders[p.Downloader]
		if client == nil {
			_ = s.tasks.ReleasePending(ctx, *p, "下载器未配置，提交结果待核实")
			continue
		}
		found, e := client.HasHash(ctx, p.InfoHash)
		if e != nil {
			_ = s.tasks.ReleasePending(ctx, *p, "下载器回查失败，提交结果待核实")
			return fmt.Errorf("回查下载器: %w", e)
		}
		if found {
			if e = s.tasks.FinishSubmission(ctx, *p, true, ""); e != nil {
				return e
			}
			s.notifyDownloadStart(ctx, p.Code, p.Title, p.Site, p.Cover)
		} else {
			_ = s.tasks.ReleasePending(ctx, *p, "未发现资源，需人工确认后重试")
		}
	}
	for range limit {
		a, e := s.tasks.Claim(ctx, time.Now())
		if e != nil {
			return e
		}
		if a == nil {
			break
		}
		items, e := searcher.Search(ctx, a.Code)
		if e != nil {
			s.failSearch(ctx, a, "资源站搜索失败")
			continue
		}
		filter := a.Filter
		if len(filter) == 0 && settings["DEFAULT_FILTER"] != "" {
			if e = json.Unmarshal([]byte(settings["DEFAULT_FILTER"]), &filter); e != nil {
				s.failSearch(ctx, a, "默认过滤配置无效")
				continue
			}
		}
		selected, passed := selectResource(items, a.Mode, filter, settings["DEFAULT_SORT"], settings["MAIN_SITE"])
		if selected == nil {
			s.failSearch(ctx, a, "暂未找到符合条件的资源")
			continue
		}
		downloader := strings.TrimSpace(settings["BT_DEFAULT_DOWNLOADER"])
		if selected.Kind == "pt" {
			downloader = strings.TrimSpace(settings["PT_DEFAULT_DOWNLOADER"])
		}
		if downloader == "" {
			downloader = "qbittorrent"
		}
		if e = ValidateDefaultDownloader(SourceKind(selected.Kind), DownloaderKind(downloader)); e != nil {
			s.failSearch(ctx, a, "默认下载器不支持该资源")
			continue
		}
		if downloaders[downloader] == nil {
			s.failSearch(ctx, a, "默认下载器未配置")
			continue
		}
		var torrentFile []byte
		if selected.Kind == "pt" {
			_, ok := downloaders[downloader].(TorrentFileDownloader)
			if private == nil || !ok {
				s.failSearch(ctx, a, "PT 下载器或站点未配置")
				continue
			}
			var hash string
			torrentFile, hash, e = private.Download(ctx, selected.URI)
			if e != nil {
				s.failSearch(ctx, a, "PT 种子文件获取失败")
				continue
			}
			selected.InfoHash = hash
		}
		if e = s.tasks.SetCandidate(ctx, *a, selected.Site, selected.Kind, selected.URI, selected.InfoHash, downloader, passed); e != nil {
			return e
		}
		client := downloaders[downloader]
		found, e := client.HasHash(ctx, selected.InfoHash)
		if e == nil && !found {
			if selected.Kind == "pt" {
				e = client.(TorrentFileDownloader).SubmitTorrent(ctx, torrentFile)
			} else {
				e = client.Submit(ctx, selected.URI)
			}
			if e == nil {
				found, e = client.HasHash(ctx, selected.InfoHash)
			}
		}
		if e != nil {
			return fmt.Errorf("提交或回查下载器: %w", e)
		}
		if found {
			p := ports.PendingSubmission{ID: a.ID, LeaseToken: a.LeaseToken}
			if e = s.tasks.FinishSubmission(ctx, p, true, ""); e != nil {
				return e
			}
			s.notifyDownloadStart(ctx, a.Code, a.Title, selected.Site, a.Cover)
		}
	}
	return nil
}
