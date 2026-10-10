package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

const characterColumns = `id, game_id, season_id, user_id, model_id, nickname, archetype, public_bio, secret_bio, status, missed_rounds, faction_id, party_id, hp, conditions, inventory, location_id, xp, created_at`

func scanCharacter(row pgx.Row) (model.Character, error) {
	var c model.Character
	err := row.Scan(&c.ID, &c.GameID, &c.SeasonID, &c.UserID, &c.ModelID, &c.Nickname, &c.Archetype, &c.PublicBio, &c.SecretBio,
		&c.Status, &c.MissedRounds, &c.FactionID, &c.PartyID, &c.HP, &c.Conditions, &c.Inventory, &c.LocationID, &c.XP, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Character{}, model.ErrNotFound
	}
	return c, err
}

// CreateCharacter создаёт персонажа и его характеристики; вызывать внутри InTx.
func (s *Store) CreateCharacter(ctx context.Context, c model.Character) (model.Character, error) {
	const q = `
INSERT INTO characters (game_id, season_id, user_id, model_id, nickname, archetype, public_bio, secret_bio, status, faction_id, hp, conditions, inventory, xp)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING ` + characterColumns
	conditions := c.Conditions
	if conditions == nil {
		conditions = []string{}
	}
	inventory := c.Inventory
	if inventory == nil {
		inventory = []model.Item{}
	}
	out, err := scanCharacter(s.db(ctx).QueryRow(ctx, q, c.GameID, c.SeasonID, c.UserID, c.ModelID, c.Nickname, c.Archetype, c.PublicBio, c.SecretBio,
		c.Status, c.FactionID, c.HP, conditions, inventory, c.XP))
	if err != nil {
		return model.Character{}, fmt.Errorf("postgres.CreateCharacter: %w", err)
	}
	const qs = `INSERT INTO character_stats (game_id, character_id, kind, key, value) VALUES ($1, $2, $3, $4, $5)`
	for key, v := range c.Attributes {
		if _, err := s.db(ctx).Exec(ctx, qs, out.GameID, out.ID, "attribute", key, v); err != nil {
			return model.Character{}, fmt.Errorf("postgres.CreateCharacter: attribute %s: %w", key, err)
		}
	}
	for key, v := range c.Skills {
		if _, err := s.db(ctx).Exec(ctx, qs, out.GameID, out.ID, "skill", key, v); err != nil {
			return model.Character{}, fmt.Errorf("postgres.CreateCharacter: skill %s: %w", key, err)
		}
	}
	out.Attributes = c.Attributes
	out.Skills = c.Skills
	return out, nil
}

// GetCharacter возвращает персонажа пользователя в сезоне вместе с характеристиками.
func (s *Store) GetCharacter(ctx context.Context, game model.GameID, season model.SeasonID, user model.UserID) (model.Character, error) {
	c, err := scanCharacter(s.db(ctx).QueryRow(ctx, `SELECT `+characterColumns+` FROM characters WHERE game_id = $1 AND season_id = $2 AND user_id = $3`, game, season, user))
	if err != nil {
		return model.Character{}, fmt.Errorf("postgres.GetCharacter: %w", err)
	}
	if err := s.loadStats(ctx, &c); err != nil {
		return model.Character{}, fmt.Errorf("postgres.GetCharacter: %w", err)
	}
	return c, nil
}

// ListCharacters возвращает персонажей сезона с характеристиками по порядку создания.
func (s *Store) ListCharacters(ctx context.Context, game model.GameID, season model.SeasonID) ([]model.Character, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+characterColumns+` FROM characters WHERE game_id = $1 AND season_id = $2 ORDER BY id`, game, season)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListCharacters: %w", err)
	}
	defer rows.Close()
	var out []model.Character
	for rows.Next() {
		c, err := scanCharacter(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ListCharacters: scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadStats(ctx, &out[i]); err != nil {
			return nil, fmt.Errorf("postgres.ListCharacters: %w", err)
		}
	}
	return out, nil
}

// UpdateCharacterBio сохраняет открытое и скрытое описание.
func (s *Store) UpdateCharacterBio(ctx context.Context, game model.GameID, id model.CharacterID, public, secret string) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE characters SET public_bio = $3, secret_bio = $4, updated_at = now() WHERE game_id = $1 AND id = $2`, game, id, public, secret); err != nil {
		return fmt.Errorf("postgres.UpdateCharacterBio: %w", err)
	}
	return nil
}

// NicknameTaken сообщает, занят ли никнейм в сезоне (без учёта регистра).
func (s *Store) NicknameTaken(ctx context.Context, game model.GameID, season model.SeasonID, nickname string) (bool, error) {
	var ok bool
	err := s.db(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM characters WHERE game_id = $1 AND season_id = $2 AND lower(nickname) = lower($3))`, game, season, nickname).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("postgres.NicknameTaken: %w", err)
	}
	return ok, nil
}

func (s *Store) loadStats(ctx context.Context, c *model.Character) error {
	rows, err := s.db(ctx).Query(ctx, `SELECT kind, key, value FROM character_stats WHERE game_id = $1 AND character_id = $2`, c.GameID, c.ID)
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}
	defer rows.Close()
	c.Attributes = map[string]int{}
	c.Skills = map[string]int{}
	for rows.Next() {
		var kind, key string
		var v int
		if err := rows.Scan(&kind, &key, &v); err != nil {
			return fmt.Errorf("stats scan: %w", err)
		}
		switch kind {
		case "attribute":
			c.Attributes[key] = v
		case "skill":
			c.Skills[key] = v
		}
	}
	return rows.Err()
}
