package pan115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// LifeEvent 是生活事件的稳定标识；数字使用 int64，避免大 ID 经浮点转换丢失精度。
type LifeEvent struct {
	ID        int64
	Type      int
	UpdatedAt int64
}

// LifePage 是按事件 ID 倒序返回的生活事件页；Web 接口最多提供最近一万条。
type LifePage struct {
	Events []LifeEvent
	Total  int
}

// LifeCookieUserID 提取 Cookie 的账号 ID；不将 Cookie 或其内容写入错误信息。
func LifeCookieUserID(cookie string) string {
	if strings.ContainsAny(cookie, "\r\n") {
		return ""
	}
	request := &http.Request{Header: http.Header{"Cookie": []string{cookie}}}
	value, err := request.Cookie("UID")
	if err != nil {
		return ""
	}
	id := strings.SplitN(value.Value, "_", 2)[0]
	if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 {
		return ""
	}
	for _, key := range []string{"CID", "SEID"} {
		part, err := request.Cookie(key)
		if err != nil || part.Value == "" {
			return ""
		}
	}
	return id
}

// LifeEvents 使用 Cookie 拉取生活事件，与 OpenAPI Bearer 授权严格分开。
// 复用客户端限流、超时和响应边界，禁止重定向以防 Cookie 离开固定 115 入口。
func (c *Client) LifeEvents(ctx context.Context, cookie string, offset, limit int) (LifePage, error) {
	if LifeCookieUserID(cookie) == "" {
		return LifePage{}, fmt.Errorf("115 事件 Cookie 需要有效的 UID、CID、SEID")
	}
	if offset < 0 || offset >= 10000 || limit < 1 || limit > 1000 {
		return LifePage{}, fmt.Errorf("115 事件分页参数无效")
	}
	release, err := c.admit(ctx)
	if err != nil {
		return LifePage{}, err
	}
	defer release()
	endpoint := appendQuery(c.life+"/behavior/detail", url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}, "type": {""}})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return LifePage{}, err
	}
	request.Header.Set("Cookie", cookie)
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return LifePage{}, fmt.Errorf("115 事件接口网络请求失败")
	}
	body, err := readBody(response)
	if err != nil {
		return LifePage{}, err
	}
	var wire struct {
		State bool `json:"state"`
		Data  *struct {
			Count json.Number `json:"count"`
			List  []struct {
				ID        json.Number `json:"id"`
				Type      json.Number `json:"type"`
				UpdatedAt json.Number `json:"update_time"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return LifePage{}, fmt.Errorf("115 事件响应格式无效")
	}
	if !wire.State {
		return LifePage{}, fmt.Errorf("115 拒绝事件请求，请检查 Cookie 与生活事件记录是否开启")
	}
	if wire.Data == nil || wire.Data.List == nil {
		return LifePage{}, fmt.Errorf("115 事件响应缺少列表")
	}
	count, err := wire.Data.Count.Int64()
	if err != nil || count < 0 {
		return LifePage{}, fmt.Errorf("115 事件总数无效")
	}
	page := LifePage{Total: int(count), Events: make([]LifeEvent, 0, len(wire.Data.List))}
	for _, item := range wire.Data.List {
		id, idErr := item.ID.Int64()
		kind, typeErr := item.Type.Int64()
		timestamp, timeErr := item.UpdatedAt.Int64()
		if idErr != nil || typeErr != nil || timeErr != nil || id <= 0 || timestamp <= 0 {
			return LifePage{}, fmt.Errorf("115 事件字段无效")
		}
		page.Events = append(page.Events, LifeEvent{ID: id, Type: int(kind), UpdatedAt: timestamp})
	}
	return page, nil
}
