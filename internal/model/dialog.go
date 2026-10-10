package model

import (
	"encoding/json"
	"time"
)

// DialogKind — вид многошагового диалога в личке.
type DialogKind string

// Виды диалогов.
const (
	DialogGameSetup       DialogKind = "game_setup"
	DialogCharacterCreate DialogKind = "character_create"
)

// Dialog — состояние диалога пользователя в личке; у пользователя не более одного диалога.
type Dialog struct {
	UserID    UserID
	Kind      DialogKind
	Step      string
	GameID    *GameID
	Data      json.RawMessage
	UpdatedAt time.Time
}

// BotCommand — команда для меню Telegram (setMyCommands).
type BotCommand struct {
	Name        string
	Description string
}
