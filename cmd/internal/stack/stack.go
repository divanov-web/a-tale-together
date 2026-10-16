// Package stack — общая сборка стека бота для `cmd/bot` и `cmd/simulate` (ADR-0034): хранилище, журнал чатов,
// сценарии, провайдеры LLM, команды, outbox и планировщик. Транспорт Telegram передаёт вызывающая команда:
// боевой клиент или подмена имитатора.
package stack

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/sync/errgroup"

	"taletogether/internal/adapter/llm/anthropic"
	"taletogether/internal/adapter/llm/claudecli"
	"taletogether/internal/adapter/llm/openai"
	"taletogether/internal/adapter/postgres"
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

// Telegram — всё, что стеку нужно от транспорта: ответы и запросы сценария команд, отправка outbox, имя бота.
type Telegram interface {
	bot.Telegram
	delivery.Sender
	Username() string
}

// Journal — журнал чатов (ADR-0012); nil — выключен.
type Journal interface {
	Write(ctx context.Context, rec model.ChatLogRecord)
}

// Clock — источник времени сценариев.
type Clock interface {
	Now() time.Time
}

// Options — параметры сборки.
type Options struct {
	Version        string
	Env            string // dev|prod
	DSN            string
	MigrateOnStart bool
	SchedulerTick  time.Duration
	OutboxTick     time.Duration
	OwnerID        model.TelegramUserID
	GuideURL       string
	AutoFaction    model.FactionCode
	Providers      map[string]applm.Client
	Journal        Journal
	Clock          Clock
}

// Stack — собранные сервисы.
type Stack struct {
	Store     *postgres.Store
	Chats     *chat.Service
	Games     *game.Service
	Rounds    *round.Service
	LLM       *applm.Service
	Commands  *bot.Service
	Outbox    *delivery.Service
	Scheduler *scheduler.Service
}

// Providers собирает карту провайдеров LLM по конфигурации; рабочая папка `claude-cli` создаётся.
func Providers(cfg config.LLM) (map[string]applm.Client, error) {
	if cfg.ClaudeCLIWorkdir != "" {
		if err := os.MkdirAll(cfg.ClaudeCLIWorkdir, 0o755); err != nil {
			return nil, fmt.Errorf("stack.Providers: рабочая папка claude-cli: %w", err)
		}
	}
	return map[string]applm.Client{
		model.ProviderClaudeCLI: claudecli.New(claudecli.Settings{Path: cfg.ClaudeCLIPath, Workdir: cfg.ClaudeCLIWorkdir}),
		model.ProviderAnthropic: anthropic.New(),
		model.ProviderOpenAI:    openai.New(),
	}, nil
}

// Build подключает хранилище и собирает сервисы. Закрывать через Close.
func Build(ctx context.Context, o Options, tg Telegram, logger *slog.Logger) (*Stack, error) {
	store, err := postgres.New(ctx, postgres.Settings{DSN: o.DSN}, logger)
	if err != nil {
		return nil, err
	}
	if o.MigrateOnStart {
		if err := store.Migrate(ctx); err != nil {
			store.Close()
			return nil, err
		}
	}
	clock := o.Clock
	if clock == nil {
		clock = systemClock{}
	}
	chats := chat.New(store, logger)
	games := game.New(store, chats, clock, logger)
	if o.Env == "dev" {
		games.Defaults.MinRoundAnswers = model.MinRoundAnswersDev // кворум 1 на время разработки
		games.MinRoundMinutes = model.MinRoundMinutesDev          // короткие раунды для прогонов в песочнице
	}
	rounds := round.New(store, games, clock, logger)
	rounds.AnnounceSkipped = o.Env == "dev" // на время разработки пропуск раунда анонсируется в группу
	llm := applm.New(store, o.Providers, clock, logger)
	rounds.LLM = llm // интерпретатор и рассказчик: два вызова на раунд (пункты 08, 09)
	commands := bot.New(bot.Settings{Version: o.Version, Env: o.Env, BotUsername: tg.Username(), OwnerID: o.OwnerID, GuideURL: o.GuideURL, AutoFaction: o.AutoFaction},
		store, store, tg, chats, games, rounds, llm, clock, logger)
	outbox := delivery.New(store, tg, clock, delivery.Settings{Interval: o.OutboxTick}, logger)
	if o.Journal != nil {
		rounds.Journal, llm.Journal, commands.Journal, outbox.Journal = o.Journal, o.Journal, o.Journal, o.Journal
	}
	return &Stack{Store: store, Chats: chats, Games: games, Rounds: rounds, LLM: llm, Commands: commands, Outbox: outbox,
		Scheduler: scheduler.New(rounds, clock, o.SchedulerTick, logger)}, nil
}

// Run ведёт планировщик раундов и воркер outbox до отмены контекста.
func (s *Stack) Run(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return s.Scheduler.Run(gctx) })
	g.Go(func() error { return s.Outbox.Run(gctx) })
	return g.Wait()
}

// Close закрывает хранилище.
func (s *Stack) Close() { s.Store.Close() }

// systemClock — системное время для сценариев.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
