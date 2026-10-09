package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"bytemuse/backend/internal/logging"
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

// PrivateTorrentSource 返回种子字节、InfoHash、实际下载 URL 和错误。
// URL 只用于任务内部快照，可能含临时凭据；资源引用仍用于通知和重新获取种子。
type PrivateTorrentSource interface {
	Download(context.Context, string) ([]byte, string, string, error)
}

// ErrUnknownPrivateTorrentSource means a selected reference has no configured source adapter.
var ErrUnknownPrivateTorrentSource = errors.New("unknown private torrent source")

// PrivateTorrentSources resolves a source-qualified reference to its authenticated adapter.
type PrivateTorrentSources struct {
	Sources map[string]PrivateTorrentSource
}

// Download dispatches only exact source prefixes; no private URL is exposed to the downloader.
func (p PrivateTorrentSources) Download(ctx context.Context, reference string) ([]byte, string, string, error) {
	prefix, _, ok := strings.Cut(reference, ":")
	if !ok || p.Sources[prefix] == nil {
		return nil, "", "", ErrUnknownPrivateTorrentSource
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

// notifyDownloadStart 从已提交任务快照生成通知，首次提交与恢复回查使用同一格式。
func (s *SubscriptionDownloadService) notifyDownloadStart(ctx context.Context, pending ports.PendingSubmission) {
	if s.notifier == nil {
		return
	}
	s.notifier.Notify(ctx, NotificationDownloadStart, NewNotificationMessage(pending.Code, "开始下载", pending.Site, pending.Kind, pending.URI, pending.Title, pending.Cover))
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
	s.notifier.Notify(ctx, NotificationDownloadFailed, NewNotificationMessage(code, "下载失败", "", "", "", strings.Join(lines, " "), cover))
}

// finishScan 删除一次已领取的搜索队列项：搜索结束且不产生下载任务，也不推送通知。
func (s *SubscriptionDownloadService) finishScan(ctx context.Context, a ports.SubscriptionScanAttempt) {
	if e := s.tasks.FinishScan(ctx, a); e != nil {
		logging.Error(logging.CategoryDownload, "结束订阅搜索失败", "scan_id", a.ID, "subscription_id", a.SubscriptionID, "error", e.Error())
	}
}

// failSearch 结束一次没有选中资源的搜索；原因只写日志，不落库、不污染下载列表。
// 只有用户显式发起的搜索才推送通知：定时任务批量扫描没找到资源属于正常状态，每次扫描都推送只会变成刷屏。
// 下载器报告的真实失败走 notifyTransferTransitions，不受这里影响。
func (s *SubscriptionDownloadService) failSearch(ctx context.Context, a *ports.SubscriptionScanAttempt, reason string) {
	s.finishScan(ctx, *a)
	logging.Info(logging.CategoryDownload, "订阅资源搜索未产生下载任务", "scan_id", a.ID, "subscription_id", a.SubscriptionID, "code", a.Code, "origin", string(a.Origin), "reason", reason)
	if a.Origin != ports.DownloadOriginUser {
		return
	}
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

// Enqueue 为一条有效订阅登记一次资源搜索，返回本次请求对应的持久化标识。
// 该订阅已有进行中的下载任务时直接返回该任务标识；已有待执行搜索时只升级发起方，不重复入队。
// origin 是这次搜索的发起方，决定搜索失败后是否推送通知。
func (s *SubscriptionDownloadService) Enqueue(ctx context.Context, id string, origin ports.DownloadOrigin) (string, error) {
	return s.tasks.EnqueueScan(ctx, id, origin)
}

// RunActiveScans 登记全部有效订阅并处理一批搜索。
// 批量扫描的来源固定为 schedule：没找到资源属于正常状态，不能为每条订阅推送失败通知。
// 只有用户针对具体番号显式发起的 Enqueue 才按 user 来源推送。
func (s *SubscriptionDownloadService) RunActiveScans(ctx context.Context) (int, error) {
	n, e := s.tasks.EnqueueActiveScans(ctx)
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
			s.notifyDownloadStart(ctx, *p)
		} else {
			_ = s.tasks.ReleasePending(ctx, *p, "未发现资源，需人工确认后重试")
		}
	}
	for range limit {
		a, e := s.tasks.ClaimScan(ctx, time.Now())
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
		downloadURL := selected.URI
		var torrentFile []byte
		if selected.Kind == "pt" {
			_, ok := downloaders[downloader].(TorrentFileDownloader)
			if private == nil || !ok {
				s.failSearch(ctx, a, "PT 下载器或站点未配置")
				continue
			}
			var hash string
			torrentFile, hash, downloadURL, e = private.Download(ctx, selected.URI)
			if e != nil {
				s.failSearch(ctx, a, "PT 种子文件获取失败")
				continue
			}
			selected.InfoHash = hash
		}
		p, e := s.tasks.StartTask(ctx, *a, ports.ScanCandidate{Site: selected.Site, Kind: selected.Kind, URI: selected.URI, DownloadURL: downloadURL, InfoHash: selected.InfoHash, Downloader: downloader, FilterPassed: passed})
		if errors.Is(e, ports.ErrSubscriptionTaskActive) || errors.Is(e, ports.ErrSubscriptionNotFound) {
			// 已有有效任务或订阅已失效：本次搜索结束，不再建立重复任务。
			s.finishScan(ctx, *a)
			continue
		}
		if e != nil {
			return e
		}
		// 资源已建立下载任务，搜索队列项完成使命，立即删除，避免租约过期后被重复领取。
		s.finishScan(ctx, *a)
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
			if e = s.tasks.FinishSubmission(ctx, p, true, ""); e != nil {
				return e
			}
			s.notifyDownloadStart(ctx, p)
		}
	}
	return nil
}
