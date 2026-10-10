package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

// chatCols — столбцы chats с алиасом таблицы для scanChat.
func chatCols(a string) string {
	return fmt.Sprintf("%[1]sid, %[1]stelegram_chat_id, %[1]stype, %[1]stitle, %[1]sowner_user_id, %[1]sbot_status, %[1]sadded_at, %[1]sleft_at", a)
}

var chatColumns = chatCols("")

func scanChat(row pgx.Row) (model.Chat, error) {
	var c model.Chat
	err := row.Scan(&c.ID, &c.TelegramChatID, &c.Type, &c.Title, &c.OwnerUserID, &c.BotStatus, &c.AddedAt, &c.LeftAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Chat{}, model.ErrNotFound
	}
	return c, err
}

// GetChatByTelegramID возвращает чат по идентификатору Telegram.
func (s *Store) GetChatByTelegramID(ctx context.Context, id model.TelegramChatID) (model.Chat, error) {
	c, err := scanChat(s.db(ctx).QueryRow(ctx, `SELECT `+chatColumns+` FROM chats WHERE telegram_chat_id = $1`, id))
	if err != nil {
		return model.Chat{}, fmt.Errorf("postgres.GetChatByTelegramID: %w", err)
	}
	return c, nil
}

// GetChat возвращает чат по внутреннему id.
func (s *Store) GetChat(ctx context.Context, id model.ChatID) (model.Chat, error) {
	c, err := scanChat(s.db(ctx).QueryRow(ctx, `SELECT `+chatColumns+` FROM chats WHERE id = $1`, id))
	if err != nil {
		return model.Chat{}, fmt.Errorf("postgres.GetChat: %w", err)
	}
	return c, nil
}

// CreateChat регистрирует чат; при повторе по telegram_chat_id возвращает существующую запись без изменений.
func (s *Store) CreateChat(ctx context.Context, c model.Chat) (model.Chat, error) {
	q := `
INSERT INTO chats (telegram_chat_id, type, title, owner_user_id, bot_status)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (telegram_chat_id) DO UPDATE SET updated_at = now()
RETURNING ` + chatColumns
	out, err := scanChat(s.db(ctx).QueryRow(ctx, q, c.TelegramChatID, c.Type, c.Title, c.OwnerUserID, c.BotStatus))
	if err != nil {
		return model.Chat{}, fmt.Errorf("postgres.CreateChat: %w", err)
	}
	return out, nil
}

// UpdateChatStatus меняет статус бота и название чата; при уходе бота ставит left_at.
func (s *Store) UpdateChatStatus(ctx context.Context, id model.ChatID, status model.BotStatus, title string) error {
	const q = `
UPDATE chats SET bot_status = $2, title = $3,
    left_at = CASE WHEN $2 = 'left' THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = $1`
	if _, err := s.db(ctx).Exec(ctx, q, id, status, title); err != nil {
		return fmt.Errorf("postgres.UpdateChatStatus: %w", err)
	}
	return nil
}

// SetChatOwner передаёт владение чатом.
func (s *Store) SetChatOwner(ctx context.Context, id model.ChatID, owner model.UserID) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE chats SET owner_user_id = $2, updated_at = now() WHERE id = $1`, id, owner); err != nil {
		return fmt.Errorf("postgres.SetChatOwner: %w", err)
	}
	return nil
}

// AddModerator добавляет модератора; повтор не ошибка.
func (s *Store) AddModerator(ctx context.Context, chat model.ChatID, user, grantedBy model.UserID) error {
	const q = `INSERT INTO chat_moderators (chat_id, user_id, granted_by_user_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`
	if _, err := s.db(ctx).Exec(ctx, q, chat, user, grantedBy); err != nil {
		return fmt.Errorf("postgres.AddModerator: %w", err)
	}
	return nil
}

// RemoveModerator снимает модератора; отсутствие записи не ошибка.
func (s *Store) RemoveModerator(ctx context.Context, chat model.ChatID, user model.UserID) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM chat_moderators WHERE chat_id = $1 AND user_id = $2`, chat, user); err != nil {
		return fmt.Errorf("postgres.RemoveModerator: %w", err)
	}
	return nil
}

// IsModerator сообщает, есть ли пользователь в списке модераторов чата.
func (s *Store) IsModerator(ctx context.Context, chat model.ChatID, user model.UserID) (bool, error) {
	var ok bool
	err := s.db(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM chat_moderators WHERE chat_id = $1 AND user_id = $2)`, chat, user).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("postgres.IsModerator: %w", err)
	}
	return ok, nil
}

// ListModerators возвращает модераторов чата в порядке назначения.
func (s *Store) ListModerators(ctx context.Context, chat model.ChatID) ([]model.User, error) {
	q := `SELECT ` + userCols("u.") + ` FROM chat_moderators m JOIN users u ON u.id = m.user_id WHERE m.chat_id = $1 ORDER BY m.created_at`
	rows, err := s.db(ctx).Query(ctx, q, chat)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListModerators: %w", err)
	}
	defer rows.Close()
	var out []model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ListModerators: scan: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ChatsOfUser возвращает чаты с ботом, где пользователь владелец или модератор.
func (s *Store) ChatsOfUser(ctx context.Context, user model.UserID) ([]model.Chat, error) {
	q := `
SELECT DISTINCT ` + chatCols("c.") + `
FROM chats c LEFT JOIN chat_moderators m ON m.chat_id = c.id AND m.user_id = $1
WHERE c.bot_status <> 'left' AND (c.owner_user_id = $1 OR m.user_id IS NOT NULL)
ORDER BY c.id`
	rows, err := s.db(ctx).Query(ctx, q, user)
	if err != nil {
		return nil, fmt.Errorf("postgres.ChatsOfUser: %w", err)
	}
	defer rows.Close()
	var out []model.Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ChatsOfUser: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
