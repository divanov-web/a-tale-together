package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

const locationColumns = `id, game_id, name, description, difficulty, links, objects, active, start, created_at`

func scanLocation(row pgx.Row) (model.Location, error) {
	var l model.Location
	err := row.Scan(&l.ID, &l.GameID, &l.Name, &l.Description, &l.Difficulty, &l.Links, &l.Objects, &l.Active, &l.Start, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Location{}, model.ErrNotFound
	}
	if l.Links == nil {
		l.Links = []model.LocationLink{}
	}
	if l.Objects == nil {
		l.Objects = []model.LocationObject{}
	}
	return l, err
}

// CreateLocation создаёт локацию игры; повтор имени — ошибка БД.
func (s *Store) CreateLocation(ctx context.Context, l model.Location) (model.Location, error) {
	links, objects := l.Links, l.Objects
	if links == nil {
		links = []model.LocationLink{}
	}
	if objects == nil {
		objects = []model.LocationObject{}
	}
	const q = `INSERT INTO locations (game_id, name, description, difficulty, links, objects, active, start) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING ` + locationColumns
	out, err := scanLocation(s.db(ctx).QueryRow(ctx, q, l.GameID, l.Name, l.Description, l.Difficulty, links, objects, l.Active, l.Start))
	if err != nil {
		return model.Location{}, fmt.Errorf("postgres.CreateLocation: %w", err)
	}
	return out, nil
}

// ListLocations возвращает локации игры по порядку создания.
func (s *Store) ListLocations(ctx context.Context, game model.GameID) ([]model.Location, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+locationColumns+` FROM locations WHERE game_id = $1 ORDER BY id`, game)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListLocations: %w", err)
	}
	defer rows.Close()
	var out []model.Location
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ListLocations: scan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// UpdateLocationObjects сохраняет объекты локации после применения событий.
func (s *Store) UpdateLocationObjects(ctx context.Context, game model.GameID, id model.LocationID, objects []model.LocationObject) error {
	if objects == nil {
		objects = []model.LocationObject{}
	}
	if _, err := s.db(ctx).Exec(ctx, `UPDATE locations SET objects = $3, updated_at = now() WHERE game_id = $1 AND id = $2`, game, id, objects); err != nil {
		return fmt.Errorf("postgres.UpdateLocationObjects: %w", err)
	}
	return nil
}
