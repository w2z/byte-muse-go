package database

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// uncategorizedTagCategory 是采集新增标签在字典中的默认分类，与标签页展示口径一致。
const uncategorizedTagCategory = "未分类"

// tagAliasMigration 新增「来源标签 → 权威标签名」映射表，不回填、不改写历史影片资料。
// alias 为主键，canonical 指向 tag_catalog.name；created_at 为 UTC 时间文本。
// 回退时保留该表：删除会丢失已确认的翻译映射，导致重复翻译并重新产生重复标签。
func tagAliasMigration(d Dialect) Migration {
	return Migration{Version: 25, Name: "tag_aliases", Statements: []string{
		"CREATE TABLE tag_aliases (alias VARCHAR(255) NOT NULL PRIMARY KEY, canonical VARCHAR(255) NOT NULL, created_at VARCHAR(40) NOT NULL)",
		"CREATE INDEX idx_tag_aliases_canonical ON tag_aliases(canonical)",
	}}
}

// TagNameRepository 读写标签权威字典与来源别名映射，是「标签是否已存在」的唯一数据来源。
type TagNameRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewTagNameRepository 绑定已完成迁移的数据库。
func NewTagNameRepository(db *sql.DB, dialect Dialect) *TagNameRepository {
	return &TagNameRepository{db: db, dialect: dialect}
}

// Canonical 在标签字典中精确命中权威标签名。
func (r *TagNameRepository) Canonical(ctx context.Context, name string) (string, bool, error) {
	var found string
	e := r.db.QueryRowContext(ctx, "SELECT name FROM tag_catalog WHERE name="+placeholder(r.dialect, 1), name).Scan(&found)
	if e == sql.ErrNoRows {
		return "", false, nil
	}
	if e != nil {
		return "", false, e
	}
	return found, true, nil
}

// Alias 命中已登记的来源别名时返回对应权威标签名。
func (r *TagNameRepository) Alias(ctx context.Context, alias string) (string, bool, error) {
	var canonical string
	e := r.db.QueryRowContext(ctx, "SELECT canonical FROM tag_aliases WHERE alias="+placeholder(r.dialect, 1), alias).Scan(&canonical)
	if e == sql.ErrNoRows {
		return "", false, nil
	}
	if e != nil {
		return "", false, e
	}
	return canonical, true, nil
}

// Register 幂等登记权威标签名与来源写法；已存在的名称和映射都不覆盖，保证同一来源写法始终归一为同一权威名。
// alias 等于 canonical 时登记的是「该写法本身就是权威名」，归一化因此不会重复翻译同一个标签。
func (r *TagNameRepository) Register(ctx context.Context, alias, canonical string) error {
	canonical = strings.TrimSpace(canonical)
	if canonical == "" {
		return fmt.Errorf("canonical tag name is empty")
	}
	alias = strings.TrimSpace(alias)
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = insertTagCatalogIgnore(ctx, tx, r.dialect, canonical); e != nil {
		return e
	}
	if alias != "" {
		if e = insertTagAliasIgnore(ctx, tx, r.dialect, alias, canonical); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// insertTagCatalogIgnore 只在权威名缺失时写入，保留既有分类与人工维护结果。
func insertTagCatalogIgnore(ctx context.Context, tx *sql.Tx, d Dialect, name string) error {
	query := "INSERT INTO tag_catalog(name,category) VALUES(" + placeholders(d, 2, 1) + ")"
	if d == DialectMySQL {
		query = "INSERT IGNORE INTO tag_catalog(name,category) VALUES(" + placeholders(d, 2, 1) + ")"
	} else {
		query += " ON CONFLICT (name) DO NOTHING"
	}
	_, e := tx.ExecContext(ctx, query, name, uncategorizedTagCategory)
	return e
}

// insertTagAliasIgnore 只在别名首次出现时登记，保留首次确认的权威名。
func insertTagAliasIgnore(ctx context.Context, tx *sql.Tx, d Dialect, alias, canonical string) error {
	query := "INSERT INTO tag_aliases(alias,canonical,created_at) VALUES(" + placeholders(d, 3, 1) + ")"
	if d == DialectMySQL {
		query = "INSERT IGNORE INTO tag_aliases(alias,canonical,created_at) VALUES(" + placeholders(d, 3, 1) + ")"
	} else {
		query += " ON CONFLICT (alias) DO NOTHING"
	}
	_, e := tx.ExecContext(ctx, query, alias, canonical, time.Now().UTC().Format(time.RFC3339Nano))
	return e
}

// TagNormalizeResult 汇总标签归一化结果。
type TagNormalizeResult struct {
	Names         int // 库内出现过的去重标签名
	CatalogMerged int // 重命名或合并掉的字典行
	CatalogAdded  int // 补齐到字典的权威名
	GenreRows     int // 重写的影片标签行
	Rules         int // 重写的追新规则
	Matches       int // 重写的追新台账
}

// NormalizeStoredTags 把库内已有标签统一为权威简体中文名并去重。
// 名称解析由调用方注入（字典 + 翻译），本函数只负责用同一映射重写字典、影片标签、追新规则与台账，
// 保证列表、详情、追新与订阅共用同一套名称。重复执行安全：名称全部命中字典时不再翻译，也不产生写入。
func NormalizeStoredTags(ctx context.Context, store Store, dialect Dialect, resolve func(context.Context, []string) (map[string]string, error)) (TagNormalizeResult, error) {
	result := TagNormalizeResult{}
	if resolve == nil {
		return result, fmt.Errorf("tag name resolver is required")
	}
	db := store.SQLDB()
	names, catalogBefore, e := collectTagNames(ctx, db)
	if e != nil {
		return result, e
	}
	result.Names = len(names)
	if len(names) == 0 {
		return result, nil
	}
	mapping, e := resolve(ctx, names)
	if e != nil {
		return result, e
	}
	canonical := func(name string) string {
		if value, ok := mapping[name]; ok && strings.TrimSpace(value) != "" {
			return value
		}
		return name
	}
	// 权威名在解析阶段就由 TagNameService 登记进字典，这里按归一前的字典快照统计真正新增的字典条目。
	for _, name := range names {
		final := canonical(name)
		if final == "" || catalogBefore[final] {
			continue
		}
		catalogBefore[final] = true
		result.CatalogAdded++
	}
	tx, e := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if e != nil {
		return result, e
	}
	defer tx.Rollback()
	if e = rewriteTagCatalog(ctx, tx, dialect, names, canonical, &result); e != nil {
		return result, e
	}
	if e = rewriteTaggedGenres(ctx, tx, dialect, mapping, &result); e != nil {
		return result, e
	}
	if e = rewriteTagSubscriptions(ctx, tx, dialect, canonical, &result); e != nil {
		return result, e
	}
	if e = rewriteTagMatches(ctx, tx, dialect, canonical, &result); e != nil {
		return result, e
	}
	if e = tx.Commit(); e != nil {
		return result, e
	}
	return result, nil
}

// collectTagNames 汇总字典、追新规则、追新台账与影片标签中的全部名称，去重后按字典序返回。
// 同时返回归一前的字典名称集合，供调用方统计本次新增的权威标签名。
func collectTagNames(ctx context.Context, db *sql.DB) ([]string, map[string]bool, error) {
	seen := map[string]bool{}
	catalog := map[string]bool{}
	add := func(value string) {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = true
		}
	}
	for _, source := range []struct {
		query   string
		catalog bool
	}{
		{"SELECT name FROM tag_catalog", true},
		{"SELECT name FROM tag_subscriptions", false},
		{"SELECT DISTINCT tag_name FROM tag_subscription_matches", false},
	} {
		rows, e := db.QueryContext(ctx, source.query)
		if e != nil {
			return nil, nil, e
		}
		for rows.Next() {
			var name string
			if e = rows.Scan(&name); e != nil {
				rows.Close()
				return nil, nil, e
			}
			add(name)
			if value := strings.TrimSpace(name); source.catalog && value != "" {
				catalog[value] = true
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, nil, e
		}
	}
	rows, e := db.QueryContext(ctx, "SELECT genres FROM legacy_media_metadata WHERE genres IS NOT NULL AND genres<>''")
	if e != nil {
		return nil, nil, e
	}
	for rows.Next() {
		var genres string
		if e = rows.Scan(&genres); e != nil {
			rows.Close()
			return nil, nil, e
		}
		for _, tag := range splitRecommendationValues(genres) {
			add(tag)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, nil, e
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, catalog, nil
}

// rewriteTagCatalog 重命名并合并字典行：名称改变的行按权威名重建，
// 权威名缺失时以未分类补齐，保证同一权威名在字典中只存在一行。
func rewriteTagCatalog(ctx context.Context, tx *sql.Tx, dialect Dialect, names []string, canonical func(string) string, result *TagNormalizeResult) error {
	rows, e := tx.QueryContext(ctx, "SELECT name,category FROM tag_catalog")
	if e != nil {
		return e
	}
	type catalogRow struct{ name, category string }
	existing := []catalogRow{}
	type entry struct {
		category string
		exact    bool
	}
	entries := map[string]entry{}
	order := []string{}
	for rows.Next() {
		var row catalogRow
		if e = rows.Scan(&row.name, &row.category); e != nil {
			rows.Close()
			return e
		}
		existing = append(existing, row)
		final := canonical(row.name)
		exact := row.name == final
		current, ok := entries[final]
		if !ok {
			entries[final] = entry{category: row.category, exact: exact}
			order = append(order, final)
			continue
		}
		next := current
		next.exact = current.exact || exact
		switch {
		case (current.category == uncategorizedTagCategory) != (row.category == uncategorizedTagCategory):
			// 归一阶段以「未分类」自动补齐的行不能覆盖对标站字典的人工分类。
			if row.category != uncategorizedTagCategory {
				next.category = row.category
			}
		case exact && !current.exact:
			// 同为人工分类时，已经使用权威名的行优先。
			next.category = row.category
		}
		entries[final] = next
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, name := range names {
		final := canonical(name)
		if _, ok := entries[final]; ok {
			continue
		}
		entries[final] = entry{category: uncategorizedTagCategory}
		order = append(order, final)
	}
	deleteStatement := "DELETE FROM tag_catalog WHERE name=" + placeholder(dialect, 1)
	for _, row := range existing {
		if canonical(row.name) == row.name {
			continue
		}
		if _, e = tx.ExecContext(ctx, deleteStatement, row.name); e != nil {
			return e
		}
		result.CatalogMerged++
	}
	selectStatement := "SELECT category FROM tag_catalog WHERE name=" + placeholder(dialect, 1)
	insertStatement := "INSERT INTO tag_catalog(name,category) VALUES(" + placeholders(dialect, 2, 1) + ")"
	updateStatement := "UPDATE tag_catalog SET category=" + placeholder(dialect, 1) + " WHERE name=" + placeholder(dialect, 2)
	for _, name := range order {
		var category string
		e = tx.QueryRowContext(ctx, selectStatement, name).Scan(&category)
		switch {
		case e == sql.ErrNoRows:
			if _, e = tx.ExecContext(ctx, insertStatement, name, entries[name].category); e != nil {
				return e
			}
		case e != nil:
			return e
		case category != entries[name].category:
			if _, e = tx.ExecContext(ctx, updateStatement, entries[name].category, name); e != nil {
				return e
			}
		}
	}
	return nil
}

// rewriteTaggedGenres 按同一映射重写影片标签并按出现顺序去重；只在文本真正变化时写入。
func rewriteTaggedGenres(ctx context.Context, tx *sql.Tx, dialect Dialect, mapping map[string]string, result *TagNormalizeResult) error {
	rows, e := tx.QueryContext(ctx, "SELECT media_id,genres FROM legacy_media_metadata WHERE genres IS NOT NULL AND genres<>''")
	if e != nil {
		return e
	}
	type tagged struct{ id, genres string }
	items := []tagged{}
	for rows.Next() {
		var item tagged
		if e = rows.Scan(&item.id, &item.genres); e != nil {
			rows.Close()
			return e
		}
		items = append(items, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	statement := "UPDATE legacy_media_metadata SET genres=" + placeholder(dialect, 1) + " WHERE media_id=" + placeholder(dialect, 2)
	for _, item := range items {
		merged := mergeMediaTags(item.genres, nil, mapping)
		if merged == item.genres {
			continue
		}
		if _, e = tx.ExecContext(ctx, statement, merged, item.id); e != nil {
			return e
		}
		result.GenreRows++
	}
	return nil
}

// rewriteTagSubscriptions 重命名追新规则；合并到同一权威名时保留更早的有效起始日。
func rewriteTagSubscriptions(ctx context.Context, tx *sql.Tx, dialect Dialect, canonical func(string) string, result *TagNormalizeResult) error {
	rows, e := tx.QueryContext(ctx, "SELECT name,limit_date FROM tag_subscriptions")
	if e != nil {
		return e
	}
	type rule struct {
		name string
		date sql.NullString
	}
	items := []rule{}
	for rows.Next() {
		var item rule
		if e = rows.Scan(&item.name, &item.date); e != nil {
			rows.Close()
			return e
		}
		items = append(items, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	selectStatement := "SELECT limit_date FROM tag_subscriptions WHERE name=" + placeholder(dialect, 1)
	renameStatement := "UPDATE tag_subscriptions SET name=" + placeholder(dialect, 1) + " WHERE name=" + placeholder(dialect, 2)
	dateStatement := "UPDATE tag_subscriptions SET limit_date=" + placeholder(dialect, 1) + " WHERE name=" + placeholder(dialect, 2)
	deleteStatement := "DELETE FROM tag_subscriptions WHERE name=" + placeholder(dialect, 1)
	for _, item := range items {
		final := canonical(item.name)
		if final == item.name {
			continue
		}
		var current sql.NullString
		e = tx.QueryRowContext(ctx, selectStatement, final).Scan(&current)
		if e == sql.ErrNoRows {
			if _, e = tx.ExecContext(ctx, renameStatement, final, item.name); e != nil {
				return e
			}
		} else if e != nil {
			return e
		} else {
			// 同一权威名已有规则：空值表示已取消，不能用它覆盖有效起始日。
			if merged, changed := earlierLimitDate(current, item.date); changed {
				if _, e = tx.ExecContext(ctx, dateStatement, merged, final); e != nil {
					return e
				}
			}
			if _, e = tx.ExecContext(ctx, deleteStatement, item.name); e != nil {
				return e
			}
		}
		result.Rules++
	}
	return nil
}

// earlierLimitDate 合并同一标签的两条追新规则：只有更早的有效起始日才需要写回。
func earlierLimitDate(current, candidate sql.NullString) (any, bool) {
	switch {
	case !candidate.Valid:
		return nil, false
	case !current.Valid:
		return candidate.String, true
	case candidate.String < current.String:
		return candidate.String, true
	default:
		return nil, false
	}
}

// rewriteTagMatches 重命名追新台账；合并到同一权威名后按主键去重，保留首次处理时间。
func rewriteTagMatches(ctx context.Context, tx *sql.Tx, dialect Dialect, canonical func(string) string, result *TagNormalizeResult) error {
	rows, e := tx.QueryContext(ctx, "SELECT tag_name,media_id,processed_at FROM tag_subscription_matches")
	if e != nil {
		return e
	}
	type match struct{ tag, media, processed string }
	items := []match{}
	for rows.Next() {
		var item match
		if e = rows.Scan(&item.tag, &item.media, &item.processed); e != nil {
			rows.Close()
			return e
		}
		items = append(items, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	deleteStatement := "DELETE FROM tag_subscription_matches WHERE tag_name=" + placeholder(dialect, 1) + " AND media_id=" + placeholder(dialect, 2)
	insertStatement := "INSERT INTO tag_subscription_matches(tag_name,media_id,processed_at) VALUES(" + placeholders(dialect, 3, 1) + ")"
	if dialect == DialectMySQL {
		insertStatement = "INSERT IGNORE INTO tag_subscription_matches(tag_name,media_id,processed_at) VALUES(" + placeholders(dialect, 3, 1) + ")"
	} else {
		insertStatement += " ON CONFLICT (tag_name,media_id) DO NOTHING"
	}
	for _, item := range items {
		final := canonical(item.tag)
		if final == item.tag {
			continue
		}
		if _, e = tx.ExecContext(ctx, deleteStatement, item.tag, item.media); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, insertStatement, final, item.media, item.processed); e != nil {
			return e
		}
		result.Matches++
	}
	return nil
}
