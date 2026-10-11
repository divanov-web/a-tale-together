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

	"taletogether/internal/adapter/chatlog"
	"taletogether/internal/adapter/llm/anthropic"
	"taletogether/internal/adapter/llm/claudecli"
	"taletogether/internal/adapter/llm/openai"
	"taletogether/internal/adapter/postgres"
	"taletogether/internal/adapter/telegram"
	"taletogether/internal/app/bot"
	"taletogether/internal/app/chat"
	"taletogether/internal/app/delivery"
	"taletogether/internal/app/game"
	applm "taletogether/internal/app/llm"
	"taletogether/internal/app/round"
	"taletogether/internal/app/scheduler"
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

	clock := systemClock{}
	tg, err := telegram.New(ctx, telegram.Settings{Token: cfg.TelegramBotToken, Now: clock.Now}, logger)
	if err != nil {
		return err
	}
	var journal *chatlog.Writer // nil — журнал чатов выключен (ADR-0012)
	if cfg.ChatLogDir != "" {
		journal = chatlog.New(chatlog.Settings{Dir: cfg.ChatLogDir, Retention: time.Duration(cfg.ChatLogRetention) * 24 * time.Hour}, logger)
		if n, err := journal.Cleanup(clock.Now()); err != nil {
			logger.Warn("chat log cleanup failed", slog.Any("err", err))
		} else {
			logger.Info("chat log enabled", slog.String("dir", cfg.ChatLogDir), slog.Int("removed_files", n))
		}
	}
	chats := chat.New(store, logger)
	games := game.New(store, chats, clock, logger)
	if cfg.AppEnv == "dev" {
		games.Defaults.MinRoundAnswers = model.MinRoundAnswersDev // кворум 1 на время разработки
		games.MinRoundMinutes = model.MinRoundMinutesDev          // короткие раунды для прогонов в песочнице
	}
	rounds := round.New(store, games, clock, logger)
	rounds.AnnounceSkipped = cfg.AppEnv == "dev" // на время разработки пропуск раунда анонсируется в группу
	if journal != nil {
		rounds.Journal = journal
	}
	if cfg.LLM.ClaudeCLIWorkdir != "" {
		if err := os.MkdirAll(cfg.LLM.ClaudeCLIWorkdir, 0o755); err != nil {
			return fmt.Errorf("рабочая папка claude-cli: %w", err)
		}
	}
	providers := map[string]applm.Client{
		model.ProviderClaudeCLI: claudecli.New(claudecli.Settings{Path: cfg.LLM.ClaudeCLIPath, Workdir: cfg.LLM.ClaudeCLIWorkdir}),
		model.ProviderAnthropic: anthropic.New(),
		model.ProviderOpenAI:    openai.New(),
	}
	llm := applm.New(store, providers, clock, logger)
	rounds.LLM = llm // интерпретатор и рассказчик: два вызова на раунд (пункты 08, 09)
	commands := bot.New(bot.Settings{Version: version, Env: cfg.AppEnv, BotUsername: tg.Username(), OwnerID: model.TelegramUserID(cfg.BotOwnerID), GuideURL: cfg.GuideURL},
		store, store, tg, chats, games, rounds, llm, clock, logger)
	outbox := delivery.New(store, tg, clock, delivery.Settings{Interval: cfg.OutboxTick}, logger)
	if journal != nil {
		llm.Journal, commands.Journal, outbox.Journal = journal, journal, journal
	}
	if err := commands.RegisterMenu(ctx); err != nil {
		logger.Warn("command menu not registered", slog.Any("err", err))
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return tg.Run(gctx, commands) })
	g.Go(func() error { return scheduler.New(rounds, clock, cfg.SchedulerTick, logger).Run(gctx) })
	g.Go(func() error { return outbox.Run(gctx) })

	err = g.Wait()
	logger.Info("bot stopped")
	return err
}

// systemClock — системное время для сценариев.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
