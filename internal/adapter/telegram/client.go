// Package telegram — адаптер Telegram Bot API на go-telegram/bot (ADR-0004): long polling, отправка текста.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"taletogether/internal/model"
)

// Settings — настройки подключения к Telegram.
type Settings struct {
	Token string // Токен бота от BotFather
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
}

// New проверяет токен запросом getMe и готовит клиент; polling начинается в Run.
func New(ctx context.Context, s Settings, logger *slog.Logger) (*Client, error) {
	c := &Client{logger: logger}
	b, err := bot.New(s.Token,
		bot.WithSkipGetMe(),
		bot.WithAllowedUpdates(bot.AllowedUpdates{"message", "my_chat_member"}),
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

// SendText отправляет обычное текстовое сообщение. Деление длинных текстов — пункт плана 09.
func (c *Client) SendText(ctx context.Context, chat model.TelegramChatID, text string) error {
	_, err := c.bot.SendMessage(ctx, &bot.SendMessageParams{ChatID: int64(chat), Text: text})
	if err != nil {
		return fmt.Errorf("telegram.SendText: chat %d: %w", chat, err)
	}
	return nil
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

// onUpdate превращает update в model.Incoming или model.ChatMemberChange и передаёт обработчику; остальные update игнорирует.
func (c *Client) onUpdate(ctx context.Context, _ *bot.Bot, u *models.Update) {
	if u.MyChatMember != nil {
		ch := c.chatMemberChange(u)
		if err := c.handler.HandleChatMember(ctx, ch); err != nil {
			c.logger.Error("handle my_chat_member failed", slog.Int64("update_id", u.ID), slog.Any("err", err))
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
