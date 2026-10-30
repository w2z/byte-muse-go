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
		if strings.TrimSpace(item.Plot) != "" {
			result, updateErr := tx.ExecContext(ctx, "UPDATE legacy_media_metadata SET plot = COALESCE(NULLIF(plot,''), "+placeholder(r.dialect, 1)+") WHERE media_id = "+placeholder(r.dialect, 2), strings.TrimSpace(item.Plot), id)
			err = updateErr
			if err == nil {
				if affected, rowsErr := result.RowsAffected(); rowsErr == nil && affected == 0 {
					_, err = tx.ExecContext(ctx, "INSERT INTO legacy_media_metadata (media_id,code,plot,legacy_status,legacy_mode) VALUES ("+placeholders(r.dialect, 5, 1)+")", id, code, strings.TrimSpace(item.Plot), "UN_SUBSCRIBE", "STRICT")
				}
			}
			if err != nil {
				return 0, fmt.Errorf("save media library plot %q: %w", code, err)
			}
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

// UpdateLibraryMetadata 补齐 NFO 提供的资料；仅命中文件名兜底标题或空简介，保持人工资料不被扫描覆盖。
func (r *sqlMediaLibraryRepository) UpdateLibraryMetadata(ctx context.Context, code, title, plot string) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	title = truncateMediaTitle(strings.TrimSpace(title))
	plot = strings.TrimSpace(plot)
	if code == "" || (title == "" && plot == "") {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id, oldTitle string
	var oldTranslated sql.NullString
	if err = tx.QueryRowContext(ctx, "SELECT id,title,translated_title FROM media WHERE UPPER(code) = "+placeholder(r.dialect, 1), code).Scan(&id, &oldTitle, &oldTranslated); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil }
		return err
	}
	set := []string{}
	args := []any{}
	if title != "" && isLibraryFilenameTitle(code, oldTitle) {
		set = append(set, "title = "+placeholder(r.dialect, len(args)+1)); args = append(args, title)
		if strings.EqualFold(strings.TrimSpace(oldTranslated.String), strings.TrimSpace(oldTitle)) {
			set = append(set, "translated_title = NULL")
		}
	}
	if len(set) > 0 {
		set = append(set, "updated_at = "+placeholder(r.dialect, len(args)+1)); args = append(args, encodeTime(time.Now().UTC(), r.dialect))
		args = append(args, id)
		if _, err = tx.ExecContext(ctx, "UPDATE media SET "+strings.Join(set, ", ")+" WHERE id = "+placeholder(r.dialect, len(args)), args...); err != nil { return err }
	}
	if plot != "" {
		result, updateErr := tx.ExecContext(ctx, "UPDATE legacy_media_metadata SET plot = COALESCE(NULLIF(plot,''), "+placeholder(r.dialect, 1)+") WHERE media_id = "+placeholder(r.dialect, 2), plot, id)
		err = updateErr
		if err == nil {
			if affected, rowsErr := result.RowsAffected(); rowsErr == nil && affected == 0 {
				_, err = tx.ExecContext(ctx, "INSERT INTO legacy_media_metadata (media_id,code,plot,legacy_status,legacy_mode) VALUES ("+placeholders(r.dialect, 5, 1)+")", id, code, plot, "UN_SUBSCRIBE", "STRICT")
			}
		}
		if err != nil { return err }
	}
	return tx.Commit()
}

func isLibraryFilenameTitle(code, title string) bool {
	value := strings.TrimSpace(title)
	return strings.EqualFold(value, code) || (len(value) > len(code) && strings.EqualFold(value[:len(code)], code) && strings.ContainsAny(value[len(code):len(code)+1], "-_ ."))
}

// truncateMediaTitle 按 Unicode 码点截断标题，保证不超过 media.title 的列长度。
func truncateMediaTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= mediaTitleMaxRunes {
		return title
	}
	return string(runes[:mediaTitleMaxRunes])
}
