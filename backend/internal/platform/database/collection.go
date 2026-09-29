package database

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// collectionMigration 新增来源快照，不回填或重算既有媒体。所有字段非空；空 code 表示无法可靠关联番号。
// source/source_id 为联合主键；payload_json 为已解析元数据，不保存原始页面、凭据或播放地址。
func collectionMigration(d Dialect) Migration {
	timestamp := "TEXT"
	if d == DialectPostgres {
		timestamp = "TIMESTAMPTZ"
	}
	if d == DialectMySQL {
		timestamp = "DATETIME(6)"
	}
	return Migration{Version: 13, Name: "create_collection_records", Statements: []string{fmt.Sprintf(`CREATE TABLE IF NOT EXISTS collection_records (source VARCHAR(32) NOT NULL, source_id VARCHAR(128) NOT NULL, code VARCHAR(128) NOT NULL DEFAULT '', payload_json TEXT NOT NULL, collected_at %s NOT NULL, PRIMARY KEY(source, source_id))`, timestamp)}}
}

// CollectionRepository 原子保存来源快照及新目录实体，按番号追加标签但不更改已有业务状态。
type CollectionRepository struct {
	db      *sql.DB
	dialect Dialect
	tags    ports.TagNameResolver
}

// NewCollectionRepository 绑定已完成迁移的数据库。
func NewCollectionRepository(db *sql.DB, dialect Dialect) *CollectionRepository {
	return &CollectionRepository{db: db, dialect: dialect}
}

// SetTagResolver 装配标签归一化入口；未装配时按原样写入标签，保持未启用翻译时的既有行为。
func (r *CollectionRepository) SetTagResolver(resolver ports.TagNameResolver) { r.tags = resolver }

// resolveTags 在事务外统一本次采集的标签名。
// 翻译与字典登记都会访问数据库或外部引擎，不能在持有写事务时执行，否则会长时间占用唯一写锁。
func (r *CollectionRepository) resolveTags(ctx context.Context, b ports.CollectionBatch) (map[string]string, error) {
	if r.tags == nil {
		return nil, nil
	}
	names := []string{}
	for _, item := range b.Items {
		for _, tag := range item.Tags {
			names = append(names, splitRecommendationValues(tag)...)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	return r.tags.Resolve(ctx, names)
}

// SaveCollection 在同一事务内去重、建档、更新来源快照；任一步失败全部回滚。
func (r *CollectionRepository) SaveCollection(ctx context.Context, req ports.CollectionRequest, b ports.CollectionBatch) (ports.CollectionCounts, error) {
	mapping, e := r.resolveTags(ctx, b)
	if e != nil {
		return ports.CollectionCounts{}, e
	}
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if e != nil {
		return ports.CollectionCounts{}, e
	}
	defer tx.Rollback()
	counts, e := r.saveCollectionTx(ctx, tx, req, b, mapping)
	if e != nil {
		return ports.CollectionCounts{}, e
	}
	if e = tx.Commit(); e != nil {
		return ports.CollectionCounts{}, e
	}
	return counts, nil
}

// saveCollectionTx 为同步兼容调用与队列逐项处理提供唯一资料写入规则。
func (r *CollectionRepository) saveCollectionTx(ctx context.Context, tx *sql.Tx, req ports.CollectionRequest, b ports.CollectionBatch, mapping map[string]string) (ports.CollectionCounts, error) {
	empty := ports.CollectionCounts{}
	if !ports.ValidCollectionSource(req.Source) {
		return empty, fmt.Errorf("invalid collection source")
	}
	if len(b.Items) > 500 || len(b.Actors) > 500 {
		return empty, fmt.Errorf("collection too large")
	}
	if req.Kind == "rank" && !slices.Contains(ports.CollectionRankPeriods(req.Source), req.Period) {
		return empty, fmt.Errorf("invalid rank")
	}
	var e error
	now := encodeTime(time.Now().UTC(), r.dialect)
	counts := ports.CollectionCounts{}
	seen := map[string]bool{}
	actors := append([]ports.CollectedActor{}, b.Actors...)
	for _, m := range b.Items {
		if m.VideoType != "" && !domain.ValidVideoType(m.VideoType) {
			return empty, fmt.Errorf("invalid video type")
		}
		if strings.TrimSpace(m.SourceID) == "" || len(m.SourceID) > 128 || strings.TrimSpace(m.Title) == "" || utf8.RuneCountInString(m.Title) > 512 || len(m.Code) > 128 || len(m.PosterURL) > 2048 || m.DurationMinutes < 0 || len(m.Actors) > 500 || len(m.Tags) > 500 || !strings.HasPrefix(m.URL, "https://") {
			return empty, fmt.Errorf("invalid collected media")
		}
		if m.ReleaseDate != "" {
			if _, e = time.Parse("2006-01-02", m.ReleaseDate); e != nil {
				return empty, fmt.Errorf("invalid release date")
			}
		}
		if seen[m.SourceID] {
			continue
		}
		seen[m.SourceID] = true
		counts.Fetched++
		payload, e := json.Marshal(m)
		if e != nil {
			return empty, e
		}
		if len(payload) > 65535 {
			return empty, fmt.Errorf("collection record too large")
		}
		var existing int
		e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM collection_records WHERE source = "+placeholder(r.dialect, 1)+" AND source_id = "+placeholder(r.dialect, 2), req.Source, m.SourceID).Scan(&existing)
		if e != nil {
			return empty, e
		}
		if existing > 0 {
			counts.Existing++
			_, e = tx.ExecContext(ctx, "UPDATE collection_records SET code = "+placeholder(r.dialect, 1)+", payload_json = "+placeholder(r.dialect, 2)+", collected_at = "+placeholder(r.dialect, 3)+" WHERE source = "+placeholder(r.dialect, 4)+" AND source_id = "+placeholder(r.dialect, 5), m.Code, string(payload), now, req.Source, m.SourceID)
		} else {
			counts.New++
			_, e = tx.ExecContext(ctx, "INSERT INTO collection_records (source,source_id,code,payload_json,collected_at) VALUES ("+placeholders(r.dialect, 5, 1)+")", req.Source, m.SourceID, m.Code, string(payload), now)
			counts.Inserted++
		}
		if e != nil {
			return empty, e
		}
		actors = append(actors, m.Actors...)
		code := strings.ToUpper(strings.TrimSpace(m.Code))
		if code == "" {
			continue
		}
		var id string
		e = tx.QueryRowContext(ctx, "SELECT id FROM media WHERE UPPER(code) = "+placeholder(r.dialect, 1), code).Scan(&id)
		if e != nil && e != sql.ErrNoRows {
			return empty, e
		}
		if e == nil {
			if e = r.mergeMediaTagsTx(ctx, tx, id, code, m.Tags, mapping); e != nil {
				return empty, e
			}
			continue
		}
		id = legacyStableID("media", code)
		// 采集契约中 0 表示未提供时长，数据库使用 NULL 避免伪造零分钟。
		var duration any
		if m.DurationMinutes > 0 {
			duration = m.DurationMinutes
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO media (id,code,title,poster_url,release_date,duration_minutes,subscription_status,library_status,created_at,updated_at,video_type) VALUES ("+placeholders(r.dialect, 11, 1)+")", id, code, m.Title, nullIfEmpty(m.PosterURL), nullIfEmpty(m.ReleaseDate), duration, "none", "unknown", now, now, nullIfEmpty(m.VideoType))
		if e != nil {
			return empty, e
		}
		names := []string{}
		for _, a := range m.Actors {
			names = append(names, a.Name)
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO legacy_media_metadata (media_id,code,genres,casts,legacy_status,legacy_mode) VALUES ("+placeholders(r.dialect, 6, 1)+")", id, code, mergeMediaTags("", m.Tags, mapping), strings.Join(names, ","), "UN_SUBSCRIBE", "STRICT")
		if e != nil {
			return empty, e
		}
		counts.MediaInserted++
	}
	seenActors := map[string]bool{}
	for _, a := range actors {
		a.Name = strings.TrimSpace(a.Name)
		if a.Name == "" || utf8.RuneCountInString(a.Name) > 255 || len(a.Photo) > 2048 {
			return empty, fmt.Errorf("invalid actor")
		}
		if seenActors[a.Name] {
			continue
		}
		seenActors[a.Name] = true
		var n int
		if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM actors WHERE name = "+placeholder(r.dialect, 1), a.Name).Scan(&n); e != nil {
			return empty, e
		}
		if n > 0 {
			continue
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO actors (name,photo,created_at,updated_at) VALUES ("+placeholders(r.dialect, 4, 1)+")", a.Name, nullIfEmpty(a.Photo), now, now); e != nil {
			return empty, e
		}
		counts.ActorsInserted++
	}
	return counts, nil
}

// mergeMediaTagsTx 仅补本次命中影片的标签；与来源快照共用可串行化事务，避免并发覆盖。
// 缺失兼容资料行时只写标签，空旧状态表示未知，不伪造影片的历史订阅状态。
func (r *CollectionRepository) mergeMediaTagsTx(ctx context.Context, tx *sql.Tx, id, code string, tags []string, mapping map[string]string) error {
	if mergeMediaTags("", tags, mapping) == "" {
		return nil
	}
	var old sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT genres FROM legacy_media_metadata WHERE media_id = "+placeholder(r.dialect, 1), id).Scan(&old)
	if err == sql.ErrNoRows {
		_, err = tx.ExecContext(ctx, "INSERT INTO legacy_media_metadata (media_id,code,genres,legacy_status,legacy_mode) VALUES ("+placeholders(r.dialect, 5, 1)+")", id, code, mergeMediaTags("", tags, mapping), "", "")
		return err
	}
	if err != nil {
		return err
	}
	merged := mergeMediaTags(old.String, tags, mapping)
	if merged == old.String {
		return nil
	}
	_, err = tx.ExecContext(ctx, "UPDATE legacy_media_metadata SET genres = "+placeholder(r.dialect, 1)+" WHERE media_id = "+placeholder(r.dialect, 2), merged, id)
	return err
}

// mergeMediaTags 按现有逗号分隔格式去空、去重并追加标签，保留出现顺序。
// mapping 为 nil 时保留人工已有文本原文，只追加新标签；有映射时先把已有文本与本次标签统一到权威名，
// 让同一标签的不同写法在写入时即合并，避免重复标签继续入库。来源快照始终保留适配器给出的标签数组。
func mergeMediaTags(existing string, tags []string, mapping map[string]string) string {
	result := existing
	if mapping != nil {
		result = strings.Join(canonicalTagList(existing, mapping), ",")
	}
	seen := map[string]bool{}
	for _, tag := range splitRecommendationValues(result) {
		seen[tag] = true
	}
	for _, value := range tags {
		for _, tag := range splitRecommendationValues(value) {
			name := canonicalTagName(tag, mapping)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			if result != "" {
				result += ","
			}
			result += name
		}
	}
	return result
}

// canonicalTagList 把已有标签文本按权威名重写并去重，保留原始出现顺序。
func canonicalTagList(existing string, mapping map[string]string) []string {
	seen := map[string]bool{}
	values := []string{}
	for _, tag := range splitRecommendationValues(existing) {
		name := canonicalTagName(tag, mapping)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		values = append(values, name)
	}
	return values
}

// canonicalTagName 返回标签的权威名；字典未覆盖时保留原值，不猜测跨语言别名。
func canonicalTagName(tag string, mapping map[string]string) string {
	if name, ok := mapping[tag]; ok && strings.TrimSpace(name) != "" {
		return name
	}
	return tag
}
