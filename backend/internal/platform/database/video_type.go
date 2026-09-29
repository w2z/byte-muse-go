package database

import (
	"context"
	"database/sql"
	"fmt"

	"bytemuse/backend/internal/domain"
)

// VideoTypeBackfillResult 汇总影片类型回填结果。
type VideoTypeBackfillResult struct {
	Scanned      int // 扫描的未分类影片数
	Classified   int // 本次写入分类的影片数
	Unclassified int // 仍无法判定、保持 NULL 的影片数
}

// BackfillVideoTypes 依据库内已有证据（旧版类别标签、番号、标题）为 media.video_type 补分类。
// 只处理 video_type IS NULL 的行，不覆盖已分类结果；没有证据且没有番号的行保持 NULL。
// 分类规则只有 domain.ClassifyVideoType 一处权威实现，采集入库与回填共用同一判定。
func BackfillVideoTypes(ctx context.Context, store Store, dialect Dialect) (VideoTypeBackfillResult, error) {
	result := VideoTypeBackfillResult{}
	db := store.SQLDB()
	rows, err := db.QueryContext(ctx, `SELECT m.id, m.code, m.title, lm.genres
		FROM media m
		LEFT JOIN legacy_media_metadata lm ON lm.media_id = m.id
		WHERE m.video_type IS NULL`)
	if err != nil {
		return result, fmt.Errorf("select unclassified media: %w", err)
	}
	type classification struct{ id, value string }
	pending := make([]classification, 0, 1024)
	for rows.Next() {
		var id, code, title string
		var genres sql.NullString
		if err := rows.Scan(&id, &code, &title, &genres); err != nil {
			rows.Close()
			return result, fmt.Errorf("scan unclassified media: %w", err)
		}
		result.Scanned++
		value := domain.ClassifyVideoType(code, title, splitRecommendationValues(genres.String))
		if value == "" {
			result.Unclassified++
			continue
		}
		pending = append(pending, classification{id: id, value: value})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, fmt.Errorf("iterate unclassified media: %w", err)
	}
	rows.Close()
	if len(pending) == 0 {
		return result, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin video type backfill: %w", err)
	}
	defer tx.Rollback()
	statement := "UPDATE media SET video_type = " + placeholder(dialect, 1) + " WHERE id = " + placeholder(dialect, 2) + " AND video_type IS NULL"
	for _, item := range pending {
		if _, err := tx.ExecContext(ctx, statement, item.value, item.id); err != nil {
			return result, fmt.Errorf("update media video type: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit video type backfill: %w", err)
	}
	result.Classified = len(pending)
	return result, nil
}
