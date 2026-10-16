// Команда simulate: прогон партии ИИ-игроками на полном стеке бота с подменой транспорта Telegram (пункт 40,
// ADR-0034). Отчёт — папка `logs/sim/<метка>/`: журнал чатов `chats/`, `transcript.md`, `summary.md`.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"taletogether/cmd/internal/stack"
	"taletogether/internal/adapter/chatlog"
	"taletogether/internal/adapter/telegram/fake"
	"taletogether/internal/app/sim"
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

func run() error {
	runPath := flag.String("run", "sim/runs/basic.json", "сценарий прогона")
	profile := flag.String("profile", "", "профиль LLM прогона вместо указанного в сценарии")
	modelNarrator := flag.String("model-narrator", "", "модель рассказчика, завязки и генерации мира вместо модели профиля")
	modelInterpreter := flag.String("model-interpreter", "", "модель интерпретатора вместо модели профиля")
	modelPlayer := flag.String("model-player", "", "модель ИИ-игрока вместо указанной в сценарии")
	rounds := flag.Int("rounds", 0, "число раундов вместо указанного в сценарии (короткий дымовой прогон)")
	out := flag.String("out", "logs/sim", "корень папок прогонов")
	dsn := flag.String("db", "", "DSN БД прогона вместо DATABASE_URL_SIM")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *dsn != "" {
		cfg.DatabaseURLSim = *dsn
	}
	if strings.TrimSpace(cfg.DatabaseURLSim) == "" {
		return fmt.Errorf("DATABASE_URL_SIM не задан")
	}
	if cfg.AppEnv != "dev" {
		return fmt.Errorf("прогон только при APP_ENV=dev (короткие раунды, /next); сейчас %q", cfg.AppEnv)
	}
	sc, err := sim.Load(*runPath)
	if err != nil {
		return err
	}
	if *profile != "" {
		sc.Profile = *profile
	}
	if *modelPlayer != "" {
		sc.PlayerModel = *modelPlayer
	}
	if *rounds > 0 {
		sc.Rounds = *rounds
		for i := range sc.Players {
			if sc.Players[i].JoinsRound > sc.Rounds {
				sc.Players[i].JoinsRound = sc.Rounds
			}
		}
	}

	stamp := time.Now().Format("2006-01-02_15-04-05")
	dir := filepath.Join(*out, stamp)
	if err := os.MkdirAll(filepath.Join(dir, "chats"), 0o755); err != nil {
		return err
	}
	logFile, err := os.Create(filepath.Join(dir, "sim.log"))
	if err != nil {
		return err
	}
	defer logFile.Close()
	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
	fmt.Printf("прогон «%s»: %d раундов, профиль %s, игрок %s; папка %s\n", sc.Name, sc.Rounds, sc.Profile, sc.PlayerModel, dir)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	providers, err := stack.Providers(cfg.LLM)
	if err != nil {
		return err
	}
	models := sim.RoleModels(*modelNarrator, *modelInterpreter, "")
	if len(models) > 0 {
		for k, c := range providers {
			providers[k] = sim.ModelOverride{Client: c, Models: models}
		}
	}
	journal := &sim.Journal{Next: chatlog.New(chatlog.Settings{Dir: filepath.Join(dir, "chats")}, logger)}
	tg := fake.New("tale_sim_bot", nil)
	tg.SetUpdateBase(time.Now().UnixMilli()) // номера update уникальны между прогонами на одной БД
	st, err := stack.Build(ctx, stack.Options{
		Version: version, Env: cfg.AppEnv, DSN: cfg.DatabaseURLSim, MigrateOnStart: true,
		SchedulerTick: time.Second, OutboxTick: 500 * time.Millisecond, Providers: providers, Journal: journal,
		AutoFaction: model.FactionCode(sc.AutoFaction),
	}, tg, logger)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Commands.RegisterMenu(ctx); err != nil {
		return err
	}

	runner := sim.New(sc, sim.Settings{}, tg, st.Commands, st.Store, st.LLM, journal, logger)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return st.Run(gctx) })
	var rep sim.Report
	var runErr error
	g.Go(func() error {
		defer stop()
		rep, runErr = runner.Run(gctx)
		return nil
	})
	_ = g.Wait()
	if err := os.WriteFile(filepath.Join(dir, "transcript.md"), []byte(rep.Transcript), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte(rep.Summary), 0o644); err != nil {
		return err
	}
	fmt.Printf("игра %d: статус %s, раундов %d, вызовов %d, $%.4f; отчёт: %s\n", rep.GameID, rep.Status, rep.Rounds, rep.Calls, rep.CostUSD, dir)
	if rep.Stopped != "" {
		fmt.Println("остановка:", rep.Stopped)
	}
	return runErr
}
