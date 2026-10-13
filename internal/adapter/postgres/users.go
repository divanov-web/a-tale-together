package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

// userCols — столбцы users с алиасом таблицы для scanUser.
func userCols(a string) string {
	return fmt.Sprintf("%[1]sid, %[1]stelegram_id, coalesce(%[1]susername, ''), %[1]sfirst_name, coalesce(%[1]slanguage_code, ''), %[1]sstarted_at, %[1]scurrent_game_id, %[1]slast_seen_at", a)
}

var userColumns = userCols("")

func scanUser(row pgx.Row) (model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.TelegramID, &u.Username, &u.FirstName, &u.LanguageCode, &u.StartedAt, &u.CurrentGameID, &u.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, model.ErrNotFound
	}
	return u, err
}

// UpsertUser создаёт пользователя или обновляет его имя и время последней активности; возвращает запись.
func (s *Store) UpsertUser(ctx context.Context, u model.TelegramUser) (model.User, error) {
	q := `
INSERT INTO users (telegram_id, username, first_name, language_code)
VALUES ($1, NULLIF($2, ''), $3, NULLIF($4, ''))
ON CONFLICT (telegram_id) DO UPDATE SET
    username      = EXCLUDED.username,
    first_name    = EXCLUDED.first_name,
    language_code = EXCLUDED.language_code,
    last_seen_at  = now(),
    updated_at    = now()
RETURNING ` + userColumns
	user, err := scanUser(s.db(ctx).QueryRow(ctx, q, int64(u.ID), u.Username, u.FirstName, u.LanguageCode))
	if err != nil {
		return model.User{}, fmt.Errorf("postgres.UpsertUser: %w", err)
	}
	return user, nil
}

// GetUser возвращает пользователя по внутреннему id.
func (s *Store) GetUser(ctx context.Context, id model.UserID) (model.User, error) {
	u, err := scanUser(s.db(ctx).QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if err != nil {
		return model.User{}, fmt.Errorf("postgres.GetUser: %w", err)
	}
	return u, nil
}

// GetUserByUsername находит пользователя по @username без учёта регистра.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (model.User, error) {
	u, err := scanUser(s.db(ctx).QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE lower(username) = lower($1)`, username))
	if err != nil {
		return model.User{}, fmt.Errorf("postgres.GetUserByUsername: %w", err)
	}
	return u, nil
}

// GetUserByTelegramID находит пользователя по идентификатору Telegram (он же чат лички).
func (s *Store) GetUserByTelegramID(ctx context.Context, id model.TelegramUserID) (model.User, error) {
	u, err := scanUser(s.db(ctx).QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE telegram_id = $1`, id))
	if err != nil {
		return model.User{}, fmt.Errorf("postgres.GetUserByTelegramID: %w", err)
	}
	return u, nil
}

// MarkStarted фиксирует первый /start в личке; повтор не меняет дату.
func (s *Store) MarkStarted(ctx context.Context, id model.UserID) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE users SET started_at = coalesce(started_at, now()), updated_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("postgres.MarkStarted: %w", err)
	}
	return nil
}

// SetCurrentGame задаёт игру, к которой относятся команды пользователя в личке; nil сбрасывает.
func (s *Store) SetCurrentGame(ctx context.Context, id model.UserID, gameID *model.GameID) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE users SET current_game_id = $2, updated_at = now() WHERE id = $1`, id, gameID); err != nil {
		return fmt.Errorf("postgres.SetCurrentGame: %w", err)
	}
	return nil
}
