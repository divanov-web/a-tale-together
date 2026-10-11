// Package model: общие сущности без поведения с I/O.
package model

import (
	"strings"
	"time"
)

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

// Incoming — входящее сообщение или нажатие кнопки, уже очищенное от деталей Telegram API.
// Command без ведущего слэша и без суффикса @bot; Args — остаток текста после команды. У нажатия кнопки (ADR-0015)
// заполнен Callback, а Text, Command и Args разобраны из данных кнопки так же, как у текста.
type Incoming struct {
	UpdateID   int64
	ChatID     TelegramChatID
	ChatType   ChatType
	From       TelegramUser
	Text       string
	Command    string
	Args       string
	ReceivedAt time.Time
	Callback   *Callback
}

// IsPrivate сообщает, пришло ли сообщение из личного чата.
func (in Incoming) IsPrivate() bool { return in.ChatType == ChatPrivate }

// Callback — нажатие inline-кнопки: идентификатор запроса для answerCallbackQuery, сообщение с кнопками и шаг диалога
// из данных кнопки (пусто у кнопок-команд).
type Callback struct {
	ID        string
	MessageID int64
	Step      string
}

// Button — кнопка под сообщением: подпись и данные нажатия (`callback_data`, до CallbackDataMaxLen байт) либо ссылка.
type Button struct {
	Text string `json:"text"`
	Data string `json:"data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Keyboard — ряды кнопок под сообщением; nil — без кнопок.
type Keyboard [][]Button

// CallbackDataMaxLen — лимит Telegram на данные кнопки в байтах.
const CallbackDataMaxLen = 64

// CallbackCommand — данные кнопки, повторяющей команду с аргументами: `/команда аргументы` (ADR-0015).
func CallbackCommand(command, args string) string {
	if args == "" {
		return "/" + command
	}
	return "/" + command + " " + args
}

// CallbackAnswer — данные кнопки с ответом на шаг диалога: `@шаг ответ`; ответ применяется только на этом шаге.
func CallbackAnswer(step, answer string) string {
	return "@" + step + " " + answer
}

// ParseCallbackData разбирает данные кнопки: `/команда аргументы` → command и args, `@шаг ответ` → step и text,
// иначе весь текст как ответ без шага. Command возвращается строчными буквами.
func ParseCallbackData(data string) (command, args, step, text string) {
	data = strings.TrimSpace(data)
	switch {
	case strings.HasPrefix(data, "/"):
		command, args, _ = strings.Cut(data[1:], " ")
		return strings.ToLower(command), strings.TrimSpace(args), "", data
	case strings.HasPrefix(data, "@"):
		step, text, _ = strings.Cut(data[1:], " ")
		return "", "", step, strings.TrimSpace(text)
	}
	return "", "", "", data
}
