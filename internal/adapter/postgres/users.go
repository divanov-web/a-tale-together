package postgres

import (
	"context"
	"fmt"

	"taletogether/internal/model"
)

// UpsertUser создаёт пользователя или обновляет его имя и время последней активности.
func (s *Store) UpsertUser(ctx context.Context, u model.TelegramUser) error {
	const q = `
INSERT INTO users (telegram_id, username, first_name, language_code)
VALUES ($1, NULLIF($2, ''), $3, NULLIF($4, ''))
ON CONFLICT (telegram_id) DO UPDATE SET
    username      = EXCLUDED.username,
    first_name    = EXCLUDED.first_name,
    language_code = EXCLUDED.language_code,
    last_seen_at  = now(),
    updated_at    = now()`
	if _, err := s.q.Exec(ctx, q, int64(u.ID), u.Username, u.FirstName, u.LanguageCode); err != nil {
		return fmt.Errorf("postgres.UpsertUser: %w", err)
	}
	return nil
}
