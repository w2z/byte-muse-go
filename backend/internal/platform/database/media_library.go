package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// mediaTitleMaxRunes 是 media.title 在全部方言下的最小长度（MySQL VARCHAR(512)）。
// 扫描入库的标题只是文件名兜底展示，超长按码点截断，避免个别长文件名导致整个目录写入失败。
const mediaTitleMaxRunes = 512

// sqlMediaLibraryRepository 把「115 扫描目录」发现的影片登记到媒体库。
// 它只负责媒体库状态这一件事，不参与采集、订阅与下载链路的字段维护。
type sqlMediaLibraryRepository struct {
	dialect Dialect
	db      *sql.DB
}

// MarkLibraryPresent 幂等登记一批已在媒体库中的影片，返回本次新建的 media 行数。
//
// 番号按 UPPER(code) 匹配，与采集入库口径一致，避免大小写差异重复建行；
// 命中已有行时只更新 library_status 与 updated_at，绝不覆盖标题、译文、封面、订阅状态等字段，
// 使重复扫描与人工编辑互不破坏。整批写入在一个事务内完成，任一行失败即整批回滚。
func (r *sqlMediaLibraryRepository) MarkLibraryPresent(ctx context.Context, items []ports.LibraryMediaItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin media library mark: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	now := encodeTime(time.Now().UTC(), r.dialect)
	created := 0
	for _, item := range items {
		code := strings.ToUpper(strings.TrimSpace(item.Code))
		if code == "" {
			continue
		}
		title := truncateMediaTitle(strings.TrimSpace(item.Title))
		if title == "" {
			title = code
		}
		var id string
		err := tx.QueryRowContext(ctx, "SELECT id FROM media WHERE UPPER(code) = "+placeholder(r.dialect, 1), code).Scan(&id)
		switch {
		case err == nil:
			_, err = tx.ExecContext(ctx, "UPDATE media SET library_status = "+placeholder(r.dialect, 1)+", updated_at = "+placeholder(r.dialect, 2)+" WHERE id = "+placeholder(r.dialect, 3), string(domain.LibraryStatusPresent), now, id)
		case errors.Is(err, sql.ErrNoRows):
			id = legacyStableID("media", code)
			_, err = tx.ExecContext(ctx, "INSERT INTO media (id,code,title,subscription_status,library_status,created_at,updated_at,video_type) VALUES ("+placeholders(r.dialect, 8, 1)+")", id, code, title, string(domain.SubscriptionStatusNone), string(domain.LibraryStatusPresent), now, now, nullIfEmpty(item.VideoType))
			created++
		}
		if err != nil {
			return 0, fmt.Errorf("mark media library %q: %w", code, err)
		}
		if item.Source != nil {
			if err = saveLibrarySource(ctx, tx, r.dialect, id, *item.Source); err != nil {
				return 0, err
			}
		}
		for _, source := range item.Sources {
			if err = saveLibrarySource(ctx, tx, r.dialect, id, source); err != nil {
				return 0, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit media library mark: %w", err)
	}
	committed = true
	return created, nil
}

// truncateMediaTitle 按 Unicode 码点截断标题，保证不超过 media.title 的列长度。
func truncateMediaTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= mediaTitleMaxRunes {
		return title
	}
	return string(runes[:mediaTitleMaxRunes])
}
