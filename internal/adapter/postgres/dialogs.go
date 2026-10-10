package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

// GetDialog возвращает текущий диалог пользователя в личке.
func (s *Store) GetDialog(ctx context.Context, user model.UserID) (model.Dialog, error) {
	var d model.Dialog
	err := s.db(ctx).QueryRow(ctx, `SELECT user_id, kind, step, game_id, data, updated_at FROM dialogs WHERE user_id = $1`, user).
		Scan(&d.UserID, &d.Kind, &d.Step, &d.GameID, &d.Data, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Dialog{}, fmt.Errorf("postgres.GetDialog: %w", model.ErrNotFound)
	}
	if err != nil {
		return model.Dialog{}, fmt.Errorf("postgres.GetDialog: %w", err)
	}
	return d, nil
}

// SaveDialog создаёт или заменяет диалог пользователя.
func (s *Store) SaveDialog(ctx context.Context, d model.Dialog) error {
	const q = `
INSERT INTO dialogs (user_id, kind, step, game_id, data) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id) DO UPDATE SET kind = EXCLUDED.kind, step = EXCLUDED.step, game_id = EXCLUDED.game_id, data = EXCLUDED.data, updated_at = now()`
	data := d.Data
	if len(data) == 0 {
		data = []byte("{}")
	}
	if _, err := s.db(ctx).Exec(ctx, q, d.UserID, d.Kind, d.Step, d.GameID, data); err != nil {
		return fmt.Errorf("postgres.SaveDialog: %w", err)
	}
	return nil
}

// DeleteDialog завершает диалог пользователя; отсутствие не ошибка.
func (s *Store) DeleteDialog(ctx context.Context, user model.UserID) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM dialogs WHERE user_id = $1`, user); err != nil {
		return fmt.Errorf("postgres.DeleteDialog: %w", err)
	}
	return nil
}
