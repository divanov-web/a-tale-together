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

	"taletogether/cmd/internal/stack"
	"taletogether/internal/adapter/chatlog"
	"taletogether/internal/adapter/telegram"
	"taletogether/internal/config"
	"taletogether/internal/model"
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

	tg, err := telegram.New(ctx, telegram.Settings{Token: cfg.TelegramBotToken}, logger)
	if err != nil {
		return err
	}
	var journal stack.Journal // nil — журнал чатов выключен (ADR-0012)
	if cfg.ChatLogDir != "" {
		w := chatlog.New(chatlog.Settings{Dir: cfg.ChatLogDir, Retention: time.Duration(cfg.ChatLogRetention) * 24 * time.Hour}, logger)
		if n, err := w.Cleanup(time.Now()); err != nil {
			logger.Warn("chat log cleanup failed", slog.Any("err", err))
		} else {
			logger.Info("chat log enabled", slog.String("dir", cfg.ChatLogDir), slog.Int("removed_files", n))
		}
		journal = w
	}
	providers, err := stack.Providers(cfg.LLM)
	if err != nil {
		return err
	}
	st, err := stack.Build(ctx, stack.Options{
		Version: version, Env: cfg.AppEnv, DSN: cfg.DatabaseURL, MigrateOnStart: cfg.MigrateOnStart,
		SchedulerTick: cfg.SchedulerTick, OutboxTick: cfg.OutboxTick, OwnerID: model.TelegramUserID(cfg.BotOwnerID), GuideURL: cfg.GuideURL,
		AutoFaction: model.FactionGood, // TODO: на время тестовых прогонов все персонажи во фракции добрых; убрать, чтобы вернуть выбор
		Providers:   providers, Journal: journal,
	}, tg, logger)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Commands.RegisterMenu(ctx); err != nil {
		logger.Warn("command menu not registered", slog.Any("err", err))
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return tg.Run(gctx, st.Commands) })
	g.Go(func() error { return st.Run(gctx) })

	err = g.Wait()
	logger.Info("bot stopped")
	return err
}
