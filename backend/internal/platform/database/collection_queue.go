package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"bytemuse/backend/internal/ports"
)

// collectionQueueMigration 所有字段非空；时间用 Unix 毫秒，空错误/令牌表示无错误/未领取。
// 队列 payload 只含规范化资料；任务状态 queued/running/done/failed，租约到期允许重新领取。
func collectionQueueMigration(d Dialect) Migration {
	plan := Migration{Version: 14, Name: "create_collection_queue", Statements: []string{
		`CREATE TABLE IF NOT EXISTS collection_runs (id VARCHAR(32) PRIMARY KEY, request_json TEXT NOT NULL, source VARCHAR(32) NOT NULL, kind VARCHAR(32) NOT NULL, period VARCHAR(32) NOT NULL, crawl_state VARCHAR(16) NOT NULL, pages INTEGER NOT NULL DEFAULT 0, rank_published INTEGER NOT NULL DEFAULT 0, revision BIGINT NOT NULL DEFAULT 0, error_code VARCHAR(128) NOT NULL DEFAULT '', created_ms BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS collection_work (id VARCHAR(64) PRIMARY KEY, run_id VARCHAR(32) NOT NULL, kind VARCHAR(16) NOT NULL, page_number INTEGER NOT NULL, position_number BIGINT NOT NULL, source_id VARCHAR(128) NOT NULL, code VARCHAR(128) NOT NULL, payload_json TEXT NOT NULL, state VARCHAR(16) NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, available_ms BIGINT NOT NULL, lease_until_ms BIGINT NOT NULL DEFAULT 0, token VARCHAR(32) NOT NULL DEFAULT '', error_code VARCHAR(128) NOT NULL DEFAULT '', counts_json TEXT NOT NULL)`,
		`CREATE INDEX idx_collection_work_claim ON collection_work (kind,state,available_ms,lease_until_ms)`,
		`CREATE INDEX idx_collection_work_run ON collection_work (run_id,kind,state)`,
		`CREATE TABLE IF NOT EXISTS collection_pages (run_id VARCHAR(32) NOT NULL, page_number INTEGER NOT NULL, fingerprint VARCHAR(64) NOT NULL, PRIMARY KEY(run_id,page_number))`,
		`CREATE TABLE IF NOT EXISTS collection_rank_heads (period VARCHAR(32) PRIMARY KEY, run_id VARCHAR(32) NOT NULL, created_ms BIGINT NOT NULL)`,
	}}
	// 队列容纳坏单项，交给视频消费者独立校验，不能使整页无法入队。
	if d == DialectMySQL {
		plan.Statements[1] = strings.Replace(plan.Statements[1], "payload_json TEXT", "payload_json LONGTEXT", 1)
	}
	return plan
}

func collectionID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func (r *CollectionRepository) q(query string) string {
	if r.dialect != DialectPostgres {
		return query
	}
	n := 0
	var b strings.Builder
	for _, c := range query {
		if c == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// CreateRun 仅登记请求和第一页任务，不发起网络调用；返回值代表受理而非采集完成。
func (r *CollectionRepository) CreateRun(ctx context.Context, req ports.CollectionRequest) (ports.CollectionRun, error) {
	period := ports.CollectionRankKey(req)
	id := collectionID()
	raw, e := json.Marshal(req)
	if e != nil {
		return ports.CollectionRun{}, e
	}
	now := time.Now().UnixMilli()
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return ports.CollectionRun{}, e
	}
	defer tx.Rollback()
	if req.Kind == "rank" {
		query := `INSERT INTO collection_rank_heads(period,run_id,created_ms) VALUES(?,'',0)`
		if r.dialect == DialectMySQL {
			query += ` ON DUPLICATE KEY UPDATE period=VALUES(period)`
		} else {
			query += ` ON CONFLICT (period) DO NOTHING`
		}
		if _, e = tx.ExecContext(ctx, r.q(query), period); e != nil {
			return ports.CollectionRun{}, e
		}
		// 同周期创建串行分配严格递增时间，毫秒内同时提交也不会用随机 ID 误判新旧。
		if _, e = tx.ExecContext(ctx, r.q(`UPDATE collection_rank_heads SET period=period WHERE period=?`), period); e != nil {
			return ports.CollectionRun{}, e
		}
		var latest sql.NullInt64
		if e = tx.QueryRowContext(ctx, r.q(`SELECT MAX(created_ms) FROM collection_runs WHERE kind='rank' AND period=?`), period).Scan(&latest); e != nil {
			return ports.CollectionRun{}, e
		}
		if latest.Valid && now <= latest.Int64 {
			now = latest.Int64 + 1
		}
	}
	_, e = tx.ExecContext(ctx, r.q(`INSERT INTO collection_runs(id,request_json,source,kind,period,crawl_state,created_ms) VALUES(?,?,?,?,?,'queued',?)`), id, string(raw), req.Source, req.Kind, period, now)
	if e != nil {
		return ports.CollectionRun{}, e
	}
	if e = r.insertWork(ctx, tx, id, "page", req.Page, 0, "", "", "{}", now); e != nil {
		return ports.CollectionRun{}, e
	}
	if e = tx.Commit(); e != nil {
		return ports.CollectionRun{}, e
	}
	return r.GetRun(ctx, id)
}

func (r *CollectionRepository) insertWork(ctx context.Context, tx *sql.Tx, run, kind string, page int, position int64, sourceID, code, payload string, now int64) error {
	key := run + "/" + kind + "/" + sourceID
	if kind == "page" {
		key = fmt.Sprintf("%s/page/%d", run, page)
	}
	hash := sha256.Sum256([]byte(key))
	id := hex.EncodeToString(hash[:])
	var count int
	if e := tx.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM collection_work WHERE id=?`), id).Scan(&count); e != nil {
		return e
	}
	if count > 0 {
		return nil
	}
	_, e := tx.ExecContext(ctx, r.q(`INSERT INTO collection_work(id,run_id,kind,page_number,position_number,source_id,code,payload_json,state,available_ms,counts_json) VALUES(?,?,?,?,?,?,?,?,'queued',?,'{}')`), id, run, kind, page, position, sourceID, code, payload, now)
	return e
}

// Claim 原子比较并交换领取令牌，过期任务可恢复；阶段执行有 90 秒超时，租约为 2 分钟。
func (r *CollectionRepository) Claim(ctx context.Context, kind string, now time.Time) (*ports.CollectionWork, error) {
	var w ports.CollectionWork
	var req string
	var attempts int
	e := r.db.QueryRowContext(ctx, r.q(`SELECT w.id,w.run_id,w.kind,w.page_number,w.payload_json,w.code,w.attempts,r.request_json FROM collection_work w JOIN collection_runs r ON r.id=w.run_id WHERE w.kind=? AND ((w.state='queued' AND w.available_ms<=?) OR (w.state='running' AND w.lease_until_ms<=?)) ORDER BY r.created_ms,w.page_number,w.position_number,w.id LIMIT 1`), kind, now.UnixMilli(), now.UnixMilli()).Scan(&w.ID, &w.RunID, &w.Kind, &w.Page, &w.Payload, &w.Code, &attempts, &req)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal([]byte(req), &w.Request); e != nil {
		return nil, e
	}
	w.Token = collectionID()
	w.Attempt = attempts + 1
	result, e := r.db.ExecContext(ctx, r.q(`UPDATE collection_work SET state='running',token=?,attempts=attempts+1,lease_until_ms=? WHERE id=? AND attempts=? AND ((state='queued' AND available_ms<=?) OR (state='running' AND lease_until_ms<=?))`), w.Token, now.Add(2*time.Minute).UnixMilli(), w.ID, attempts, now.UnixMilli(), now.UnixMilli())
	if e != nil {
		return nil, e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return nil, e
	}
	if n == 0 {
		return nil, nil
	}
	return &w, nil
}

// guard 通过带令牌的更新锁住任务行，过期执行者不能写任何业务表。
func (r *CollectionRepository) guard(ctx context.Context, tx *sql.Tx, w ports.CollectionWork) error {
	res, e := tx.ExecContext(ctx, r.q(`UPDATE collection_work SET lease_until_ms=lease_until_ms+1 WHERE id=? AND token=? AND state='running' AND lease_until_ms>?`), w.ID, w.Token, time.Now().UnixMilli())
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ports.ErrCollectionLeaseLost
	}
	// 同一批次页面结束与最后一个视频提交串行，避免两者都未观察到对方完成而遗漏榜单发布。
	_, e = tx.ExecContext(ctx, r.q(`UPDATE collection_runs SET revision=revision+1 WHERE id=?`), w.RunID)
	return e
}
func (r *CollectionRepository) done(ctx context.Context, tx *sql.Tx, w ports.CollectionWork, counts ports.CollectionCounts) error {
	raw, e := json.Marshal(counts)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, r.q(`UPDATE collection_work SET state='done',token='',lease_until_ms=0,error_code='',counts_json=? WHERE id=?`), string(raw), w.ID)
	return e
}

// SavePage 原子保存本页所有视频任务和下一页任务，重复页返回错误，不能作为完成。
func (r *CollectionRepository) SavePage(ctx context.Context, w ports.CollectionWork, b ports.CollectionBatch) error {
	if len(b.Items) > 500 {
		return fmt.Errorf("page_too_large")
	}
	if b.HasMore && len(b.Items) == 0 {
		return fmt.Errorf("empty_page_with_next")
	}
	ids := []string{}
	for _, m := range b.Items {
		ids = append(ids, m.SourceID)
	}
	hash := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	fingerprint := hex.EncodeToString(hash[:])
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = r.guard(ctx, tx, w); e != nil {
		return e
	}
	var duplicates int
	if e = tx.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM collection_pages WHERE run_id=? AND fingerprint=?`), w.RunID, fingerprint).Scan(&duplicates); e != nil {
		return e
	}
	if duplicates > 0 {
		return fmt.Errorf("pagination_loop")
	}
	if _, e = tx.ExecContext(ctx, r.q(`INSERT INTO collection_pages(run_id,page_number,fingerprint) VALUES(?,?,?)`), w.RunID, w.Page, fingerprint); e != nil {
		return e
	}
	for i, m := range b.Items {
		raw, e := json.Marshal(m)
		if e != nil {
			return e
		}
		id := m.SourceID
		if id == "" {
			id = fmt.Sprintf("invalid-%d-%d", w.Page, i)
		}
		if len(id) > 128 {
			sum := sha256.Sum256([]byte(id))
			id = hex.EncodeToString(sum[:])
		}
		code := strings.ToUpper(strings.TrimSpace(m.Code))
		if len(code) > 128 {
			code = ""
		}
		if e = r.insertWork(ctx, tx, w.RunID, "video", w.Page, int64(w.Page)*1000+int64(i), id, code, string(raw), time.Now().UnixMilli()); e != nil {
			return e
		}
	}
	state := "done"
	if w.Request.Kind == "rank" && b.HasMore {
		state = "queued"
		if e = r.insertWork(ctx, tx, w.RunID, "page", w.Page+1, 0, "", "", "{}", time.Now().UnixMilli()); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, r.q(`UPDATE collection_runs SET pages=pages+1,crawl_state=?,error_code='' WHERE id=?`), state, w.RunID); e != nil {
		return e
	}
	if e = r.done(ctx, tx, w, ports.CollectionCounts{}); e != nil {
		return e
	}
	if e = r.publishRank(ctx, tx, w.RunID); e != nil {
		return e
	}
	return tx.Commit()
}

// SaveVideo 每次仅保存一个视频，并在同一事务登记缺失译文任务。
func (r *CollectionRepository) SaveVideo(ctx context.Context, w ports.CollectionWork, translate bool) error {
	var m ports.CollectedMedia
	if e := json.Unmarshal([]byte(w.Payload), &m); e != nil {
		return e
	}
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = r.guard(ctx, tx, w); e != nil {
		return e
	}
	req := w.Request
	req.Kind = "item"
	counts, e := r.saveCollectionTx(ctx, tx, req, ports.CollectionBatch{Items: []ports.CollectedMedia{m}})
	if e != nil {
		return e
	}
	if translate && w.Code != "" {
		var title sql.NullString
		e = tx.QueryRowContext(ctx, r.q(`SELECT translated_title FROM media WHERE UPPER(code)=?`), w.Code).Scan(&title)
		if e != nil {
			return e
		}
		if !title.Valid || strings.TrimSpace(title.String) == "" {
			if e = r.insertWork(ctx, tx, w.RunID, "translation", w.Page, 0, w.Code, w.Code, "{}", time.Now().UnixMilli()); e != nil {
				return e
			}
		}
	}
	if e = r.done(ctx, tx, w, counts); e != nil {
		return e
	}
	if e = r.publishRank(ctx, tx, w.RunID); e != nil {
		return e
	}
	return tx.Commit()
}

// TranslationInput 在事务外读取已保存标题；已有译文直接跳过，不覆盖人工译文。
func (r *CollectionRepository) TranslationInput(ctx context.Context, w ports.CollectionWork) (string, bool, error) {
	var title string
	var translated sql.NullString
	e := r.db.QueryRowContext(ctx, r.q(`SELECT title,translated_title FROM media WHERE UPPER(code)=?`), w.Code).Scan(&title, &translated)
	return title, translated.Valid && strings.TrimSpace(translated.String) != "", e
}

// SaveTranslation 只填补当前仍为空的译文，与任务完成一起提交。
func (r *CollectionRepository) SaveTranslation(ctx context.Context, w ports.CollectionWork, value string) error {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = r.guard(ctx, tx, w); e != nil {
		return e
	}
	if strings.TrimSpace(value) != "" {
		_, e = tx.ExecContext(ctx, r.q(`UPDATE media SET translated_title=?,updated_at=? WHERE UPPER(code)=? AND (translated_title IS NULL OR translated_title='')`), strings.TrimSpace(value), encodeTime(time.Now().UTC(), r.dialect), w.Code)
		if e != nil {
			return e
		}
	}
	if e = r.done(ctx, tx, w, ports.CollectionCounts{}); e != nil {
		return e
	}
	return tx.Commit()
}

// FailWork 最多执行三次，失败保留记录；可恢复错误按 5 秒、10 秒退避。
func (r *CollectionRepository) FailWork(ctx context.Context, w ports.CollectionWork, code string, now time.Time) error {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = r.guard(ctx, tx, w); e != nil {
		return e
	}
	state := "queued"
	if w.Attempt >= 3 {
		state = "failed"
	}
	_, e = tx.ExecContext(ctx, r.q(`UPDATE collection_work SET state=?,available_ms=?,token='',lease_until_ms=0,error_code=? WHERE id=?`), state, now.Add(time.Duration(w.Attempt)*5*time.Second).UnixMilli(), code, w.ID)
	if e != nil {
		return e
	}
	if w.Kind == "page" {
		crawl := "queued"
		if state == "failed" {
			crawl = "failed"
		}
		if _, e = tx.ExecContext(ctx, r.q(`UPDATE collection_runs SET crawl_state=?,error_code=? WHERE id=?`), crawl, code, w.RunID); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// publishRank 只发布完整且视频全部入库的非空榜；旧批次不能覆盖较新批次，视频失败不影响已提交影片。
func (r *CollectionRepository) publishRank(ctx context.Context, tx *sql.Tx, run string) error {
	var kind, period, crawl string
	var created int64
	var published int
	if e := tx.QueryRowContext(ctx, r.q(`SELECT kind,period,crawl_state,created_ms,rank_published FROM collection_runs WHERE id=?`), run).Scan(&kind, &period, &crawl, &created, &published); e != nil {
		return e
	}
	if kind != "rank" || crawl != "done" || published == 1 {
		return nil
	}
	// 先锁住对应周期的发布指针，跨批次发布也必须比较已提交的最新版本。
	if _, e := tx.ExecContext(ctx, r.q(`UPDATE collection_rank_heads SET period=period WHERE period=?`), period); e != nil {
		return e
	}
	var pending int
	if e := tx.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM collection_work WHERE run_id=? AND kind='video' AND state<>'done'`), run).Scan(&pending); e != nil {
		return e
	}
	if pending > 0 {
		return nil
	}
	rows, e := tx.QueryContext(ctx, r.q(`SELECT code FROM collection_work WHERE run_id=? AND kind='video' AND state='done' ORDER BY position_number,id`), run)
	if e != nil {
		return e
	}
	codes := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var code string
		if e = rows.Scan(&code); e != nil {
			rows.Close()
			return e
		}
		if code != "" && !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(codes) == 0 {
		return nil
	}
	var headTime int64
	var head string
	e = tx.QueryRowContext(ctx, r.q(`SELECT run_id,created_ms FROM collection_rank_heads WHERE period=?`), period).Scan(&head, &headTime)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e == nil && (headTime > created || (headTime == created && head >= run)) {
		return nil
	}
	if errors.Is(e, sql.ErrNoRows) {
		_, e = tx.ExecContext(ctx, r.q(`INSERT INTO collection_rank_heads(period,run_id,created_ms) VALUES(?,?,?)`), period, run, created)
	} else {
		_, e = tx.ExecContext(ctx, r.q(`UPDATE collection_rank_heads SET run_id=?,created_ms=? WHERE period=?`), run, created, period)
	}
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, r.q(`DELETE FROM rank_entries WHERE rank_type=?`), period); e != nil {
		return e
	}
	for i, code := range codes {
		if _, e = tx.ExecContext(ctx, r.q(`INSERT INTO rank_entries(rank_type,position,code,source_created_at) VALUES(?,?,?,?)`), period, i+1, code, encodeTime(time.Now().UTC(), r.dialect)); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, r.q(`UPDATE collection_runs SET rank_published=1 WHERE id=?`), run)
	return e
}

// GetRun 由持久化阶段记录计算进度；done 不包括仍待处理或最终失败的任务。
func (r *CollectionRepository) GetRun(ctx context.Context, id string) (ports.CollectionRun, error) {
	out := ports.CollectionRun{ID: id}
	var raw, crawl string
	var created int64
	var published int
	e := r.db.QueryRowContext(ctx, r.q(`SELECT request_json,crawl_state,pages,rank_published,error_code,created_ms FROM collection_runs WHERE id=?`), id).Scan(&raw, &crawl, &out.Pages, &published, &out.Error, &created)
	if errors.Is(e, sql.ErrNoRows) {
		return out, ports.ErrCollectionRunNotFound
	}
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal([]byte(raw), &out.Request); e != nil {
		return out, e
	}
	out.RankPublished = published == 1
	out.CrawlStatus = crawl
	out.CreatedAt = time.UnixMilli(created).UTC().Format(time.RFC3339Nano)
	rows, e := r.db.QueryContext(ctx, r.q(`SELECT kind,state,counts_json,error_code FROM collection_work WHERE run_id=?`), id)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var kind, state, counts, errorCode string
		if e = rows.Scan(&kind, &state, &counts, &errorCode); e != nil {
			return out, e
		}
		if errorCode != "" && out.Error == "" {
			out.Error = errorCode
		}
		switch kind {
		case "video":
			out.Discovered++
			switch state {
			case "done":
				out.Saved++
				var c ports.CollectionCounts
				if e = json.Unmarshal([]byte(counts), &c); e != nil {
					return out, e
				}
				out.Counts.Fetched += c.Fetched
				out.Counts.New += c.New
				out.Counts.Existing += c.Existing
				out.Counts.Inserted += c.Inserted
				out.Counts.MediaInserted += c.MediaInserted
				out.Counts.ActorsInserted += c.ActorsInserted
			case "failed":
				out.Failed++
			default:
				out.Pending++
			}
		case "translation":
			switch state {
			case "done":
				out.Translated++
			case "failed":
				out.TranslationFailed++
			default:
				out.TranslationPending++
			}
		}
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	out.Status = "running"
	if crawl == "queued" && out.Pages == 0 {
		out.Status = "queued"
	}
	if crawl == "done" && out.Pending == 0 && out.TranslationPending == 0 {
		out.Status = "completed"
		if out.Failed+out.TranslationFailed > 0 {
			out.Status = "partial_failed"
		}
	}
	if crawl == "failed" {
		out.Status = "failed"
	}
	return out, nil
}
