package pan115

import (
	"context"
	"encoding/json"
	"fmt"
)

// SpaceAmount 是 115 的容量字段：Size 为字节数，Formatted 为 115 提供的展示文本。
type SpaceAmount struct {
	Size      int64
	Formatted string
}

// Space 是 115 账号的容量快照。
type Space struct {
	Total     SpaceAmount
	Used      SpaceAmount
	Remaining SpaceAmount
}

// Account 是 115 账号信息。
type Account struct {
	ID     string
	Name   string
	Avatar string
	Level  string
	Space  Space
}

// spaceAmountWire 是容量字段的传输结构。
type spaceAmountWire struct {
	Size      json.Number `json:"size"`
	Formatted string      `json:"size_format"`
}

func (w spaceAmountWire) amount() (SpaceAmount, error) {
	if w.Size == "" {
		return SpaceAmount{Formatted: w.Formatted}, nil
	}
	bytes, err := w.Size.Int64()
	if err != nil {
		return SpaceAmount{}, fmt.Errorf("解码 115 容量失败: %w", err)
	}
	return SpaceAmount{Size: bytes, Formatted: w.Formatted}, nil
}

// Account 读取账号资料与容量。
func (c *Client) Account(ctx context.Context, accessToken string) (Account, error) {
	type accountWire struct {
		ID   json.Number `json:"user_id"`
		Name string      `json:"user_name"`
		Face string      `json:"user_face_m"`
		VIP  struct {
			Level string `json:"level_name"`
		} `json:"vip_info"`
		Space struct {
			Total     spaceAmountWire `json:"all_total"`
			Used      spaceAmountWire `json:"all_use"`
			Remaining spaceAmountWire `json:"all_remain"`
		} `json:"rt_space_info"`
	}
	data, err := apiGet[accountWire](ctx, c, c.api+"/open/user/info", accessToken, nil, "账号信息")
	if err != nil {
		return Account{}, err
	}
	if data.ID.String() == "" {
		return Account{}, fmt.Errorf("115 账号响应缺少用户 ID")
	}
	total, err := data.Space.Total.amount()
	if err != nil {
		return Account{}, err
	}
	used, err := data.Space.Used.amount()
	if err != nil {
		return Account{}, err
	}
	remaining, err := data.Space.Remaining.amount()
	if err != nil {
		return Account{}, err
	}
	return Account{
		ID:     data.ID.String(),
		Name:   data.Name,
		Avatar: data.Face,
		Level:  data.VIP.Level,
		Space:  Space{Total: total, Used: used, Remaining: remaining},
	}, nil
}
