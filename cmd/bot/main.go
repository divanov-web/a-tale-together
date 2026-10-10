// Команда bot: сборка зависимостей и запуск бота A Tale Together.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"taletogether/internal/adapter/postgres"
	"taletogether/internal/adapter/telegram"
	"taletogether/internal/app/bot"
	"taletogether/internal/config"
)

// version подставляется при сборке: -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

// run собирает зависимости, запускает фоновые циклы и ждёт сигнала остановки.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("конфигурация: %w", err)
	}
	lvl, _ := cfg.SlogLevel()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("bot starting", slog.String("version", version), slog.String("env", cfg.AppEnv))

	store, err := postgres.New(ctx, postgres.Settings{DSN: cfg.DatabaseURL}, logger)
	if err != nil {
		return err
	}
	defer store.Close()
	if cfg.MigrateOnStart {
		if err := store.Migrate(ctx); err != nil {
			return err
		}
	}

	tg, err := telegram.New(ctx, telegram.Settings{Token: cfg.TelegramBotToken}, logger)
	if err != nil {
		return err
	}
	commands := bot.New(bot.Settings{Version: version, Env: cfg.AppEnv}, store, tg, systemClock{}, logger)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return tg.Run(gctx, commands) })
	// TODO(этап 2): планировщик раундов и воркер outbox.

	err = g.Wait()
	logger.Info("bot stopped")
	return err
}

// systemClock — системное время для сценариев.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
