package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

const genreColumns = `id, code, title, character_model_id, template, enabled`

func scanGenre(row pgx.Row) (model.Genre, error) {
	var g model.Genre
	err := row.Scan(&g.ID, &g.Code, &g.Title, &g.CharacterModelID, &g.Template, &g.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Genre{}, model.ErrNotFound
	}
	return g, err
}

// GetGenre возвращает жанр по id.
func (s *Store) GetGenre(ctx context.Context, id model.GenreID) (model.Genre, error) {
	g, err := scanGenre(s.db(ctx).QueryRow(ctx, `SELECT `+genreColumns+` FROM genres WHERE id = $1`, id))
	if err != nil {
		return model.Genre{}, fmt.Errorf("postgres.GetGenre: %w", err)
	}
	return g, nil
}

// GetGenreByCode возвращает включённый жанр по коду.
func (s *Store) GetGenreByCode(ctx context.Context, code string) (model.Genre, error) {
	g, err := scanGenre(s.db(ctx).QueryRow(ctx, `SELECT `+genreColumns+` FROM genres WHERE code = $1 AND enabled`, code))
	if err != nil {
		return model.Genre{}, fmt.Errorf("postgres.GetGenreByCode: %w", err)
	}
	return g, nil
}

// ListGenres возвращает включённые жанры по порядку добавления.
func (s *Store) ListGenres(ctx context.Context) ([]model.Genre, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+genreColumns+` FROM genres WHERE enabled ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListGenres: %w", err)
	}
	defer rows.Close()
	var out []model.Genre
	for rows.Next() {
		g, err := scanGenre(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ListGenres: scan: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetCharacterModel возвращает модель персонажа по id.
func (s *Store) GetCharacterModel(ctx context.Context, id model.CharacterModelID) (model.CharacterModel, error) {
	var m model.CharacterModel
	err := s.db(ctx).QueryRow(ctx, `SELECT id, code, rules_version, definition FROM character_models WHERE id = $1`, id).
		Scan(&m.ID, &m.Code, &m.RulesVersion, &m.Definition)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.CharacterModel{}, fmt.Errorf("postgres.GetCharacterModel: %w", model.ErrNotFound)
	}
	if err != nil {
		return model.CharacterModel{}, fmt.Errorf("postgres.GetCharacterModel: %w", err)
	}
	return m, nil
}
