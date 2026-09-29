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

// fakeActorRepository 是只服务测试的内存演员仓储。
type fakeActorRepository struct {
	items []domain.Actor
}

// List 复刻仓储的订阅状态与关键词过滤。
func (r *fakeActorRepository) List(_ context.Context, query ports.ActorListQuery) ([]domain.Actor, int, error) {
	matched := make([]domain.Actor, 0, len(r.items))
	for _, item := range r.items {
		switch query.Subscription {
		case "active":
			if item.LimitDate == nil {
				continue
			}
		case "none":
			if item.LimitDate != nil {
				continue
			}
		}
		if query.Keywords != "" && !strings.Contains(item.Name, query.Keywords) {
			continue
		}
		matched = append(matched, item)
	}
	return pageSlice(matched, query.Limit, query.Offset), len(matched), nil
}

// SaveSubscription 只更新已存在的演员，未知演员返回 ports.ErrActorNotFound。
func (r *fakeActorRepository) SaveSubscription(_ context.Context, name, limitDate string) (domain.Actor, error) {
	for index := range r.items {
		if r.items[index].Name != name {
			continue
		}
		value := limitDate
		r.items[index].LimitDate = &value
		return r.items[index], nil
	}
	return domain.Actor{}, ports.ErrActorNotFound
}

// CancelSubscription 清除追新日期，保留演员资料。
func (r *fakeActorRepository) CancelSubscription(_ context.Context, name string) (domain.Actor, error) {
	for index := range r.items {
		if r.items[index].Name != name {
			continue
		}
		r.items[index].LimitDate = nil
		return r.items[index], nil
	}
	return domain.Actor{}, ports.ErrActorNotFound
}

// ActiveNames 返回内存仓储中已订阅的演员名称。
func (r *fakeActorRepository) ActiveNames(_ context.Context) ([]string, error) {
	names := make([]string, 0, len(r.items))
	for _, item := range r.items {
		if item.LimitDate != nil {
			names = append(names, item.Name)
		}
	}
	return names, nil
}

// Follow 内存仓储不建立真实影片订阅，保持测试隔离。
func (r *fakeActorRepository) Follow(_ context.Context, _ string, _ int) (int, error) {
	return 0, nil
}

// fakeTagRepository 是只服务测试的内存标签仓储。
type fakeTagRepository struct {
	items     []ports.Tag
	followErr error
}

// ListTags 复刻仓储的名称与订阅状态过滤。
func (r *fakeTagRepository) ListTags(_ context.Context, query ports.TagListQuery) ([]ports.Tag, int, error) {
	matched := make([]ports.Tag, 0, len(r.items))
	for _, item := range r.items {
		if query.Search != "" && !strings.Contains(item.Name, query.Search) {
			continue
		}
		switch query.Subscription {
		case "active":
			if item.LimitDate == nil {
				continue
			}
		case "none":
			if item.LimitDate != nil {
				continue
			}
		}
		matched = append(matched, item)
	}
	return pageSlice(matched, query.Limit, query.Offset), len(matched), nil
}

// SaveSubscription 保存规则；未知标签返回 ports.ErrTagNotFound。
func (r *fakeTagRepository) SaveSubscription(_ context.Context, name, date string) (ports.Tag, error) {
	for index := range r.items {
		if r.items[index].Name != name {
			continue
		}
		value := date
		r.items[index].LimitDate = &value
		return r.items[index], nil
	}
	return ports.Tag{}, ports.ErrTagNotFound
}

// CancelSubscription 清除追新日期。
func (r *fakeTagRepository) CancelSubscription(_ context.Context, name string) (ports.Tag, error) {
	for index := range r.items {
		if r.items[index].Name != name {
			continue
		}
		r.items[index].LimitDate = nil
		return r.items[index], nil
	}
	return ports.Tag{}, ports.ErrTagNotFound
}

// Follow 按测试需要返回成功或异常，用于覆盖“规则已保存但追新未完成”的分支。
func (r *fakeTagRepository) Follow(context.Context, string) (int, error) {
	return 0, r.followErr
}

// SearchMedia 在测试中不返回影片。
func (r *fakeTagRepository) SearchMedia(context.Context, string, int, int) (domain.MediaPage, error) {
	return domain.MediaPage{}, nil
}

// TestActorAndTagSubscribesUseAcceptedFilters 验证演员与标签订阅列表使用的过滤值被服务层接受。
func TestActorAndTagSubscribesUseAcceptedFilters(t *testing.T) {
	limit := "2026-01-01"
	actorRepo := &fakeActorRepository{items: []domain.Actor{
		{Name: "演员甲", LimitDate: &limit},
		{Name: "演员乙"},
	}}
	tagRepo := &fakeTagRepository{items: []ports.Tag{
		{Name: "巨乳", MediaCount: 30, Category: "体型", LimitDate: &limit},
		{Name: "未订阅标签", MediaCount: 5, Category: "未分类"},
	}}
	deps := Deps{
		Actors: application.NewActorService(actorRepo),
		Tags:   application.NewTagService(tagRepo),
	}
	registry := NewRegistry(BuiltinTools(deps)...)
	ctx := context.Background()

	out := registry.Call(ctx, "query_actor_subscribes", map[string]any{})
	if !strings.Contains(out, "演员甲") || strings.Contains(out, "演员乙") {
		t.Fatalf("演员订阅列表=%s", out)
	}

	out = registry.Call(ctx, "query_tag_subscribes", map[string]any{})
	if !strings.Contains(out, "巨乳") || strings.Contains(out, "未订阅标签") {
		t.Fatalf("标签订阅列表=%s", out)
	}
}

// TestQueryTagsOrdersByMediaCount 验证标签按影片数量降序返回，便于模型确认真实标签名。
func TestQueryTagsOrdersByMediaCount(t *testing.T) {
	tagRepo := &fakeTagRepository{items: []ports.Tag{
		{Name: "少", MediaCount: 1, Category: "未分类"},
		{Name: "多", MediaCount: 99, Category: "体型"},
	}}
	registry := NewRegistry(BuiltinTools(Deps{Tags: application.NewTagService(tagRepo)})...)

	out := registry.Call(context.Background(), "query_tags", map[string]any{})
	if strings.Index(out, `"name":"多"`) > strings.Index(out, `"name":"少"`) {
		t.Fatalf("标签未按影片数量降序: %s", out)
	}
}

// TestTagSubscribeReportsPendingFollow 验证追新未完成时如实说明规则已保存，而不是报告失败。
func TestTagSubscribeReportsPendingFollow(t *testing.T) {
	tagRepo := &fakeTagRepository{
		items:     []ports.Tag{{Name: "巨乳", MediaCount: 30, Category: "体型"}},
		followErr: errors.New("追新异常"),
	}
	registry := NewRegistry(BuiltinTools(Deps{Tags: application.NewTagService(tagRepo)})...)
	ctx := context.Background()

	out := registry.Call(ctx, "add_tag_subscribe", map[string]any{"name": "巨乳", "limit_date": "2026-09-01"})
	if !strings.Contains(out, "订阅规则已保存") || strings.Contains(out, "工具执行失败") {
		t.Fatalf("追新未完成的分支返回=%s", out)
	}
	if tagRepo.items[0].LimitDate == nil || *tagRepo.items[0].LimitDate != "2026-09-01" {
		t.Fatalf("规则应已保存，实际 %#v", tagRepo.items[0].LimitDate)
	}

	out = registry.Call(ctx, "add_tag_subscribe", map[string]any{"name": "不存在", "limit_date": "2026-09-01"})
	if !strings.Contains(out, "未找到标签") {
		t.Fatalf("未知标签返回=%s", out)
	}
}
