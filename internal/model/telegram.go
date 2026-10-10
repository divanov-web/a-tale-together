// Package model: общие сущности без поведения с I/O.
package model

import "time"

// TelegramChatID — идентификатор чата Telegram (группа отрицательный, личка положительный).
type TelegramChatID int64

// TelegramUserID — идентификатор пользователя Telegram.
type TelegramUserID int64

// ChatType — тип чата Telegram.
type ChatType string

// Типы чатов Telegram.
const (
	ChatPrivate    ChatType = "private"
	ChatGroup      ChatType = "group"
	ChatSupergroup ChatType = "supergroup"
	ChatChannel    ChatType = "channel"
)

// TelegramUser — данные отправителя из update; хранятся в таблице users.
type TelegramUser struct {
	ID           TelegramUserID
	Username     string
	FirstName    string
	LanguageCode string
}

// Incoming — входящее текстовое сообщение, уже очищенное от деталей Telegram API.
// Command без ведущего слэша и без суффикса @bot; Args — остаток текста после команды.
type Incoming struct {
	UpdateID   int64
	ChatID     TelegramChatID
	ChatType   ChatType
	From       TelegramUser
	Text       string
	Command    string
	Args       string
	ReceivedAt time.Time
}

// IsPrivate сообщает, пришло ли сообщение из личного чата.
func (in Incoming) IsPrivate() bool { return in.ChatType == ChatPrivate }
