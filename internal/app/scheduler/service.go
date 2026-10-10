// Package scheduler — тик планировщика раундов: периодически вызывает `round.Tick` до отмены контекста.
package scheduler

import (
	"context"
	"log/slog"
	"time"
)

// Rounds — шаг планировщика; реализует `app/round`.
type Rounds interface {
	Tick(ctx context.Context, now time.Time) error
}

// Clock — источник времени.
type Clock interface {
	Now() time.Time
}

// Service — цикл планировщика.
type Service struct {
	rounds   Rounds
	clock    Clock
	interval time.Duration
	logger   *slog.Logger
}

// New собирает планировщик с интервалом тика.
func New(rounds Rounds, clock Clock, interval time.Duration, logger *slog.Logger) *Service {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &Service{rounds: rounds, clock: clock, interval: interval, logger: logger}
}

// Run выполняет тик сразу и далее по интервалу; ошибки тика пишутся в лог и цикл продолжается.
func (s *Service) Run(ctx context.Context) error {
	s.logger.Info("scheduler started", slog.Duration("interval", s.interval))
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		if err := s.rounds.Tick(ctx, s.clock.Now()); err != nil && ctx.Err() == nil {
			s.logger.Error("scheduler tick", slog.Any("err", err))
		}
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopped")
			return nil
		case <-ticker.C:
		}
	}
}
