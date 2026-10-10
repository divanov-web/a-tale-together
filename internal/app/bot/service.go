// Package bot — сценарий обработки команд Telegram: дедупликация, учёт пользователя, маршрутизация команд.
package bot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"taletogether/internal/model"
)

// Store — хранилище, нужное обработчику команд; реализует `adapter/postgres`.
type Store interface {
	MarkUpdate(ctx context.Context, updateID int64) (bool, error)
	UpsertUser(ctx context.Context, u model.TelegramUser) error
}

// Sender отправляет ответы; реализует `adapter/telegram`.
type Sender interface {
	SendText(ctx context.Context, chat model.ChatID, text string) error
}

// Clock — источник времени; в тестах подменяется.
type Clock interface {
	Now() time.Time
}

// Settings — настройки сценария.
type Settings struct {
	Version string // Версия сборки для ответа на /ping
	Env     string // dev|prod
}

// Service обрабатывает входящие сообщения. Ответы на команды отправляются синхронно (ADR-0006).
type Service struct {
	settings Settings
	store    Store
	sender   Sender
	clock    Clock
	logger   *slog.Logger
}

// New собирает сервис команд.
func New(s Settings, store Store, sender Sender, clock Clock, logger *slog.Logger) *Service {
	return &Service{settings: s, store: store, sender: sender, clock: clock, logger: logger}
}

// Handle дедуплицирует update, фиксирует пользователя и выполняет команду; не-команды игнорирует.
func (s *Service) Handle(ctx context.Context, in model.Incoming) error {
	fresh, err := s.store.MarkUpdate(ctx, in.UpdateID)
	if err != nil {
		return fmt.Errorf("bot.Handle: %w", err)
	}
	if !fresh {
		s.logger.Debug("duplicate update skipped", slog.Int64("update_id", in.UpdateID))
		return nil
	}
	if in.From.ID != 0 {
		if err := s.store.UpsertUser(ctx, in.From); err != nil {
			return fmt.Errorf("bot.Handle: %w", err)
		}
	}
	if in.Command == "" {
		return nil
	}
	s.logger.Info("command", slog.String("command", in.Command), slog.Int64("chat_id", int64(in.ChatID)), slog.Int64("update_id", in.UpdateID))

	switch in.Command {
	case "ping":
		return s.ping(ctx, in)
	default:
		return nil // неизвестные команды молча игнорируются: в группе могут быть команды других ботов
	}
}

// ping отвечает версией и временем сервера: проверка, что бот жив, в группе и в личке.
func (s *Service) ping(ctx context.Context, in model.Incoming) error {
	text := fmt.Sprintf("pong\nверсия: %s (%s)\nвремя: %s",
		s.settings.Version, s.settings.Env, s.clock.Now().Format(time.RFC3339))
	if err := s.sender.SendText(ctx, in.ChatID, text); err != nil {
		return fmt.Errorf("bot.ping: %w", err)
	}
	return nil
}
