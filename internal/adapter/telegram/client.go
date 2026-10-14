// Package telegram — адаптер Telegram Bot API на go-telegram/bot (ADR-0004): long polling, отправка текста, inline-кнопок (ADR-0015) и reply-клавиатуры лички (ADR-0021).
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf16"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"taletogether/internal/model"
)

// Settings — настройки подключения к Telegram.
type Settings struct {
	Token string           // Токен бота от BotFather
	Now   func() time.Time // Время получения нажатия кнопки (у callback_query нет даты); nil — time.Now
}

// Handler принимает входящие сообщения и изменения статуса бота в чатах; реализует `app/bot`.
// Передаётся в Run, чтобы разорвать цикл клиент ↔ сценарий.
type Handler interface {
	Handle(ctx context.Context, in model.Incoming) error
	HandleChatMember(ctx context.Context, ch model.ChatMemberChange) error
}

// Client — обёртка над go-telegram/bot: превращает update в `model.Incoming` и отправляет ответы.
type Client struct {
	bot      *bot.Bot
	handler  Handler
	logger   *slog.Logger
	username string
	now      func() time.Time
}

// New проверяет токен запросом getMe и готовит клиент; polling начинается в Run.
func New(ctx context.Context, s Settings, logger *slog.Logger) (*Client, error) {
	c := &Client{logger: logger, now: s.Now}
	if c.now == nil {
		c.now = time.Now
	}
	b, err := bot.New(s.Token,
		bot.WithSkipGetMe(),
		bot.WithAllowedUpdates(bot.AllowedUpdates{"message", "my_chat_member", "callback_query"}),
		bot.WithDefaultHandler(c.onUpdate),
		bot.WithErrorsHandler(c.onError),
		bot.WithDebugHandler(func(format string, args ...any) { logger.Debug(fmt.Sprintf(format, args...)) }),
	)
	if err != nil {
		return nil, fmt.Errorf("telegram.New: %w", err)
	}
	me, err := b.GetMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("telegram.New: getMe: %w", err)
	}
	c.bot = b
	c.username = me.Username
	return c, nil
}

// Username — имя бота без @; нужно для разбора команд вида `/ping@bot` в группах.
func (c *Client) Username() string { return c.username }

// Run ведёт long polling до отмены контекста, передавая входящие сообщения обработчику.
func (c *Client) Run(ctx context.Context, h Handler) error {
	c.handler = h
	c.logger.Info("telegram polling started", slog.String("bot", c.username))
	c.bot.Start(ctx)
	c.logger.Info("telegram polling stopped")
	return nil
}

// SendText отправляет текстовое сообщение (ответы на команды, ADR-0006), разметка — entities (ADR-0029). Длинные
// игровые тексты делит сценарий при постановке в outbox (ADR-0011).
func (c *Client) SendText(ctx context.Context, chat model.TelegramChatID, text model.FormattedText) error {
	if _, err := c.SendFormatted(ctx, chat, text, nil); err != nil {
		return fmt.Errorf("telegram.SendText: %w", err)
	}
	return nil
}

// SendKeyboard отправляет текст с inline-кнопками под ним (ADR-0015); пустая клавиатура — обычное сообщение.
func (c *Client) SendKeyboard(ctx context.Context, chat model.TelegramChatID, text model.FormattedText, kb model.Keyboard) error {
	if _, err := c.SendFormatted(ctx, chat, text, kb); err != nil {
		return fmt.Errorf("telegram.SendKeyboard: %w", err)
	}
	return nil
}

// SendReplyKeyboard отправляет текст и ставит в личке постоянную клавиатуру под полем ввода или снимает её (ADR-0021).
func (c *Client) SendReplyKeyboard(ctx context.Context, chat model.TelegramChatID, text model.FormattedText, rk model.ReplyKeyboard) error {
	params := &bot.SendMessageParams{ChatID: int64(chat), Text: text.Text, Entities: entities(text), ReplyMarkup: replyMarkup(rk)}
	if _, err := c.bot.SendMessage(ctx, params); err != nil {
		return fmt.Errorf("telegram.SendReplyKeyboard: chat %d: %w", chat, classify(err))
	}
	return nil
}

// Send отправляет простой текст с кнопками (nil — без них) и возвращает идентификатор сообщения Telegram; используется
// воркером outbox.
func (c *Client) Send(ctx context.Context, chat model.TelegramChatID, text string, kb model.Keyboard) (int64, error) {
	return c.SendFormatted(ctx, chat, model.Plain(text), kb)
}

// SendFormatted отправляет текст с разметкой (ADR-0029) и кнопками (nil — без них) и возвращает идентификатор
// сообщения Telegram. Ошибки классифицируются для повторов (ADR-0013): окончательный отказ Telegram →
// `model.ErrSendRejected`, 429 → `model.RetryAfterError`, остальное (сеть, 5xx, таймаут) — временная ошибка.
func (c *Client) SendFormatted(ctx context.Context, chat model.TelegramChatID, text model.FormattedText, kb model.Keyboard) (int64, error) {
	params := &bot.SendMessageParams{ChatID: int64(chat), Text: text.Text, Entities: entities(text)}
	if len(kb) > 0 {
		params.ReplyMarkup = markup(kb)
	}
	m, err := c.bot.SendMessage(ctx, params)
	if err != nil {
		return 0, fmt.Errorf("telegram.Send: chat %d: %w", chat, classify(err))
	}
	return int64(m.ID), nil
}

// entities переводит разметку модели в entities Telegram: смещения и длины из рун в единицы UTF-16 (ADR-0029).
// Отрезок за границами текста пропускается, текст уходит без него.
func entities(text model.FormattedText) []models.MessageEntity {
	if len(text.Entities) == 0 {
		return nil
	}
	pos := make([]int, 0, len(text.Text)+1) // позиция каждой руны в UTF-16 и позиция конца
	u := 0
	for _, r := range text.Text {
		pos = append(pos, u)
		if utf16.RuneLen(r) == 2 {
			u += 2
		} else {
			u++
		}
	}
	pos = append(pos, u)
	var out []models.MessageEntity
	for _, e := range text.Entities {
		if e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length >= len(pos) {
			continue
		}
		start, end := pos[e.Offset], pos[e.Offset+e.Length]
		out = append(out, models.MessageEntity{Type: models.MessageEntityType(e.Kind), Offset: start, Length: end - start})
	}
	return out
}

// ClearKeyboard снимает кнопки с отправленного сообщения после нажатия (ADR-0015). Отказ Telegram (кнопок уже нет,
// сообщение старое или удалено) не ошибка: клавиатуры и так нет.
func (c *Client) ClearKeyboard(ctx context.Context, chat model.TelegramChatID, messageID int64) error {
	_, err := c.bot.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{ChatID: int64(chat), MessageID: int(messageID)})
	if err != nil && !errors.Is(err, bot.ErrorBadRequest) {
		return fmt.Errorf("telegram.ClearKeyboard: chat %d message %d: %w", chat, messageID, err)
	}
	return nil
}

// markup собирает inline-клавиатуру Telegram из модели: кнопка с данными нажатия или ссылкой.
func markup(kb model.Keyboard) *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, len(kb))
	for _, row := range kb {
		r := make([]models.InlineKeyboardButton, 0, len(row))
		for _, b := range row {
			r = append(r, models.InlineKeyboardButton{Text: b.Text, CallbackData: b.Data, URL: b.URL})
		}
		rows = append(rows, r)
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// replyMarkup собирает reply-клавиатуру Telegram: постоянную и сжатую по высоте; без рядов — снятие клавиатуры.
func replyMarkup(rk model.ReplyKeyboard) models.ReplyMarkup {
	if len(rk.Rows) == 0 {
		return &models.ReplyKeyboardRemove{RemoveKeyboard: true}
	}
	rows := make([][]models.KeyboardButton, 0, len(rk.Rows))
	for _, row := range rk.Rows {
		r := make([]models.KeyboardButton, 0, len(row))
		for _, text := range row {
			r = append(r, models.KeyboardButton{Text: text})
		}
		rows = append(rows, r)
	}
	return &models.ReplyKeyboardMarkup{Keyboard: rows, IsPersistent: true, ResizeKeyboard: true, InputFieldPlaceholder: rk.Placeholder}
}

// classify сопоставляет ошибку библиотеки с сентинелами модели: отказы 400/401/403/404 и переезд чата — окончательные,
// 429 — ожидание `retry_after` (без него минута).
func classify(err error) error {
	var tooMany *bot.TooManyRequestsError
	if errors.As(err, &tooMany) {
		after := time.Duration(tooMany.RetryAfter) * time.Second
		if after <= 0 {
			after = time.Minute
		}
		return &model.RetryAfterError{After: after, Err: err}
	}
	for _, permanent := range []error{bot.ErrorForbidden, bot.ErrorBadRequest, bot.ErrorNotFound, bot.ErrorUnauthorized} {
		if errors.Is(err, permanent) {
			return fmt.Errorf("%w: %w", model.ErrSendRejected, err)
		}
	}
	if bot.IsMigrateError(err) {
		return fmt.Errorf("%w: %w", model.ErrSendRejected, err)
	}
	return err
}

// inviteLinkName — подпись ссылок-приглашений бота в списке ссылок группы.
const inviteLinkName = "A Tale Together"

// CreateInviteLink создаёт дополнительную ссылку-приглашение в группу (`createChatInviteLink`, ADR-0022); основная
// ссылка группы не отзывается. Нужны права администратора с приглашением пользователей.
func (c *Client) CreateInviteLink(ctx context.Context, chat model.TelegramChatID) (string, error) {
	l, err := c.bot.CreateChatInviteLink(ctx, &bot.CreateChatInviteLinkParams{ChatID: int64(chat), Name: inviteLinkName})
	if err != nil {
		return "", fmt.Errorf("telegram.CreateInviteLink: chat %d: %w", chat, err)
	}
	return l.InviteLink, nil
}

// IsChatAdmin сообщает, является ли пользователь создателем или администратором группы Telegram.
func (c *Client) IsChatAdmin(ctx context.Context, chat model.TelegramChatID, user model.TelegramUserID) (bool, error) {
	m, err := c.bot.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: int64(chat), UserID: int64(user)})
	if err != nil {
		return false, fmt.Errorf("telegram.IsChatAdmin: chat %d: %w", chat, err)
	}
	return m.Type == models.ChatMemberTypeOwner || m.Type == models.ChatMemberTypeAdministrator, nil
}

// SetCommands регистрирует меню команд отдельно для личек и групп (setMyCommands).
func (c *Client) SetCommands(ctx context.Context, private, group []model.BotCommand) error {
	scopes := []struct {
		scope    models.BotCommandScope
		commands []model.BotCommand
	}{
		{&models.BotCommandScopeAllPrivateChats{}, private},
		{&models.BotCommandScopeAllGroupChats{}, group},
		{&models.BotCommandScopeAllChatAdministrators{}, group},
	}
	for _, s := range scopes {
		cmds := make([]models.BotCommand, 0, len(s.commands))
		for _, bc := range s.commands {
			cmds = append(cmds, models.BotCommand{Command: bc.Name, Description: bc.Description})
		}
		if _, err := c.bot.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: cmds, Scope: s.scope}); err != nil {
			return fmt.Errorf("telegram.SetCommands: %w", err)
		}
	}
	return nil
}

// SetMemberCommands регистрирует меню команд одному участнику конкретного чата (область `chat_member`, B20);
// пустой список снимает его (deleteMyCommands), и участник видит меню группы для всех.
func (c *Client) SetMemberCommands(ctx context.Context, chat model.TelegramChatID, user model.TelegramUserID, cmds []model.BotCommand) error {
	scope := &models.BotCommandScopeChatMember{ChatID: int64(chat), UserID: int64(user)}
	if len(cmds) == 0 {
		if _, err := c.bot.DeleteMyCommands(ctx, &bot.DeleteMyCommandsParams{Scope: scope}); err != nil {
			return fmt.Errorf("telegram.SetMemberCommands: delete: %w", err)
		}
		return nil
	}
	out := make([]models.BotCommand, 0, len(cmds))
	for _, bc := range cmds {
		out = append(out, models.BotCommand{Command: bc.Name, Description: bc.Description})
	}
	if _, err := c.bot.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: out, Scope: scope}); err != nil {
		return fmt.Errorf("telegram.SetMemberCommands: %w", err)
	}
	return nil
}

// onUpdate превращает update в model.Incoming или model.ChatMemberChange и передаёт обработчику; остальные update игнорирует.
// Нажатие кнопки обрабатывается как сообщение с данными кнопки, затем запрос закрывается answerCallbackQuery без текста.
func (c *Client) onUpdate(ctx context.Context, _ *bot.Bot, u *models.Update) {
	if u.MyChatMember != nil {
		ch := c.chatMemberChange(u)
		if err := c.handler.HandleChatMember(ctx, ch); err != nil {
			c.logger.Error("handle my_chat_member failed", slog.Int64("update_id", u.ID), slog.Any("err", err))
		}
		return
	}
	if q := u.CallbackQuery; q != nil {
		if in, ok := c.callback(u); ok {
			if err := c.handler.Handle(ctx, in); err != nil {
				c.logger.Error("handle callback failed", slog.Int64("update_id", in.UpdateID), slog.Any("err", err))
			}
		}
		if _, err := c.bot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID}); err != nil {
			c.logger.Warn("answer callback failed", slog.Int64("update_id", u.ID), slog.Any("err", err))
		}
		return
	}
	in, ok := c.incoming(u)
	if !ok {
		return
	}
	if err := c.handler.Handle(ctx, in); err != nil {
		c.logger.Error("handle update failed", slog.Int64("update_id", in.UpdateID), slog.Any("err", err))
	}
}

// incoming собирает model.Incoming из update с текстовым сообщением.
func (c *Client) incoming(u *models.Update) (model.Incoming, bool) {
	m := u.Message
	if m == nil || m.Text == "" {
		return model.Incoming{}, false
	}
	in := model.Incoming{
		UpdateID:   u.ID,
		ChatID:     model.TelegramChatID(m.Chat.ID),
		ChatType:   model.ChatType(m.Chat.Type),
		Text:       m.Text,
		ReceivedAt: time.Unix(int64(m.Date), 0),
	}
	if m.From != nil {
		in.From = model.TelegramUser{
			ID:           model.TelegramUserID(m.From.ID),
			Username:     m.From.Username,
			FirstName:    m.From.FirstName,
			LanguageCode: m.From.LanguageCode,
		}
	}
	in.Command, in.Args = parseCommand(m.Text, m.Entities, c.username)
	return in, true
}

// callback собирает model.Incoming из нажатия кнопки (ADR-0015): чат и сообщение из callback_query, команда или
// ответ шага диалога из данных кнопки. Нажатие на недоступном сообщении (без чата) игнорируется.
func (c *Client) callback(u *models.Update) (model.Incoming, bool) {
	q := u.CallbackQuery
	var chat models.Chat
	var messageID int
	switch {
	case q.Message.Message != nil:
		chat, messageID = q.Message.Message.Chat, q.Message.Message.ID
	case q.Message.InaccessibleMessage != nil:
		chat, messageID = q.Message.InaccessibleMessage.Chat, q.Message.InaccessibleMessage.MessageID
	default:
		return model.Incoming{}, false
	}
	command, args, step, text := model.ParseCallbackData(q.Data)
	return model.Incoming{
		UpdateID:   u.ID,
		ChatID:     model.TelegramChatID(chat.ID),
		ChatType:   model.ChatType(chat.Type),
		From:       model.TelegramUser{ID: model.TelegramUserID(q.From.ID), Username: q.From.Username, FirstName: q.From.FirstName, LanguageCode: q.From.LanguageCode},
		Text:       text,
		Command:    command,
		Args:       args,
		ReceivedAt: c.now(),
		Callback:   &model.Callback{ID: q.ID, MessageID: int64(messageID), Step: step},
	}, true
}

// chatMemberChange собирает model.ChatMemberChange из update my_chat_member.
func (c *Client) chatMemberChange(u *models.Update) model.ChatMemberChange {
	m := u.MyChatMember
	status := model.BotLeft
	switch m.NewChatMember.Type {
	case models.ChatMemberTypeMember, models.ChatMemberTypeRestricted:
		status = model.BotMember
	case models.ChatMemberTypeAdministrator, models.ChatMemberTypeOwner:
		status = model.BotAdmin
	}
	return model.ChatMemberChange{
		UpdateID:  u.ID,
		ChatID:    model.TelegramChatID(m.Chat.ID),
		ChatType:  model.ChatType(m.Chat.Type),
		ChatTitle: m.Chat.Title,
		From: model.TelegramUser{
			ID: model.TelegramUserID(m.From.ID), Username: m.From.Username, FirstName: m.From.FirstName, LanguageCode: m.From.LanguageCode,
		},
		NewStatus:  status,
		ReceivedAt: time.Unix(int64(m.Date), 0),
	}
}

// onError пишет ошибки библиотеки в лог; 409 означает второй экземпляр бота с тем же токеном.
func (c *Client) onError(err error) {
	if errors.Is(err, bot.ErrorConflict) {
		c.logger.Error("telegram 409 Conflict: another bot instance is polling with this token; stop it", slog.Any("err", err))
		return
	}
	c.logger.Error("telegram library error", slog.Any("err", err))
}
