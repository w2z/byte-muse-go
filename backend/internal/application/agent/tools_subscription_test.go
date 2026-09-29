package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// fakeCatalogQueryRepository 是只服务测试的内存影片查询仓储，不接触真实数据库。
type fakeCatalogQueryRepository struct {
	media    []domain.Media
	lastRank string
}

// Search 按番号或标题包含关键词匹配，行为对齐仓储的关键词语义。
func (r *fakeCatalogQueryRepository) Search(_ context.Context, query string, limit, offset int) (domain.MediaPage, error) {
	needle := strings.ToLower(strings.TrimSpace(query))
	matched := make([]domain.Media, 0, len(r.media))
	for _, item := range r.media {
		if needle == "" || strings.Contains(strings.ToLower(item.Code), needle) || strings.Contains(strings.ToLower(item.Title), needle) {
			matched = append(matched, item)
		}
	}
	return domain.MediaPage{Items: pageSlice(matched, limit, offset), Total: len(matched)}, nil
}

// Rank 记录榜单周期，便于断言工具的取值收敛规则。
func (r *fakeCatalogQueryRepository) Rank(_ context.Context, rankType string, limit, offset int) (domain.MediaPage, error) {
	r.lastRank = rankType
	return domain.MediaPage{Items: pageSlice(r.media, limit, offset), Total: len(r.media)}, nil
}

// ReleaseToday 返回全部测试影片。
func (r *fakeCatalogQueryRepository) ReleaseToday(_ context.Context, _ string, limit, offset int) (domain.MediaPage, error) {
	return domain.MediaPage{Items: pageSlice(r.media, limit, offset), Total: len(r.media)}, nil
}

// Recommend 返回全部测试影片。
func (r *fakeCatalogQueryRepository) Recommend(_ context.Context, _, _ string, limit, offset int) (domain.MediaPage, error) {
	return domain.MediaPage{Items: pageSlice(r.media, limit, offset), Total: len(r.media)}, nil
}

// pageSlice 模拟仓储分页：越界返回空切片，limit<=0 视为不限。
func pageSlice[T any](items []T, limit, offset int) []T {
	if offset >= len(items) {
		return []T{}
	}
	end := len(items)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return items[offset:end]
}

// fakeSubscriptionRepository 是只服务测试的内存订阅仓储，保留幂等键与取消语义。
type fakeSubscriptionRepository struct {
	items  []domain.Subscription
	keys   map[string]string
	media  map[string]domain.Media
	nextID int
}

// newFakeSubscriptionRepository 用测试影片建立番号到影片的关联索引。
func newFakeSubscriptionRepository(media ...domain.Media) *fakeSubscriptionRepository {
	index := make(map[string]domain.Media, len(media))
	for _, item := range media {
		index[item.ID] = item
	}
	return &fakeSubscriptionRepository{keys: map[string]string{}, media: index}
}

// Create 复刻真实仓储的幂等创建：相同幂等键返回既有订阅，不重复写入。
func (r *fakeSubscriptionRepository) Create(_ context.Context, request ports.CreateSubscription) (domain.Subscription, bool, error) {
	if id, ok := r.keys[request.IdempotencyKey]; ok {
		for _, item := range r.items {
			if item.ID == id {
				return item, false, nil
			}
		}
	}
	r.nextID++
	item := domain.Subscription{
		ID:        fmt.Sprintf("sub-%d", r.nextID),
		MediaID:   request.MediaID,
		Status:    domain.SubscriptionStatusActive,
		Mode:      request.Mode,
		Filter:    request.Filter,
		CreatedAt: time.Now().UTC(),
		Media:     r.mediaFor(request.MediaID),
	}
	r.items = append(r.items, item)
	r.keys[request.IdempotencyKey] = item.ID
	return item, true, nil
}

// Update 在测试中不支持，返回真实仓储的“订阅不可编辑”语义。
func (r *fakeSubscriptionRepository) Update(context.Context, ports.UpdateSubscription) (domain.Subscription, error) {
	return domain.Subscription{}, ports.ErrSubscriptionInactive
}

// Cancel 删除有效订阅；不存在或已取消时返回 ports.ErrSubscriptionNotFound。
func (r *fakeSubscriptionRepository) Cancel(_ context.Context, id string) (domain.Subscription, bool, error) {
	for index, item := range r.items {
		if item.ID != id {
			continue
		}
		if item.Status != domain.SubscriptionStatusActive {
			return domain.Subscription{}, false, ports.ErrSubscriptionNotFound
		}
		r.items = append(r.items[:index], r.items[index+1:]...)
		return item, true, nil
	}
	return domain.Subscription{}, false, ports.ErrSubscriptionNotFound
}

// List 按状态过滤并补全影片关联，模拟仓储的媒体联表。
func (r *fakeSubscriptionRepository) List(_ context.Context, query ports.SubscriptionListQuery) (domain.SubscriptionPage, error) {
	matched := make([]domain.Subscription, 0, len(r.items))
	for _, item := range r.items {
		if query.Status != "" && item.Status != query.Status {
			continue
		}
		item.Media = r.mediaFor(item.MediaID)
		matched = append(matched, item)
	}
	return domain.SubscriptionPage{Items: pageSlice(matched, query.Limit, query.Offset), Total: len(matched)}, nil
}

// add 预置一条指定状态的订阅，返回订阅 ID。
func (r *fakeSubscriptionRepository) add(mediaID string, status domain.SubscriptionStatus) string {
	r.nextID++
	id := fmt.Sprintf("sub-%d", r.nextID)
	r.items = append(r.items, domain.Subscription{
		ID:        id,
		MediaID:   mediaID,
		Status:    status,
		Mode:      domain.SubscriptionModeStrict,
		CreatedAt: time.Now().UTC(),
		Media:     r.mediaFor(mediaID),
	})
	return id
}

func (r *fakeSubscriptionRepository) mediaFor(id string) *domain.Media {
	item, ok := r.media[id]
	if !ok {
		return nil
	}
	return &item
}

// testMedia 构造一条测试影片，覆盖 search_code 返回的全部精简字段。
func testMedia(id, code, title string) domain.Media {
	translated := "中文-" + title
	release := "2026-09-01"
	duration := 120
	poster := "https://example.test/" + code + ".jpg"
	return domain.Media{
		ID:                 id,
		Code:               code,
		Title:              title,
		TranslatedTitle:    &translated,
		ReleaseDate:        &release,
		DurationMinutes:    &duration,
		PosterURL:          &poster,
		SubscriptionStatus: domain.SubscriptionStatusNone,
		LibraryStatus:      domain.LibraryStatusAbsent,
		DisplayStatus:      domain.MediaDisplayStatusUnsubscribed,
	}
}

// newSubscriptionTestRegistry 用内存假仓储组装工具注册表，测试不接触真实数据库。
func newSubscriptionTestRegistry(repo *fakeSubscriptionRepository, catalog *fakeCatalogQueryRepository) *Registry {
	deps := Deps{
		Queries:       application.NewCatalogQueryService(catalog),
		Subscriptions: application.NewSubscriptionService(repo),
	}
	return NewRegistry(BuiltinTools(deps)...)
}

// TestAddSubscribeCreatesOneSubscriptionPerCode 验证多番号逐个受理，且兼容中文逗号与番号内空格。
func TestAddSubscribeCreatesOneSubscriptionPerCode(t *testing.T) {
	first := testMedia("m1", "SSIS-001", "First")
	second := testMedia("m2", "SSIS-002", "Second")
	catalog := &fakeCatalogQueryRepository{media: []domain.Media{first, second}}
	repo := newFakeSubscriptionRepository(first, second)
	registry := newSubscriptionTestRegistry(repo, catalog)
	ctx := context.Background()

	out := registry.Call(ctx, "add_subscribe", map[string]any{"codes": "ssis-001，SSIS 002"})
	if strings.Count(out, "已加入订阅") != 2 {
		t.Fatalf("应逐条受理两个番号，实际返回: %s", out)
	}
	if len(repo.items) != 2 {
		t.Fatalf("订阅条数=%d，期望 2", len(repo.items))
	}
	for _, item := range repo.items {
		if item.Mode != domain.SubscriptionModeStrict {
			t.Fatalf("渠道订阅必须使用 strict 模式，实际 %q", item.Mode)
		}
	}

	out = registry.Call(ctx, "add_subscribe", map[string]any{"codes": "SSIS-001,SSIS-999"})
	if !strings.Contains(out, "SSIS-999") || !strings.Contains(out, "不在媒体库中") {
		t.Fatalf("未命中番号应如实说明，实际返回: %s", out)
	}
	if len(repo.items) != 2 {
		t.Fatalf("未命中番号不应新增订阅，实际条数=%d", len(repo.items))
	}
}

// TestCancelSubscribeOnlyMatchesActiveSubscription 验证取消按有效订阅匹配，且番号先归一化。
func TestCancelSubscribeOnlyMatchesActiveSubscription(t *testing.T) {
	first := testMedia("m1", "SSIS-001", "First")
	second := testMedia("m2", "SSIS-002", "Second")
	catalog := &fakeCatalogQueryRepository{media: []domain.Media{first, second}}
	repo := newFakeSubscriptionRepository(first, second)
	repo.add("m1", domain.SubscriptionStatusActive)
	repo.add("m2", domain.SubscriptionStatusCanceled)
	registry := newSubscriptionTestRegistry(repo, catalog)
	ctx := context.Background()

	out := registry.Call(ctx, "cancel_subscribe", map[string]any{"code": "ssis 001"})
	if !strings.Contains(out, "番号 SSIS-001 的订阅已取消") {
		t.Fatalf("取消文案=%q", out)
	}
	if len(repo.items) != 1 || repo.items[0].MediaID != "m2" {
		t.Fatalf("应只删除有效订阅，剩余 %#v", repo.items)
	}

	out = registry.Call(ctx, "cancel_subscribe", map[string]any{"code": "SSIS-999"})
	if out != "番号 SSIS-999 没有有效订阅" {
		t.Fatalf("未命中取消文案=%q", out)
	}
}

// TestQuerySubscribesReturnsActiveOnly 验证订阅列表只返回有效订阅，并优先展示译文标题。
func TestQuerySubscribesReturnsActiveOnly(t *testing.T) {
	first := testMedia("m1", "SSIS-001", "First")
	second := testMedia("m2", "SSIS-002", "Second")
	catalog := &fakeCatalogQueryRepository{media: []domain.Media{first, second}}
	repo := newFakeSubscriptionRepository(first, second)
	repo.add("m1", domain.SubscriptionStatusActive)
	repo.add("m2", domain.SubscriptionStatusCanceled)
	registry := newSubscriptionTestRegistry(repo, catalog)

	out := registry.Call(context.Background(), "query_subscribes", map[string]any{})
	if !strings.Contains(out, `"code":"SSIS-001"`) || !strings.Contains(out, `"title":"中文-First"`) {
		t.Fatalf("有效订阅应包含番号与译文标题，实际返回: %s", out)
	}
	if !strings.Contains(out, `"mode":"strict"`) || !strings.Contains(out, `"total":1`) {
		t.Fatalf("订阅结果应带模式与总数，实际返回: %s", out)
	}
	if strings.Contains(out, "SSIS-002") {
		t.Fatalf("已取消订阅不应出现，实际返回: %s", out)
	}
}

// TestSearchCodeMissReturnsPlainText 验证未命中番号返回可直接展示的文案。
func TestSearchCodeMissReturnsPlainText(t *testing.T) {
	catalog := &fakeCatalogQueryRepository{media: []domain.Media{testMedia("m1", "SSIS-001", "First")}}
	registry := newSubscriptionTestRegistry(newFakeSubscriptionRepository(), catalog)
	ctx := context.Background()

	out := registry.Call(ctx, "search_code", map[string]any{"code": "ssis 999"})
	if out != "未找到番号: SSIS-999" {
		t.Fatalf("未命中文案=%q", out)
	}

	out = registry.Call(ctx, "search_code", map[string]any{"code": "ssis 001"})
	for _, fragment := range []string{`"code":"SSIS-001"`, `"title":"First"`, `"translated_title":"中文-First"`, `"release_date":"2026-09-01"`, `"duration_minutes":120`, `"poster_url":"https://example.test/SSIS-001.jpg"`, `"subscription_status":"none"`, `"library_status":"absent"`, `"display_status":"unsubscribed"`} {
		if !strings.Contains(out, fragment) {
			t.Fatalf("search_code 缺少 %s，实际返回: %s", fragment, out)
		}
	}
}

// TestGetRankFallsBackToMonthly 验证榜单周期取值收敛，非法值回落到 monthly。
func TestGetRankFallsBackToMonthly(t *testing.T) {
	catalog := &fakeCatalogQueryRepository{media: []domain.Media{testMedia("m1", "SSIS-001", "First")}}
	registry := newSubscriptionTestRegistry(newFakeSubscriptionRepository(), catalog)
	ctx := context.Background()

	registry.Call(ctx, "get_rank", map[string]any{"type": "yearly"})
	if catalog.lastRank != "monthly" {
		t.Fatalf("非法周期应回落 monthly，实际 %q", catalog.lastRank)
	}

	registry.Call(ctx, "get_rank", map[string]any{"type": "weekly"})
	if catalog.lastRank != "weekly" {
		t.Fatalf("合法周期应透传，实际 %q", catalog.lastRank)
	}
}
