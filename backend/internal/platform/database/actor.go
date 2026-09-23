package database

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"context"
	"database/sql"
	"fmt"
)

type actorRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewActorRepository binds a migrated SQL store to the actor read port.
func NewActorRepository(db *sql.DB, dialect Dialect) ports.ActorRepository {
	return &actorRepository{db: db, dialect: dialect}
}

// ListSubscribed returns actors whose legacy limit_date is present, ordered by name.
func (r *actorRepository) ListSubscribed(ctx context.Context, limit, offset int) ([]domain.Actor, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM actors WHERE limit_date IS NOT NULL`).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := fmt.Sprintf(`SELECT name, photo, limit_date FROM actors WHERE limit_date IS NOT NULL ORDER BY name LIMIT %s OFFSET %s`, placeholder(r.dialect, 1), placeholder(r.dialect, 2))
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
