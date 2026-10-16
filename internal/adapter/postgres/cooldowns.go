package postgres

import (
	"context"
	"fmt"

	"taletogether/internal/model"
)

// ListCooldowns возвращает перезарядки владельца в игре (ADR-0033).
func (s *Store) ListCooldowns(ctx context.Context, game model.GameID, owner model.CooldownOwner, ownerID int64) ([]model.Cooldown, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT game_id, owner_kind, owner_id, action, last_round, updated_at FROM cooldowns
		WHERE game_id = $1 AND owner_kind = $2 AND owner_id = $3 ORDER BY action`, game, owner, ownerID)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListCooldowns: %w", err)
	}
	defer rows.Close()
	var out []model.Cooldown
	for rows.Next() {
		var c model.Cooldown
		if err := rows.Scan(&c.GameID, &c.Owner, &c.OwnerID, &c.Action, &c.LastRound, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres.ListCooldowns: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetCooldown записывает раунд последнего выполнения действия владельцем; повтор для того же действия обновляет строку.
func (s *Store) SetCooldown(ctx context.Context, c model.Cooldown) error {
	if _, err := s.db(ctx).Exec(ctx, `INSERT INTO cooldowns (game_id, owner_kind, owner_id, action, last_round)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (game_id, owner_kind, owner_id, action) DO UPDATE SET last_round = EXCLUDED.last_round, updated_at = now()`,
		c.GameID, c.Owner, c.OwnerID, c.Action, c.LastRound); err != nil {
		return fmt.Errorf("postgres.SetCooldown: %w", err)
	}
	return nil
}
