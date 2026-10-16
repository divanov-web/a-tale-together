// Package fake — подмена транспорта Telegram в процессе (ADR-0034): реализует интерфейсы `bot.Telegram`
// и `delivery.Sender`, хранит отправленные сообщения по чатам и собирает входящие так же, как боевой клиент
// строит их из update. Сети нет; бот неотличим от боевого для сценариев, диалогов, кнопок и outbox.
package fake

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"taletogether/internal/model"
)

// Transport — ящики сообщений по чатам, счётчики идентификаторов и права в группах.
type Transport struct {
	now func() time.Time

	mu        sync.Mutex
	nextMsg   int64
	nextUpd   int64
	boxes     map[model.TelegramChatID][]model.SentMessage
	admins    map[model.TelegramChatID]map[model.TelegramUserID]bool
	cleared   map[int64]bool
	commands  map[string][]model.BotCommand // меню по областям: "private", "group", "member:<chat>:<user>"
	titles    map[model.TelegramChatID]string
	botName   string
	sendDelay time.Duration
}

// New готовит транспорт; now — источник времени для отметок сообщений (nil — time.Now), botName — имя бота без @.
func New(botName string, now func() time.Time) *Transport {
	if now == nil {
		now = time.Now
	}
	return &Transport{now: now, botName: botName, boxes: map[model.TelegramChatID][]model.SentMessage{},
		admins: map[model.TelegramChatID]map[model.TelegramUserID]bool{}, cleared: map[int64]bool{},
		commands: map[string][]model.BotCommand{}, titles: map[model.TelegramChatID]string{}}
}

// SetUpdateBase задаёт начало счётчика `update_id`: прогоны на одной БД не должны повторять номера, иначе бот
// отбросит входящие как дубли (дедупликация по `update_id`). Имитатор берёт время запуска в миллисекундах.
func (t *Transport) SetUpdateBase(id int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextUpd = id
}

// Username — имя бота без @ для ссылок и разбора `/cmd@bot`.
func (t *Transport) Username() string { return t.botName }

// SetAdmin объявляет пользователя администратором (владельцем) группы: `IsChatAdmin` отвечает true.
func (t *Transport) SetAdmin(chat model.TelegramChatID, user model.TelegramUserID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.admins[chat] == nil {
		t.admins[chat] = map[model.TelegramUserID]bool{}
	}
	t.admins[chat][user] = true
}

// SetTitle задаёт название группы для `my_chat_member`.
func (t *Transport) SetTitle(chat model.TelegramChatID, title string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.titles[chat] = title
}

// Messages возвращает сообщения чата с номером больше after в порядке отправки.
func (t *Transport) Messages(chat model.TelegramChatID, after int64) []model.SentMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []model.SentMessage
	for _, m := range t.boxes[chat] {
		if m.ID > after {
			out = append(out, m)
		}
	}
	return out
}

// Cleared сообщает, сняты ли кнопки с сообщения.
func (t *Transport) Cleared(messageID int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cleared[messageID]
}

// Commands — меню команд области: "private", "group" или "member:<chat>:<user>"; nil — не задано или снято.
func (t *Transport) Commands(scope string) []model.BotCommand {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.commands[scope]
}

func (t *Transport) put(chat model.TelegramChatID, text model.FormattedText, kb model.Keyboard, rk *model.ReplyKeyboard) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextMsg++
	t.boxes[chat] = append(t.boxes[chat], model.SentMessage{ID: t.nextMsg, Chat: chat, Text: text, Keyboard: kb, Reply: rk, SentAt: t.now()})
	return t.nextMsg
}

// SendText — ответ на команду без кнопок (`bot.Messenger`).
func (t *Transport) SendText(_ context.Context, chat model.TelegramChatID, text model.FormattedText) error {
	t.put(chat, text, nil, nil)
	return nil
}

// SendKeyboard — текст с inline-кнопками (`bot.Messenger`).
func (t *Transport) SendKeyboard(_ context.Context, chat model.TelegramChatID, text model.FormattedText, kb model.Keyboard) error {
	t.put(chat, text, kb, nil)
	return nil
}

// SendReplyKeyboard — текст с reply-клавиатурой лички или её снятием (`bot.Messenger`).
func (t *Transport) SendReplyKeyboard(_ context.Context, chat model.TelegramChatID, text model.FormattedText, rk model.ReplyKeyboard) error {
	t.put(chat, text, nil, &rk)
	return nil
}

// ClearKeyboard снимает кнопки с сообщения (`bot.Messenger`).
func (t *Transport) ClearKeyboard(_ context.Context, _ model.TelegramChatID, messageID int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleared[messageID] = true
	return nil
}

// Send — отправка воркером outbox с возвратом номера сообщения (`delivery.Sender`).
func (t *Transport) Send(_ context.Context, chat model.TelegramChatID, text model.FormattedText, kb model.Keyboard) (int64, error) {
	return t.put(chat, text, kb, nil), nil
}

// IsChatAdmin — права пользователя в группе по `SetAdmin` (`bot.ChatAPI`).
func (t *Transport) IsChatAdmin(_ context.Context, chat model.TelegramChatID, user model.TelegramUserID) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.admins[chat][user], nil
}

// CreateInviteLink — ссылка-заглушка; реальной группы нет (`bot.ChatAPI`).
func (t *Transport) CreateInviteLink(_ context.Context, chat model.TelegramChatID) (string, error) {
	return fmt.Sprintf("https://t.me/+sim%d", -int64(chat)), nil
}

// SetCommands запоминает меню личек и групп (`bot.ChatAPI`).
func (t *Transport) SetCommands(_ context.Context, private, group []model.BotCommand) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.commands["private"], t.commands["group"] = private, group
	return nil
}

// SetMemberCommands запоминает меню участника чата; пустой список снимает его (`bot.ChatAPI`).
func (t *Transport) SetMemberCommands(_ context.Context, chat model.TelegramChatID, user model.TelegramUserID, cmds []model.BotCommand) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := fmt.Sprintf("member:%d:%d", chat, user)
	if len(cmds) == 0 {
		delete(t.commands, key)
		return nil
	}
	t.commands[key] = cmds
	return nil
}

func (t *Transport) updateID() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextUpd++
	return t.nextUpd
}

// Text собирает входящее текстовое сообщение пользователя в чат: команда и аргументы разбираются как в боевом
// клиенте (`/cmd@bot` своего бота принимается, чужого — отбрасывается в обычный текст без команды).
func (t *Transport) Text(chat model.TelegramChatID, kind model.ChatType, from model.TelegramUser, text string) model.Incoming {
	in := model.Incoming{UpdateID: t.updateID(), ChatID: chat, ChatType: kind, From: from, Text: text, ReceivedAt: t.now()}
	in.Command, in.Args = parseCommand(text, t.botName)
	return in
}

// Press собирает нажатие inline-кнопки сообщения: данные кнопки разбираются в команду или ответ шага диалога.
func (t *Transport) Press(chat model.TelegramChatID, kind model.ChatType, from model.TelegramUser, messageID int64, data string) model.Incoming {
	command, args, step, text := model.ParseCallbackData(data)
	id := t.updateID()
	return model.Incoming{UpdateID: id, ChatID: chat, ChatType: kind, From: from, Text: text, Command: command, Args: args,
		ReceivedAt: t.now(), Callback: &model.Callback{ID: fmt.Sprintf("cb%d", id), MessageID: messageID, Step: step}}
}

// ChatMember собирает изменение статуса бота в группе (`my_chat_member`): добавление администратором — `BotAdmin`.
func (t *Transport) ChatMember(chat model.TelegramChatID, kind model.ChatType, from model.TelegramUser, status model.BotStatus) model.ChatMemberChange {
	t.mu.Lock()
	title := t.titles[chat]
	t.mu.Unlock()
	return model.ChatMemberChange{UpdateID: t.updateID(), ChatID: chat, ChatType: kind, ChatTitle: title, From: from, NewStatus: status, ReceivedAt: t.now()}
}

// parseCommand выделяет команду в начале текста: `/cmd`, `/cmd@bot` своего бота; `@other` — не команда.
func parseCommand(text, botName string) (command, args string) {
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	if head == "" {
		return "", ""
	}
	if name, mention, ok := strings.Cut(head, "@"); ok {
		if !strings.EqualFold(mention, botName) {
			return "", ""
		}
		head = name
	}
	for _, r := range head {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return "", ""
		}
	}
	return strings.ToLower(head), strings.TrimSpace(rest)
}
