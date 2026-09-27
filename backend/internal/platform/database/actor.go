package database

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type actorRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewActorRepository binds a migrated SQL store to the actor read port.
func NewActorRepository(db *sql.DB, dialect Dialect) ports.ActorRepository {
	return &actorRepository{db: db, dialect: dialect}
}

// List returns actors selected by subscription state and optional name keyword.
func (r *actorRepository) List(ctx context.Context, request ports.ActorListQuery) ([]domain.Actor, int, error) {
	limit, offset := normalizePagination(request.Limit, request.Offset)
	conditions := make([]string, 0, 2)
	args := make([]any, 0, 2)
	switch request.Subscription {
	case "active":
		conditions = append(conditions, "limit_date IS NOT NULL")
	case "none":
		conditions = append(conditions, "limit_date IS NULL")
	case "hot":
		// 热门演员通过演员榜单快照关联，不额外限制订阅状态。
	}
	if keyword := strings.TrimSpace(request.Keywords); keyword != "" {
		conditions = append(conditions, fmt.Sprintf("UPPER(name) LIKE %s", placeholder(r.dialect, len(args)+1)))
		args = append(args, "%"+strings.ToUpper(keyword)+"%")
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	from := "actors"
	if request.Subscription == "hot" {
		from = "actors a INNER JOIN rank_entries ar ON ar.rank_type = 'actors' AND UPPER(ar.code) = UPPER(a.name)"
	}
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+from+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	orderBy := "name ASC"
	if request.Subscription == "hot" {
		orderBy = "ar.position ASC, a.name ASC"
	}
	selectArgs := append(append([]any{}, args...), limit, offset)
	selectFrom := "actors"
	selectColumns := "name, photo, limit_date"
	if request.Subscription == "hot" {
		selectFrom = "actors a INNER JOIN rank_entries ar ON ar.rank_type = 'actors' AND UPPER(ar.code) = UPPER(a.name)"
		selectColumns = "a.name, a.photo, a.limit_date"
	}
	query := fmt.Sprintf(`SELECT %s FROM %s%s ORDER BY %s LIMIT %s OFFSET %s`, selectColumns, selectFrom, where, orderBy, placeholder(r.dialect, len(selectArgs)-1), placeholder(r.dialect, len(selectArgs)))
	rows, err := r.db.QueryContext(ctx, query, selectArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]domain.Actor, 0)
	for rows.Next() {
		var name string
		var photo, limitDate sql.NullString
		if err := rows.Scan(&name, &photo, &limitDate); err != nil {
			return nil, 0, err
		}
		item := domain.Actor{Name: name}
		if photo.Valid {
			item.Photo = &photo.String
		}
		if limitDate.Valid {
			item.LimitDate = &limitDate.String
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// SaveSubscription sets the actor cutoff date while preserving the actor identity and metadata.
func (r *actorRepository) SaveSubscription(ctx context.Context, name, limitDate string) (domain.Actor, error) {
	name, limitDate = strings.TrimSpace(name), strings.TrimSpace(limitDate)
	if name == "" || limitDate == "" {
		return domain.Actor{}, fmt.Errorf("actor name and limit date are required")
	}
	if _, err := time.Parse("2006-01-02", limitDate); err != nil {
		return domain.Actor{}, fmt.Errorf("invalid actor limit date: %w", err)
	}
	result, err := r.db.ExecContext(ctx, fmt.Sprintf(`UPDATE actors SET limit_date = %s, updated_at = %s WHERE name = %s`, placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3)), limitDate, encodeTime(time.Now().UTC(), r.dialect), name)
	if err != nil {
		return domain.Actor{}, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return domain.Actor{}, ports.ErrActorNotFound
	}
	return r.get(ctx, name)
}

// CancelSubscription clears only the cutoff date; actor metadata remains available for later resubscription.
func (r *actorRepository) CancelSubscription(ctx context.Context, name string) (domain.Actor, error) {
	name = strings.TrimSpace(name)
	result, err := r.db.ExecContext(ctx, fmt.Sprintf(`UPDATE actors SET limit_date = NULL, updated_at = %s WHERE name = %s`, placeholder(r.dialect, 1), placeholder(r.dialect, 2)), encodeTime(time.Now().UTC(), r.dialect), name)
	if err != nil {
		return domain.Actor{}, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return domain.Actor{}, ports.ErrActorNotFound
	}
	return r.get(ctx, name)
}

func (r *actorRepository) get(ctx context.Context, name string) (domain.Actor, error) {
	var item domain.Actor
	var photo, limitDate sql.NullString
	err := r.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT name, photo, limit_date FROM actors WHERE name = %s`, placeholder(r.dialect, 1)), name).Scan(&item.Name, &photo, &limitDate)
	if err == sql.ErrNoRows {
		return domain.Actor{}, ports.ErrActorNotFound
	}
	if err != nil {
		return domain.Actor{}, err
	}
	if photo.Valid {
		item.Photo = &photo.String
	}
	if limitDate.Valid {
		item.LimitDate = &limitDate.String
	}
	return item, nil
}
