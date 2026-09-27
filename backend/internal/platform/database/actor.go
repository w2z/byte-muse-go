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

// List returns actors selected by subscription state, ordered by name.
func (r *actorRepository) List(ctx context.Context, request ports.ActorListQuery) ([]domain.Actor, int, error) {
	limit, offset := normalizePagination(request.Limit, request.Offset)
	where := ""
	switch request.Subscription {
	case "active":
		where = " WHERE limit_date IS NOT NULL"
	case "none":
		where = " WHERE limit_date IS NULL"
	}
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM actors"+where).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := fmt.Sprintf(`SELECT name, photo, limit_date FROM actors%s ORDER BY name LIMIT %s OFFSET %s`, where, placeholder(r.dialect, 1), placeholder(r.dialect, 2))
	rows, err := r.db.QueryContext(ctx, query, limit, offset)
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
