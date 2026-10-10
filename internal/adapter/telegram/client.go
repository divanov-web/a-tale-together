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

// Handler принимает входящие сообщения; реализует `app/bot`. Передаётся в Run, чтобы разорвать цикл клиент ↔ сценарий.
type Handler interface {
	Handle(ctx context.Context, in model.Incoming) error
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
func (c *Client) SendText(ctx context.Context, chat model.ChatID, text string) error {
	_, err := c.bot.SendMessage(ctx, &bot.SendMessageParams{ChatID: int64(chat), Text: text})
	if err != nil {
		return fmt.Errorf("telegram.SendText: chat %d: %w", chat, err)
	}
	return nil
}

// onUpdate превращает текстовое сообщение в model.Incoming и передаёт обработчику; остальные update игнорирует.
func (c *Client) onUpdate(ctx context.Context, _ *bot.Bot, u *models.Update) {
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
		ChatID:     model.ChatID(m.Chat.ID),
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

// onError пишет ошибки библиотеки в лог; 409 означает второй экземпляр бота с тем же токеном.
func (c *Client) onError(err error) {
	if errors.Is(err, bot.ErrorConflict) {
		c.logger.Error("telegram 409 Conflict: another bot instance is polling with this token; stop it", slog.Any("err", err))
		return
	}
	c.logger.Error("telegram library error", slog.Any("err", err))
}
