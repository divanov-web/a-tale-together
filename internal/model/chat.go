package model

import "time"

// BotStatus — состояние бота в групповом чате.
type BotStatus string

// Состояния бота в чате.
const (
	BotMember BotStatus = "member"
	BotAdmin  BotStatus = "admin"
	BotLeft   BotStatus = "left"
)

// Chat — групповой чат, куда добавили бота (ADR-0007).
type Chat struct {
	ID             ChatID
	TelegramChatID TelegramChatID
	Type           ChatType
	Title          string
	OwnerUserID    UserID
	BotStatus      BotStatus
	AddedAt        time.Time
	LeftAt         *time.Time
}

// ChatMemberChange — изменение статуса бота в чате (update my_chat_member).
type ChatMemberChange struct {
	UpdateID   int64
	ChatID     TelegramChatID
	ChatType   ChatType
	ChatTitle  string
	From       TelegramUser // кто изменил статус бота
	NewStatus  BotStatus
	ReceivedAt time.Time
}

// ChatInfo — чат с владельцем и модераторами для ответов бота.
type ChatInfo struct {
	Chat       Chat
	Owner      User
	Moderators []User
}
