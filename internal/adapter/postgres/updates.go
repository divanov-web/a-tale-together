package postgres

import (
	"context"
	"fmt"
)

// MarkUpdate регистрирует update Telegram; false — такой update_id уже обработан.
func (s *Store) MarkUpdate(ctx context.Context, updateID int64) (bool, error) {
	const q = `INSERT INTO telegram_updates (update_id) VALUES ($1) ON CONFLICT DO NOTHING`
	tag, err := s.q.Exec(ctx, q, updateID)
	if err != nil {
		return false, fmt.Errorf("postgres.MarkUpdate: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
