package database

import (
	"bytemuse/backend/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ScanTaskRepository 保存扫描与生成任务；单进程服务负责同类任务的执行互斥。
type ScanTaskRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewScanTaskRepository 使用应用已迁移的数据库，不在构造时修改结构。
func NewScanTaskRepository(db *sql.DB, dialect Dialect) *ScanTaskRepository {
	return &ScanTaskRepository{db, dialect}
}

func (r *ScanTaskRepository) q(query string) string {
	return (&CollectionRepository{dialect: r.dialect}).q(query)
}

// LoadCheckpoint 按任务和稳定键读取断点，不存在表示该项尚未确认完成。
func (r *ScanTaskRepository) LoadCheckpoint(ctx context.Context, taskID, key string) ([]byte, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, r.q("SELECT payload FROM task_checkpoints WHERE task_id=? AND checkpoint_key=?"), taskID, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return []byte(raw), err
}

// SaveCheckpoint 幂等保存单项断点，避免每处理一个文件都重写整份任务快照。
func (r *ScanTaskRepository) SaveCheckpoint(ctx context.Context, taskID, key string, raw []byte) error {
	query := "INSERT INTO task_checkpoints(task_id,checkpoint_key,payload) VALUES(?,?,?) ON CONFLICT(task_id,checkpoint_key) DO UPDATE SET payload=excluded.payload"
	if r.dialect == DialectMySQL {
		query = "INSERT INTO task_checkpoints(task_id,checkpoint_key,payload) VALUES(?,?,?) ON DUPLICATE KEY UPDATE payload=VALUES(payload)"
	}
	_, err := r.db.ExecContext(ctx, r.q(query), taskID, key, string(raw))
	return err
}

// Save 原子替换整个快照，防止状态与进度来自不同时间点。
func (r *ScanTaskRepository) Save(ctx context.Context, task domain.ScanTask) error {
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	query := "INSERT INTO scan_tasks(id,kind,state,snapshot,created_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,snapshot=excluded.snapshot"
	if r.dialect == DialectMySQL {
		query = "INSERT INTO scan_tasks(id,kind,state,snapshot,created_at) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE state=VALUES(state),snapshot=VALUES(snapshot)"
	}
	_, err = r.db.ExecContext(ctx, r.q(query), task.ID, task.Kind, task.State, string(raw), task.CreatedAt)
	return err
}

// Latest 返回某类任务最后一次持久化快照；两类任务独立查询。
func (r *ScanTaskRepository) Latest(ctx context.Context, kind string) (*domain.ScanTask, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, r.q("SELECT snapshot FROM scan_tasks WHERE kind=? ORDER BY created_at DESC,id DESC LIMIT 1"), kind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var task domain.ScanTask
	err = json.Unmarshal([]byte(raw), &task)
	return &task, err
}

// Get 按任务 ID 读取快照，避免旧同步请求被后续新任务替换。
func (r *ScanTaskRepository) Get(ctx context.Context, id string) (*domain.ScanTask, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, r.q("SELECT snapshot FROM scan_tasks WHERE id=?"), id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var task domain.ScanTask
	err = json.Unmarshal([]byte(raw), &task)
	return &task, err
}

// Interrupt 收敛退出状态并保留用户暂停和取消意图；运行中任务由服务按断点恢复。
func (r *ScanTaskRepository) Interrupt(ctx context.Context) error {
	rows, err := r.db.QueryContext(ctx, "SELECT snapshot FROM scan_tasks WHERE state IN ('running','pausing','paused','canceling')")
	if err != nil {
		return err
	}
	var tasks []domain.ScanTask
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var task domain.ScanTask
		if err = json.Unmarshal([]byte(raw), &task); err != nil {
			rows.Close()
			return err
		}
		tasks = append(tasks, task)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, task := range tasks {
		switch task.State {
		case "paused", "pausing":
			task.State = "paused"
		case "canceling":
			task.State = "canceled"
		default:
			task.State = "interrupted"
		}
		var exists int
		if err := r.db.QueryRowContext(ctx, r.q("SELECT COUNT(*) FROM task_checkpoints WHERE task_id=?"), task.ID).Scan(&exists); err != nil {
			return err
		}
		task.CanRetry = exists > 0
		task.Error = ""
		if task.State == "interrupted" {
			task.Error = "服务重启，任务已中断，请重新启动"
			if task.CanRetry {
				task.Error = "服务重启，等待从断点恢复"
			}
		} else if task.State == "paused" && !task.CanRetry {
			task.State = "interrupted"
			task.Error = "历史暂停任务缺少断点，请重新启动"
		}
		task.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err = r.Save(ctx, task); err != nil {
			return err
		}
	}
	return nil
}
