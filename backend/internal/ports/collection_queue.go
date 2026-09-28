package ports

import (
	"context"
	"errors"
	"time"
)

// ErrCollectionRunNotFound 表示未找到持久化采集批次。
var ErrCollectionRunNotFound = errors.New("collection_run_not_found")

// ErrCollectionLeaseLost 阻止过期消费者继续提交业务数据。
var ErrCollectionLeaseLost = errors.New("collection_lease_lost")

// CollectionRun 分别公开采集、入库和翻译状态，不把排队视为成功。
type CollectionRun struct {
	ID                 string            `json:"run_id"`
	Request            CollectionRequest `json:"request"`
	Status             string            `json:"status"`
	CrawlStatus        string            `json:"crawl_status"`
	Pages              int               `json:"pages"`
	Discovered         int               `json:"discovered"`
	Saved              int               `json:"saved"`
	Failed             int               `json:"failed"`
	Pending            int               `json:"pending"`
	Translated         int               `json:"translated"`
	TranslationFailed  int               `json:"translation_failed"`
	TranslationPending int               `json:"translation_pending"`
	RankPublished      bool              `json:"rank_published"`
	Error              string            `json:"error,omitempty"`
	Counts             CollectionCounts  `json:"counts"`
	CreatedAt          string            `json:"created_at"`
}

// CollectionWork 是带租约令牌的持久化阶段任务；错误信息不保存上游正文。
type CollectionWork struct {
	ID, RunID, Kind, Token, Payload, Code string
	Page, Attempt                         int
	Request                               CollectionRequest
}

// CollectionQueue 的每个提交方法校验领取令牌；业务写入与任务完成原子提交。
type CollectionQueue interface {
	CreateRun(context.Context, CollectionRequest) (CollectionRun, error)
	GetRun(context.Context, string) (CollectionRun, error)
	Claim(context.Context, string, time.Time) (*CollectionWork, error)
	SavePage(context.Context, CollectionWork, CollectionBatch) error
	SaveVideo(context.Context, CollectionWork, bool) error
	TranslationInput(context.Context, CollectionWork) (string, bool, error)
	SaveTranslation(context.Context, CollectionWork, string) error
	FailWork(context.Context, CollectionWork, string, time.Time) error
}
