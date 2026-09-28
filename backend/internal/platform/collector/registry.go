package collector

import (
	"context"
	"slices"
	"strings"

	"bytemuse/backend/internal/ports"
)

// Registry 分派经过能力检查的请求；Fetcher 在所有适配器之间共享限速。
type Registry struct{ fetch Fetcher }

// NewRegistry 建立首批可调用来源，不声明尚未实现的站点能力。
func NewRegistry(fetch Fetcher) *Registry { return &Registry{fetch: fetch} }

// JavDBRankPeriods 返回已实现的有码榜周期，供请求校验和全榜调度共同使用。
// 每次返回独立切片；源站其他分类及专题榜尚未接入，不在此声明。
func JavDBRankPeriods() []string { return []string{"daily", "weekly", "monthly"} }

// Sources 返回各站点已接入的查询类型。
func (r *Registry) Sources() []ports.CollectionSource {
	return []ports.CollectionSource{
		{ID: "javdb", Kinds: []string{"search", "rank"}},
		{ID: "netflav", Kinds: []string{"search", "detail"}},
	}
}

// Collect 校验单页范围，防止拼接任意地址；HTTP 200 仍须通过解析器结构检查。
func (r *Registry) Collect(ctx context.Context, req ports.CollectionRequest) (ports.CollectionBatch, error) {
	if err := r.Validate(req); err != nil {
		return ports.CollectionBatch{}, err
	}
	switch req.Source {
	case "javdb":
		return collectJavDB(ctx, r.fetch, req)
	case "netflav":
		return collectNetflav(ctx, r.fetch, req)
	}
	return ports.CollectionBatch{}, ErrUnsupported
}

// Validate 在入队前验证请求，不访问外站；完整榜单分页不设固定页数上限。
func (r *Registry) Validate(req ports.CollectionRequest) error {
	if req.Page < 1 || (req.Kind != "rank" && req.Page > 100) || len(req.Query) > 200 || strings.ContainsAny(req.Query, "\r\n\x00") {
		return ErrInvalidRequest
	}
	supported := false
	for _, source := range r.Sources() {
		if source.ID == req.Source {
			for _, kind := range source.Kinds {
				if req.Kind == kind {
					supported = true
				}
			}
		}
	}
	if !supported {
		return ErrUnsupported
	}
	if req.Kind == "rank" {
		if !slices.Contains(JavDBRankPeriods(), req.Period) {
			return ErrInvalidRequest
		}
	} else if strings.TrimSpace(req.Query) == "" {
		return ErrInvalidRequest
	}
	if req.Kind == "detail" {
		if req.Page != 1 {
			return ErrInvalidRequest
		}
		for _, c := range req.Query {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return ErrInvalidRequest
			}
		}
	}
	return nil
}
