package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// downloadControllerName 是卡片测试里下载器任务的标识，与下载器替身注册名一致。
const downloadControllerName = "qbittorrent"

// libraryMedia 构造一条已入库影片，覆盖卡片按钮映射需要的字段。
func libraryMedia(id, code, title string) domain.Media {
	media := testMedia(id, code, title)
	media.LibraryStatus = domain.LibraryStatusPresent
	return media
}

// subscribedMedia 在已入库影片上标记活动订阅，供按钮映射与动作执行使用。
func subscribedMedia(id, code, title string) domain.Media {
	media := libraryMedia(id, code, title)
	media.SubscriptionStatus = domain.SubscriptionStatusActive
	media.ActiveSubscription = &domain.Subscription{
		ID:      "sub-" + id,
		MediaID: id,
		Status:  domain.SubscriptionStatusActive,
		Mode:    domain.SubscriptionModeStrict,
	}
	return media
}

// downloadTask 构造一条带下载器标识的下载任务，覆盖按钮映射与控制链路所需字段。
func downloadTask(mediaID string, status domain.DownloadStatus, transfer string, actions ...string) domain.DownloadTask {
	hash := "hash-" + mediaID
	downloader := downloadControllerName
	return domain.DownloadTask{
		ID:               "task-" + mediaID,
		MediaID:          mediaID,
		Status:           status,
		Downloader:       &downloader,
		InfoHash:         &hash,
		TransferStatus:   &transfer,
		AvailableActions: actions,
	}
}

// ptrTask 返回任务指针；nil 任务用于表达「没有下载任务」。
func ptrTask(task domain.DownloadTask) *domain.DownloadTask { return &task }

// cardCatalog 是卡片测试用的影片查询替身：订阅状态实时取自订阅仓储，
// 使「点击订阅后刷新按钮」这类跨状态断言与真实联表行为一致。
type cardCatalog struct {
	*fakeCatalogQueryRepository
	repo *fakeSubscriptionRepository
}

func (c cardCatalog) Search(ctx context.Context, query string, limit, offset int) (domain.MediaPage, error) {
	page, err := c.fakeCatalogQueryRepository.Search(ctx, query, limit, offset)
	if err != nil {
		return page, err
	}
	for index := range page.Items {
		page.Items[index] = c.withSubscription(page.Items[index])
	}
	return page, nil
}

func (c cardCatalog) withSubscription(media domain.Media) domain.Media {
	for _, item := range c.repo.items {
		if item.MediaID != media.ID || item.Status != domain.SubscriptionStatusActive {
			continue
		}
		current := item
		media.SubscriptionStatus = domain.SubscriptionStatusActive
		media.ActiveSubscription = &current
		return media
	}
	media.SubscriptionStatus = domain.SubscriptionStatusNone
	media.ActiveSubscription = nil
	return media
}

// cardTestDeps 组装卡片测试依赖：内存影片查询、订阅仓储与下载仓储。
func cardTestDeps(media ...domain.Media) (Deps, *fakeSubscriptionRepository, *fakeDownloadRepository) {
	repo := newFakeSubscriptionRepository(media...)
	downloads := &fakeDownloadRepository{}
	deps := Deps{
		Queries:       application.NewCatalogQueryService(cardCatalog{fakeCatalogQueryRepository: &fakeCatalogQueryRepository{media: media}, repo: repo}),
		Subscriptions: application.NewSubscriptionService(repo),
		Downloads:     application.NewDownloadService(downloads),
	}
	return deps, repo, downloads
}

// fakeDownloadController 按预置状态序列响应 Observe：第一次用于控制前校验，之后用于结果回查。
type fakeDownloadController struct {
	capabilities []string
	states       []string
	observations int
	controlled   []string
}

func (c *fakeDownloadController) Capabilities(context.Context) ([]string, error) {
	return c.capabilities, nil
}

func (c *fakeDownloadController) Observe(context.Context, string) (*ports.TransferState, error) {
	index := c.observations
	c.observations++
	if index >= len(c.states) {
		index = len(c.states) - 1
	}
	return &ports.TransferState{Hash: "hash", Status: c.states[index]}, nil
}

func (c *fakeDownloadController) Control(_ context.Context, _ string, action string) error {
	c.controlled = append(c.controlled, action)
	return nil
}

// fakeDownloadControlRepository 复用内存下载仓储，FinishControl 时把回查状态写回任务，
// 与真实仓储在控制完成后更新 transfer_status 的行为一致。
type fakeDownloadControlRepository struct{ tasks *fakeDownloadRepository }

func (r *fakeDownloadControlRepository) LockControl(_ context.Context, id string) (domain.DownloadTask, string, error) {
	for _, task := range r.tasks.tasks {
		if task.ID == id {
			return task, "token-1", nil
		}
	}
	return domain.DownloadTask{}, "", errors.New("下载任务不存在")
}

func (r *fakeDownloadControlRepository) FinishControl(_ context.Context, id, _, _ string, state *ports.TransferState) error {
	for index := range r.tasks.tasks {
		if r.tasks.tasks[index].ID != id {
			continue
		}
		if state != nil {
			status := state.Status
			r.tasks.tasks[index].TransferStatus = &status
		}
		return nil
	}
	return nil
}

func (r *fakeDownloadControlRepository) ReleaseControl(context.Context, string, string) error {
	return nil
}

// wireDownloadControl 给下载服务注入控制仓储与下载器替身，使按钮点击能走真实控制链路。
func wireDownloadControl(deps *Deps, downloads *fakeDownloadRepository, controller ports.DownloadController) {
	service := application.NewDownloadService(downloads)
	service.SetControls(&fakeDownloadControlRepository{tasks: downloads}, func(context.Context) (map[string]ports.DownloadController, error) {
		return map[string]ports.DownloadController{downloadControllerName: controller}, nil
	})
	deps.Downloads = service
}

// TestCardDataRoundTrip 验证按钮载荷的组装与解析严格对称，非法载荷一律拒绝。
func TestCardDataRoundTrip(t *testing.T) {
	for _, action := range []string{cardActionSubscribe, cardActionUnsubscribe, cardActionDownload, cardActionPause, cardActionResume, cardActionRetry} {
		data := cardData(action, "SSIS-001")
		gotAction, gotCode, ok := parseCardData(data)
		if !ok || gotAction != action || gotCode != "SSIS-001" {
			t.Fatalf("载荷 %q 解析为 (%q,%q,%v)", data, gotAction, gotCode, ok)
		}
	}
	// 番号在载荷里也必须归一化，保证同一部影片只有一种写法。
	if action, code, ok := parseCardData("bm:sub:ssis 001"); !ok || action != cardActionSubscribe || code != "SSIS-001" {
		t.Fatalf("载荷未归一化番号: (%q,%q,%v)", action, code, ok)
	}
	for _, raw := range []string{"", "bm", "bm:sub", "bm:sub:", "bm::SSIS-001", "bm:unknown:SSIS-001", "other:sub:SSIS-001", "bm:sub:---"} {
		if _, _, ok := parseCardData(raw); ok {
			t.Fatalf("非法载荷 %q 应被拒绝", raw)
		}
	}
}

// TestCardButtonsFollowCurrentState 验证按钮完全由订阅状态与下载任务可用操作决定。
func TestCardButtonsFollowCurrentState(t *testing.T) {
	code := "SSIS-001"
	for _, tc := range []struct {
		name   string
		media  domain.Media
		task   *domain.DownloadTask
		labels []string
	}{
		{name: "未入库无按钮", media: testMedia("m1", code, "First")},
		{name: "已入库未订阅", media: libraryMedia("m1", code, "First"), labels: []string{"订阅"}},
		{name: "已订阅无任务", media: subscribedMedia("m1", code, "First"), labels: []string{"开始下载", "取消订阅"}},
		{name: "下载中可暂停", media: subscribedMedia("m1", code, "First"), task: ptrTask(downloadTask("m1", domain.DownloadStatusDownloading, "downloading", "pause", "stop")), labels: []string{"暂停", "取消订阅"}},
		{name: "已暂停可继续", media: subscribedMedia("m1", code, "First"), task: ptrTask(downloadTask("m1", domain.DownloadStatusDownloading, "paused", "resume")), labels: []string{"继续", "取消订阅"}},
		{name: "失败可重试", media: subscribedMedia("m1", code, "First"), task: ptrTask(downloadTask("m1", domain.DownloadStatusFailed, "failed", "retry")), labels: []string{"重试", "取消订阅"}},
		{name: "下载器不支持暂停", media: subscribedMedia("m1", code, "First"), task: ptrTask(downloadTask("m1", domain.DownloadStatusDownloading, "downloading", "stop")), labels: []string{"取消订阅"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buttons := cardButtons(tc.media, tc.task)
			if len(buttons) != len(tc.labels) {
				t.Fatalf("按钮数=%d，期望 %d：%+v", len(buttons), len(tc.labels), buttons)
			}
			for index, label := range tc.labels {
				if buttons[index].Label != label {
					t.Fatalf("第 %d 个按钮=%q，期望 %q", index+1, buttons[index].Label, label)
				}
				if !strings.HasPrefix(buttons[index].Data, cardActionPrefix+":") {
					t.Fatalf("按钮载荷缺少前缀: %q", buttons[index].Data)
				}
			}
		})
	}
}

// TestCardForCodeBuildsCardForLibraryMedia 验证已入库番号返回带封面与按钮的卡片。
func TestCardForCodeBuildsCardForLibraryMedia(t *testing.T) {
	deps, _, _ := cardTestDeps(libraryMedia("m1", "SSIS-001", "First"))
	reply := cardForCode(context.Background(), deps, "ssis 001")
	if reply.Card == nil {
		t.Fatalf("已入库番号应返回卡片: %+v", reply)
	}
	if reply.Card.Title != "SSIS-001" {
		t.Fatalf("卡片标题=%q，期望归一化番号", reply.Card.Title)
	}
	if reply.Card.PhotoURL != "https://example.test/SSIS-001.jpg" {
		t.Fatalf("卡片封面=%q", reply.Card.PhotoURL)
	}
	if !strings.Contains(reply.Card.Text, "中文-First") || !strings.Contains(reply.Card.Text, "状态：未订阅") {
		t.Fatalf("卡片正文=%q", reply.Card.Text)
	}
	if len(reply.Card.Buttons) != 1 || reply.Card.Buttons[0].Label != "订阅" {
		t.Fatalf("卡片按钮=%+v", reply.Card.Buttons)
	}
	if reply.Text == "" {
		t.Fatal("卡片回复必须携带降级文本")
	}
}

// TestCardForCodeWithoutActionsReturnsPlainText 验证没有可执行操作时不发卡片，只回文本。
func TestCardForCodeWithoutActionsReturnsPlainText(t *testing.T) {
	deps, _, _ := cardTestDeps(testMedia("m1", "SSIS-001", "First"))
	reply := cardForCode(context.Background(), deps, "SSIS-001")
	if reply.Card != nil {
		t.Fatalf("未入库影片不应返回卡片: %+v", reply)
	}
	if !strings.Contains(reply.Text, "状态：未入库") {
		t.Fatalf("文本=%q", reply.Text)
	}
}

// TestCardForCodeReportsInvalidAndUnknownCodes 验证无效番号与未入库番号给出可执行提示。
func TestCardForCodeReportsInvalidAndUnknownCodes(t *testing.T) {
	deps, _, _ := cardTestDeps(libraryMedia("m1", "SSIS-001", "First"))
	if reply := cardForCode(context.Background(), deps, "---"); !strings.Contains(reply.Text, "番号无效") {
		t.Fatalf("无效番号提示=%q", reply.Text)
	}
	if reply := cardForCode(context.Background(), deps, "SSIS-999"); !strings.Contains(reply.Text, "不在媒体库中") {
		t.Fatalf("未入库番号提示=%q", reply.Text)
	}
}

// TestCardForKnownCodeOnlyMatchesLibraryMedia 验证 AI 前置截获只对已入库番号生效。
func TestCardForKnownCodeOnlyMatchesLibraryMedia(t *testing.T) {
	deps, _, _ := cardTestDeps(libraryMedia("m1", "SSIS-001", "First"))
	if _, ok := cardForKnownCode(context.Background(), deps, "SSIS-001"); !ok {
		t.Fatal("已入库番号应命中卡片分支")
	}
	if _, ok := cardForKnownCode(context.Background(), deps, "SSIS-999"); ok {
		t.Fatal("未入库番号不应命中卡片分支")
	}
	if _, ok := cardForKnownCode(context.Background(), Deps{}, "SSIS-001"); ok {
		t.Fatal("查询服务缺失时不应命中卡片分支")
	}
}

// TestHandleCardActionSubscribeCreatesSubscriptionAndRefreshesButtons 验证点击「订阅」真正创建订阅，
// 并按订阅后的最新状态把按钮刷新为「取消订阅」。
func TestHandleCardActionSubscribeCreatesSubscriptionAndRefreshesButtons(t *testing.T) {
	deps, repo, _ := cardTestDeps(libraryMedia("m1", "SSIS-001", "First"))
	reply := handleCardAction(context.Background(), deps, ports.InboundAction{Data: cardData(cardActionSubscribe, "SSIS-001"), MessageID: "77"})
	if !strings.Contains(reply.Text, "已加入订阅") {
		t.Fatalf("订阅结果=%q", reply.Text)
	}
	if len(repo.items) != 1 {
		t.Fatalf("订阅条数=%d，期望 1", len(repo.items))
	}
	if reply.Refresh == nil || reply.Refresh.MessageID != "77" {
		t.Fatalf("按钮刷新=%+v", reply.Refresh)
	}
	// 订阅后没有下载任务，按钮应为「开始下载」+「取消订阅」。
	if len(reply.Refresh.Buttons) != 2 || reply.Refresh.Buttons[0].Label != "开始下载" || reply.Refresh.Buttons[1].Label != "取消订阅" {
		t.Fatalf("刷新后按钮=%+v", reply.Refresh.Buttons)
	}
}

// TestHandleCardActionUnsubscribeRemovesSubscription 验证点击「取消订阅」真正删除订阅并刷新按钮。
func TestHandleCardActionUnsubscribeRemovesSubscription(t *testing.T) {
	media := subscribedMedia("m1", "SSIS-001", "First")
	deps, repo, _ := cardTestDeps(media)
	repo.add("m1", domain.SubscriptionStatusActive)
	reply := handleCardAction(context.Background(), deps, ports.InboundAction{Data: cardData(cardActionUnsubscribe, "SSIS-001"), MessageID: "78"})
	if !strings.Contains(reply.Text, "已取消订阅") {
		t.Fatalf("取消结果=%q", reply.Text)
	}
	if len(repo.items) != 0 {
		t.Fatalf("订阅应被删除，剩余 %+v", repo.items)
	}
	if reply.Refresh == nil || len(reply.Refresh.Buttons) != 1 || reply.Refresh.Buttons[0].Label != "订阅" {
		t.Fatalf("刷新后按钮=%+v", reply.Refresh)
	}
}

// TestHandleCardActionControlsDownloadAndRefreshesButtons 验证点击「暂停」走真实下载器控制链路，
// 并用回查后的最新状态把按钮刷新为「继续」。
func TestHandleCardActionControlsDownloadAndRefreshesButtons(t *testing.T) {
	deps, repo, downloads := cardTestDeps(subscribedMedia("m1", "SSIS-001", "First"))
	repo.add("m1", domain.SubscriptionStatusActive)
	downloads.tasks = []domain.DownloadTask{downloadTask("m1", domain.DownloadStatusDownloading, "downloading", "pause", "stop")}
	controller := &fakeDownloadController{capabilities: []string{"pause", "resume", "stop"}, states: []string{"downloading", "paused"}}
	wireDownloadControl(&deps, downloads, controller)

	reply := handleCardAction(context.Background(), deps, ports.InboundAction{Data: cardData(cardActionPause, "SSIS-001"), MessageID: "88"})
	if !strings.Contains(reply.Text, "已执行「暂停」") {
		t.Fatalf("暂停结果=%q", reply.Text)
	}
	if len(controller.controlled) != 1 || controller.controlled[0] != "pause" {
		t.Fatalf("下载器控制调用=%v", controller.controlled)
	}
	if reply.Refresh == nil || len(reply.Refresh.Buttons) != 2 || reply.Refresh.Buttons[0].Label != "继续" {
		t.Fatalf("刷新后按钮=%+v", reply.Refresh)
	}
}

// TestHandleCardActionReportsMissingTaskAndExpiredPayload 验证无任务与过期载荷都如实说明。
func TestHandleCardActionReportsMissingTaskAndExpiredPayload(t *testing.T) {
	deps, _, _ := cardTestDeps(subscribedMedia("m1", "SSIS-001", "First"))
	if reply := handleCardAction(context.Background(), deps, ports.InboundAction{Data: cardData(cardActionPause, "SSIS-001")}); !strings.Contains(reply.Text, "没有下载任务") {
		t.Fatalf("无任务提示=%q", reply.Text)
	}
	if reply := handleCardAction(context.Background(), deps, ports.InboundAction{Data: "other:sub:SSIS-001"}); reply.Text != cardActionExpired {
		t.Fatalf("过期载荷提示=%q", reply.Text)
	}
}

// TestCardTextFallsBackToDownloadStatus 验证没有下载器实时状态时使用落库的下载状态。
func TestCardTextFallsBackToDownloadStatus(t *testing.T) {
	media := subscribedMedia("m1", "SSIS-001", "First")
	status := domain.DownloadStatusSearching
	media.DownloadStatus = &status
	_, body := cardText(media, nil)
	if !strings.Contains(body, "状态：正在搜索资源") {
		t.Fatalf("正文=%q", body)
	}
}
