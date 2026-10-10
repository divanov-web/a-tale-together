package model

import "time"

// User — пользователь Telegram, известный боту (таблица users).
type User struct {
	ID            UserID
	TelegramID    TelegramUserID
	Username      string
	FirstName     string
	LanguageCode  string
	StartedAt     *time.Time // первый /start в личке; nil — личка не открыта
	CurrentGameID *GameID    // игра, к которой относятся команды в личке
	LastSeenAt    time.Time
}

// Mention — как обращаться к пользователю в текстах: @username или имя.
func (u User) Mention() string {
	if u.Username != "" {
		return "@" + u.Username
	}
	return u.FirstName
}

// PrivateChat — идентификатор личного чата пользователя с ботом (равен telegram_id, ADR-0007).
func (u User) PrivateChat() TelegramChatID { return TelegramChatID(u.TelegramID) }
