// Команда bot: сборка зависимостей и запуск бота A Tale Together.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"taletogether/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

// run собирает зависимости и ждёт сигнала остановки.
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

	logger.Info("bot starting", slog.String("env", cfg.AppEnv))
	// TODO(этап 1): адаптеры Postgres и Telegram, сценарии app, планировщик.
	<-ctx.Done()
	logger.Info("bot stopped")
	return nil
}
