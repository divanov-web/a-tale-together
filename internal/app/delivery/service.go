// Package delivery — воркер outbox (ADR-0006): берёт ожидающие сообщения, отправляет в Telegram,
// хранит `telegram_message_id`, повторяет с задержкой; неопределённый исход не переотправляет.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"taletogether/internal/model"
)

// Store — очередь исходящих; реализует `adapter/postgres`.
type Store interface {
	ClaimOutbox(ctx context.Context, now time.Time, stale time.Duration, limit int) ([]model.OutboxMessage, error)
	FinishOutbox(ctx context.Context, m model.OutboxMessage) error
}

// Sender — отправка текста с возвратом идентификатора сообщения; реализует `adapter/telegram`.
type Sender interface {
	Send(ctx context.Context, chat model.TelegramChatID, text string) (int64, error)
}

// Clock — источник времени.
type Clock interface {
	Now() time.Time
}

// Journal — журнал чатов (ADR-0012): каждая попытка отправки с `outbox_id` и `telegram_message_id`; реализует `adapter/chatlog`.
type Journal interface {
	Write(ctx context.Context, rec model.ChatLogRecord)
}

// Settings — параметры воркера.
type Settings struct {
	Interval    time.Duration // период опроса очереди
	Batch       int           // сообщений за проход
	MaxAttempts int           // попыток при временных ошибках до статуса failed
	Stale       time.Duration // через сколько «sending» без ответа считается неопределённым исходом
}

// Service — воркер доставки.
type Service struct {
	store    Store
	sender   Sender
	clock    Clock
	settings Settings
	logger   *slog.Logger
	// Journal — журнал чатов; nil — выключен.
	Journal Journal
}

// New собирает воркер; нулевые настройки заменяются значениями по умолчанию.
func New(store Store, sender Sender, clock Clock, s Settings, logger *slog.Logger) *Service {
	if s.Interval <= 0 {
		s.Interval = 3 * time.Second
	}
	if s.Batch <= 0 {
		s.Batch = 20
	}
	if s.MaxAttempts <= 0 {
		s.MaxAttempts = 30 // с паузой до 5 минут — около двух часов недоступности Telegram
	}
	if s.Stale <= 0 {
		s.Stale = 5 * time.Minute
	}
	return &Service{store: store, sender: sender, clock: clock, settings: s, logger: logger}
}

// Run доставляет сообщения по интервалу до отмены контекста.
func (s *Service) Run(ctx context.Context) error {
	s.logger.Info("delivery started", slog.Duration("interval", s.settings.Interval))
	ticker := time.NewTicker(s.settings.Interval)
	defer ticker.Stop()
	for {
		if _, err := s.Deliver(ctx); err != nil && ctx.Err() == nil {
			s.logger.Error("delivery pass", slog.Any("err", err))
		}
		select {
		case <-ctx.Done():
			s.logger.Info("delivery stopped")
			return nil
		case <-ticker.C:
		}
	}
}

// Deliver выполняет один проход: отправляет до Batch сообщений; возвращает число отправленных.
// Исход попытки (ADR-0013): окончательный отказ Telegram (`model.ErrSendRejected`) → `failed` сразу; просьба подождать
// (`model.RetryAfterError`) → отсрочка этого и остальных сообщений прохода; прочие ошибки → повтор с паузой,
// после MaxAttempts → `failed`.
func (s *Service) Deliver(ctx context.Context) (int, error) {
	now := s.clock.Now()
	msgs, err := s.store.ClaimOutbox(ctx, now, s.settings.Stale, s.settings.Batch)
	if err != nil {
		return 0, fmt.Errorf("delivery.Deliver: %w", err)
	}
	sent := 0
	var pause time.Duration // Telegram попросил подождать: остальные сообщения прохода откладываются без отправки
	for _, m := range msgs {
		var err error
		if pause > 0 {
			m.Status, m.SendAfter, m.LastError = model.OutboxPending, now.Add(pause), "отложено: лимит Telegram"
		} else if id, sendErr := s.sender.Send(ctx, m.TelegramChatID, m.Text); sendErr != nil {
			err = sendErr
			m.LastError = sendErr.Error()
			var retry *model.RetryAfterError
			switch {
			case errors.Is(sendErr, model.ErrSendRejected):
				m.Status = model.OutboxFailed
				s.logger.Error("outbox message rejected", slog.Int64("game_id", int64(m.GameID)), slog.Int64("outbox_id", int64(m.ID)), slog.Any("err", sendErr))
			case errors.As(sendErr, &retry):
				pause = retry.After
				m.Status, m.SendAfter = model.OutboxPending, now.Add(pause)
				s.logger.Warn("outbox rate limited", slog.Int64("game_id", int64(m.GameID)), slog.Int64("outbox_id", int64(m.ID)), slog.Duration("retry_after", pause))
			case m.Attempts >= s.settings.MaxAttempts:
				m.Status = model.OutboxFailed
				s.logger.Error("outbox message failed", slog.Int64("game_id", int64(m.GameID)), slog.Int64("outbox_id", int64(m.ID)), slog.Int("attempts", m.Attempts), slog.Any("err", sendErr))
			default:
				m.Status, m.SendAfter = model.OutboxPending, now.Add(backoff(m.Attempts))
				s.logger.Warn("outbox send retry", slog.Int64("game_id", int64(m.GameID)), slog.Int64("outbox_id", int64(m.ID)), slog.Int("attempt", m.Attempts), slog.Any("err", sendErr))
			}
		} else {
			m.Status = model.OutboxSent
			m.TelegramMessageID = &id
			t := now
			m.SentAt = &t
			m.LastError = ""
			sent++
		}
		if err := s.store.FinishOutbox(ctx, m); err != nil {
			return sent, fmt.Errorf("delivery.Deliver: %w", err)
		}
		if pause == 0 || err != nil {
			s.journal(ctx, m, now, err)
		}
	}
	return sent, nil
}

// journal пишет попытку отправки в журнал чата получателя.
func (s *Service) journal(ctx context.Context, m model.OutboxMessage, now time.Time, sendErr error) {
	if s.Journal == nil {
		return
	}
	rec := model.ChatLogRecord{TS: now, Kind: model.ChatLogOut, Chat: m.TelegramChatID, GameID: m.GameID, OutboxID: m.ID, OutboxKind: m.Kind, Part: m.Part, Attempt: m.Attempts, Text: m.Text}
	if m.RoundID != nil {
		rec.RoundID = *m.RoundID
	}
	if sendErr != nil {
		rec.Error = sendErr.Error()
	} else if m.TelegramMessageID != nil {
		rec.MessageID = *m.TelegramMessageID
	}
	s.Journal.Write(ctx, rec)
}

// backoff — пауза перед повтором временной ошибки: 5с, 10с, 20с… не больше 5 минут.
func backoff(attempt int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < attempt && d < 5*time.Minute; i++ {
		d *= 2
	}
	return min(d, 5*time.Minute)
}
