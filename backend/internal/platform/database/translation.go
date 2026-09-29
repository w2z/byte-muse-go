package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// TranslationCleanupResult 汇总译文清理结果。
type TranslationCleanupResult struct {
	Scanned int // 已有译文的影片数
	Reset   int // 判定无效、置回 NULL 的影片数
}

// TranslationFillResult 汇总译文补全结果。
type TranslationFillResult struct {
	Attempted  int
	Translated int
	Failed     int
}

// ResetInvalidTranslations 清除不符合翻译规范的 translated_title（提示词回显、拒答、夹带说明），
// 置回 NULL 以便重新翻译。校验规则由调用方注入，保证与入库校验共用同一权威实现。
func ResetInvalidTranslations(ctx context.Context, store Store, dialect Dialect, valid func(title, translated string) bool) (TranslationCleanupResult, error) {
	result := TranslationCleanupResult{}
	db := store.SQLDB()
	rows, err := db.QueryContext(ctx, `SELECT id, title, translated_title FROM media
		WHERE translated_title IS NOT NULL AND TRIM(translated_title) <> ''`)
	if err != nil {
		return result, fmt.Errorf("select translated media: %w", err)
	}
	invalid := make([]string, 0, 64)
	for rows.Next() {
		var id, title, translated string
		if err := rows.Scan(&id, &title, &translated); err != nil {
			rows.Close()
			return result, fmt.Errorf("scan translated media: %w", err)
		}
		result.Scanned++
		if !valid(title, translated) {
			invalid = append(invalid, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, fmt.Errorf("iterate translated media: %w", err)
	}
	rows.Close()
	if len(invalid) == 0 {
		return result, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin translation cleanup: %w", err)
	}
	defer tx.Rollback()
	statement := "UPDATE media SET translated_title = NULL WHERE id = " + placeholder(dialect, 1)
	for _, id := range invalid {
		if _, err := tx.ExecContext(ctx, statement, id); err != nil {
			return result, fmt.Errorf("reset translated title: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit translation cleanup: %w", err)
	}
	result.Reset = len(invalid)
	return result, nil
}

// BackfillTranslations 为 translated_title 为空的影片补翻译。单条失败只跳过该影片，其余继续；
// 失败影片保持 NULL，可由懒翻译或下次执行重试。limit 小于等于 0 表示不限制数量。
func BackfillTranslations(ctx context.Context, store Store, dialect Dialect, limit int, translate func(context.Context, string) (string, error)) (TranslationFillResult, error) {
	result := TranslationFillResult{}
	if translate == nil {
		return result, fmt.Errorf("translation callback is required")
	}
	db := store.SQLDB()
	query := `SELECT id, title FROM media WHERE translated_title IS NULL OR TRIM(translated_title) = '' ORDER BY updated_at DESC, id`
	args := []any{}
	if limit > 0 {
		query += " LIMIT " + placeholder(dialect, 1)
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("select untranslated media: %w", err)
	}
	type pending struct{ id, title string }
	items := make([]pending, 0, 256)
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.title); err != nil {
			rows.Close()
			return result, fmt.Errorf("scan untranslated media: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, fmt.Errorf("iterate untranslated media: %w", err)
	}
	rows.Close()
	statement := "UPDATE media SET translated_title = " + placeholder(dialect, 1) + " WHERE id = " + placeholder(dialect, 2)
	for _, item := range items {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Attempted++
		value, err := translate(ctx, item.title)
		if err != nil {
			result.Failed++
			continue
		}
		if err := saveTranslationWithRetry(ctx, db, statement, value, item.id); err != nil {
			return result, fmt.Errorf("save translated title: %w", err)
		}
		result.Translated++
	}
	return result, nil
}

// translationWriteAttempts 是译文写入遇到写锁竞争时的最大尝试次数（含首次）。
const translationWriteAttempts = 6

// saveTranslationWithRetry 在瞬时写锁竞争时按 0.5s 递增退避重试。
// 在线服务与补全命令共用同一 SQLite 文件，采集写入会让单次 UPDATE 立即返回 SQLITE_BUSY，
// 不做重试会导致整批补全中断并丢失已尝试进度。
func saveTranslationWithRetry(ctx context.Context, db *sql.DB, statement, value, id string) error {
	var err error
	for attempt := 0; attempt < translationWriteAttempts; attempt++ {
		if _, err = db.ExecContext(ctx, statement, value, id); err == nil {
			return nil
		}
		if !isWriteConflict(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
		}
	}
	return err
}

// isWriteConflict 判定各方言可重试的写锁竞争错误，判定风格与既有驱动错误识别保持一致。
func isWriteConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"database is locked",
		"database table is locked",
		"database is busy",
		"sqlite_busy",
		"deadlock found",
		"lock wait timeout",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
