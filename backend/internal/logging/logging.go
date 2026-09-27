// Package logging provides one structured logging vocabulary for backend workflows.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Category is the stable business category attached to structured process logs.
type Category string

const (
	CategoryCollection   Category = "采集同步"
	CategorySubscription Category = "订阅查询"
	CategoryDownload     Category = "下载"
	CategoryMedia        Category = "媒体库"
	CategoryNotification Category = "通知"
	CategorySystem       Category = "系统"
	CategoryAgent        Category = "Agent"
	CategoryOther        Category = "其他"
)

// MaxPageSize 是日志列表允许的最大单页条数。日志页面按 100 递增，最多读取 500 条。
const MaxPageSize = 500

// Valid reports whether category belongs to the public log category vocabulary.
func (c Category) Valid() bool {
	switch c {
	case CategoryCollection, CategorySubscription, CategoryDownload, CategoryMedia, CategoryNotification, CategorySystem, CategoryAgent, CategoryOther:
		return true
	default:
		return false
	}
}

// Level is the stable severity vocabulary shared with the management log page.
type Level string

const (
	LevelDebug   Level = "debug"
	LevelInfo    Level = "info"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

// Valid reports whether level belongs to the public level vocabulary.
func (l Level) Valid() bool {
	switch l {
	case LevelDebug, LevelInfo, LevelWarning, LevelError:
		return true
	default:
		return false
	}
}

// Record is one process log entry as shown by the management log page.
type Record struct {
	Time     time.Time      `json:"time"`
	Level    Level          `json:"level"`
	Category Category       `json:"category"`
	Message  string         `json:"message"`
	Attrs    map[string]any `json:"attrs,omitempty"`
}

// Query selects and paginates buffered records. Zero fields do not filter.
type Query struct {
	Level     Level
	Category  Category
	Keyword   string
	StartTime *time.Time
	EndTime   *time.Time
	Page      int
	PageSize  int
}

// Store is the persistent source of truth for management logs.
type Store interface {
	Append(ctx context.Context, record Record) error
	Search(ctx context.Context, query Query) ([]Record, int, error)
	Clear(ctx context.Context, query Query) (int, error)
	DeleteBefore(ctx context.Context, before time.Time) (int, error)
}

// Logger writes structured process output and persists management logs when a store is bound.
type Logger struct {
	logger *slog.Logger
	mu     sync.RWMutex
	store  Store
}

// Default is the process logger used by HTTP and background entry points.
var Default = New(os.Stderr)

// New creates a logger writing structured records to output. A nil output uses stderr.
func New(output io.Writer) *Logger {
	if output == nil {
		output = os.Stderr
	}
	return &Logger{logger: slog.New(slog.NewJSONHandler(output, nil))}
}

// SetStore replaces the persistent log store after database migrations complete.
func (l *Logger) SetStore(store Store) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.store = store
	l.mu.Unlock()
}

// Info records a normal business event.
func (l *Logger) Info(category Category, message string, attrs ...any) {
	l.log(slog.LevelInfo, category, message, attrs...)
}

// Error records a failed business event.
func (l *Logger) Error(category Category, message string, attrs ...any) {
	l.log(slog.LevelError, category, message, attrs...)
}

// Info writes through the process default logger.
func Info(category Category, message string, attrs ...any) { Default.Info(category, message, attrs...) }

// Error writes through the process default logger.
func Error(category Category, message string, attrs ...any) {
	Default.Error(category, message, attrs...)
}

// Search returns the records matching query, newest first, together with the filtered total
// so the caller can paginate without reading the whole buffer again.
func (l *Logger) Search(ctx context.Context, query Query) ([]Record, int, error) {
	if l == nil {
		return []Record{}, 0, nil
	}
	l.mu.RLock()
	store := l.store
	l.mu.RUnlock()
	if store == nil {
		return []Record{}, 0, nil
	}
	return store.Search(ctx, query)
}

// Clear permanently deletes every persisted management log and reports the affected row count.
func (l *Logger) Clear(ctx context.Context, query Query) (int, error) {
	if l == nil {
		return 0, nil
	}
	l.mu.RLock()
	store := l.store
	l.mu.RUnlock()
	if store == nil {
		return 0, nil
	}
	return store.Clear(ctx, query)
}

// DeleteBefore removes logs older than the supplied UTC cutoff.
func (l *Logger) DeleteBefore(ctx context.Context, before time.Time) (int, error) {
	if l == nil {
		return 0, nil
	}
	l.mu.RLock()
	store := l.store
	l.mu.RUnlock()
	if store == nil {
		return 0, nil
	}
	return store.DeleteBefore(ctx, before)
}

func (l *Logger) log(level slog.Level, category Category, message string, attrs ...any) {
	if l == nil || l.logger == nil {
		return
	}
	if !category.Valid() {
		category = CategoryOther
	}
	fields := []slog.Attr{slog.String("category", string(category))}
	for i := 0; i+1 < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if !ok {
			continue
		}
		fields = append(fields, slog.Any(key, attrs[i+1]))
	}
	l.logger.LogAttrs(context.Background(), level, message, fields...)
	l.persist(levelOf(level), category, message, fields)
}

// persist 将完整附加字段写入数据库；持久化失败只写标准日志，不中断原业务流程。
func (l *Logger) persist(level Level, category Category, message string, fields []slog.Attr) {
	// 对外契约（api/openapi.yaml）约定所有时间为 UTC RFC 3339，由前端换算为本地时区展示。
	record := Record{Time: time.Now().UTC(), Level: level, Category: category, Message: message}
	for _, field := range fields {
		if field.Key == "category" {
			continue
		}
		if record.Attrs == nil {
			record.Attrs = make(map[string]any, len(fields)-1)
		}
		record.Attrs[field.Key] = field.Value.Any()
	}
	l.mu.RLock()
	store := l.store
	l.mu.RUnlock()
	if store == nil {
		return
	}
	if err := store.Append(context.Background(), record); err != nil {
		l.logger.Error("日志持久化失败", "error", err)
	}
}

func levelOf(level slog.Level) Level {
	switch {
	case level < slog.LevelInfo:
		return LevelDebug
	case level < slog.LevelWarn:
		return LevelInfo
	case level < slog.LevelError:
		return LevelWarning
	default:
		return LevelError
	}
}
