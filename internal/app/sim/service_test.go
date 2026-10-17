package sim

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"

	"taletogether/internal/adapter/postgres/fake"
	tgfake "taletogether/internal/adapter/telegram/fake"
	"taletogether/internal/app/bot"
	"taletogether/internal/app/chat"
	"taletogether/internal/app/delivery"
	"taletogether/internal/app/game"
	applm "taletogether/internal/app/llm"
	"taletogether/internal/app/round"
	"taletogether/internal/app/scheduler"
	"taletogether/internal/model"
)

// scriptedPlayer — фейк модели игрока: отвечает на вопросы диалогов по тексту вопроса и кнопкам, в раунде
// выполняет задания и подаёт заявку; запоминает запросы.
type scriptedPlayer struct {
	mu       sync.Mutex
	requests []model.LLMRequest
	names    int
}

var (
	reMessage = regexp.MustCompile(`(?m)^\[(личка|группа)\] сообщение #(\d+):$`)
	reButtons = regexp.MustCompile(`«([^»]+)»`)
)

func (f *scriptedPlayer) Complete(_ context.Context, req model.LLMRequest) (model.LLMResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if req.Role != model.RoleSimPlayer {
		return model.LLMResponse{}, fmt.Errorf("неожиданная роль %s", req.Role)
	}
	a := f.decide(req.Input)
	raw, _ := json.Marshal(a)
	return model.LLMResponse{JSON: raw, Text: string(raw), Model: "fake", Usage: model.LLMUsage{Input: 100, Output: 20}}, nil
}

// decide разбирает вход: последний вопрос лички с кнопками, фаза, задания и уже сделанные действия.
func (f *scriptedPlayer) decide(input string) action {
	// последний блок лички
	locs := reMessage.FindAllStringSubmatchIndex(input, -1)
	var question, buttons string
	var msgID int64
	for i := len(locs) - 1; i >= 0; i-- {
		if input[locs[i][2]:locs[i][3]] != "личка" {
			continue
		}
		end := len(input)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := input[locs[i][1]:end]
		fmt.Sscanf(input[locs[i][4]:locs[i][5]], "%d", &msgID)
		if idx := strings.Index(block, "кнопки под сообщением:"); idx >= 0 {
			question, buttons = block[:idx], block[idx:]
			if nl := strings.Index(buttons, "\n"); nl >= 0 {
				buttons = buttons[:nl]
			}
		} else {
			question = block
		}
		break
	}
	if strings.Contains(input, "Фаза: раунд") {
		section := func(from, to string) string {
			s := input
			i := strings.Index(s, from)
			if i < 0 {
				return ""
			}
			s = s[i:]
			if j := strings.Index(s, to); j >= 0 {
				s = s[:j]
			}
			return s
		}
		tasks := section("Задания этого раунда", "\nТвои действия") + section("Задания этого раунда", "\nНовые сообщения")
		done := func(s string) bool {
			return strings.Contains(section("Твои действия в этом ходу", "\nНовые сообщения"), s)
		}
		switch {
		case strings.Contains(tasks, "/scout") && !done("/scout"):
			return action{Action: "command", Text: "/scout", Thought: "разведка по заданию"}
		case strings.Contains(tasks, "/say") && !done("/say"):
			return action{Action: "command", Text: "/say Привет всем, держимся вместе.", Thought: "реплика по заданию"}
		case !done("/act"):
			return action{Action: "command", Text: "/act Иду вперёд и осматриваюсь.", Thought: "заявка"}
		}
		return action{Action: "done", Thought: "ход сделан"}
	}
	switch {
	case question == "":
		return action{Action: "done", Thought: "нечего отвечать"}
	case strings.Contains(question, "Никнейм"):
		f.mu.Lock()
		f.names++
		n := f.names
		f.mu.Unlock()
		return action{Action: "text", Text: fmt.Sprintf("Герой%d", n), Thought: "имя"}
	case strings.Contains(question, "Название мира"):
		return action{Action: "text", Text: "Мир прогона", Thought: "название"}
	}
	if labels := reButtons.FindAllStringSubmatch(buttons, -1); len(labels) > 0 {
		return action{Action: "button", MessageID: msgID, Button: labels[0][1], Thought: "первая кнопка"}
	}
	if strings.Contains(question, "«-»") || strings.Contains(question, "оставить как есть") {
		return action{Action: "text", Text: "-", Thought: "пропуск"}
	}
	return action{Action: "done", Thought: "жду"}
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// runScenario собирает стек на фейках (бот, раунды, outbox, планировщик, фейковый игрок) и ведёт прогон сценария.
func runScenario(t *testing.T, sc Scenario) (Report, *fake.Store, *scriptedPlayer, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	clock := realClock{}
	store := fake.New(clock.Now)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tr := tgfake.New("tale_bot", nil)
	chats := chat.New(store, logger)
	games := game.New(store, chats, clock, logger)
	games.Defaults.MinRoundAnswers = model.MinRoundAnswersDev
	games.MinRoundMinutes = model.MinRoundMinutesDev
	rounds := round.New(store, games, clock, logger)
	rounds.AnnounceSkipped = true
	player := &scriptedPlayer{}
	llm := applm.New(store, map[string]applm.Client{model.ProviderFake: player}, clock, logger)
	if _, err := store.CreateLLMProfile(ctx, model.LLMProfile{Name: "sim-test", Provider: model.ProviderFake, ModelInterpreter: "x", ModelNarrator: "x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	commands := bot.New(bot.Settings{Version: "test", Env: "dev", BotUsername: "tale_bot", AutoFaction: model.FactionGood},
		store, store, tr, chats, games, rounds, llm, clock, logger)
	journal := &Journal{}
	rounds.Journal, llm.Journal, commands.Journal = journal, journal, journal
	outbox := delivery.New(store, tr, clock, delivery.Settings{Interval: 20 * time.Millisecond}, logger)
	sched := scheduler.New(rounds, clock, 20*time.Millisecond, logger)

	if err := sc.Validate(); err != nil {
		t.Fatal(err)
	}
	runner := New(sc, Settings{Poll: 10 * time.Millisecond, PhaseTimeout: 20 * time.Second}, tr, commands, store, llm, journal, logger)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return sched.Run(gctx) })
	g.Go(func() error { return outbox.Run(gctx) })
	rep, err := runner.Run(gctx)
	cancel()
	_ = g.Wait()
	if os.Getenv("SIM_DUMP") != "" {
		t.Log(rep.Transcript + "\n" + rep.Summary)
	}
	return rep, store, player, err
}

// TestRunner_ThreeRounds: полный прогон на фейках без сети — три игрока входят по расписанию, три раунда
// закрываются по /next, игра доигрывается до финала по max_rounds, отчёт содержит раунды и заявки.
func TestRunner_ThreeRounds(t *testing.T) {
	sc := Scenario{Name: "test", Rounds: 3, RoundMinutes: 2, Profile: "sim-test", PlayerModel: "fake-player", MaxActions: 4,
		Players: []Persona{
			{Name: "Марта", Username: "marta", JoinsRound: 0, Persona: "p", Character: "c"},
			{Name: "Глеб", Username: "gleb", JoinsRound: 2, Persona: "p", Character: "c"},
			{Name: "Нина", Username: "nina", JoinsRound: 3, Persona: "p", Character: "c"},
		},
		Features: map[string][]string{"2": {"scout"}, "3": {"say@gleb"}},
		Budget:   Budget{MaxCalls: 500}}
	rep, store, player, err := runScenario(t, sc)
	ctx := context.Background()
	if err != nil {
		t.Fatalf("прогон: %v\n%s", err, rep.Transcript)
	}
	if rep.Status != model.GameFinished || rep.Rounds != 3 || rep.Stopped != "" {
		t.Fatalf("итог: статус %s, раундов %d, остановка %q\n%s", rep.Status, rep.Rounds, rep.Stopped, rep.Summary)
	}
	// заявки: раунд 1 — один игрок, раунд 2 — двое, раунд 3 — трое
	for no, want := range map[int]int{1: 1, 2: 2, 3: 3} {
		rd, err := store.RoundByNo(ctx, rep.GameID, no)
		if err != nil {
			t.Fatalf("раунд %d: %v", no, err)
		}
		subs, _ := store.ListSubmissions(ctx, rep.GameID, rd.ID)
		if len(subs) != want || rd.Status != model.RoundPublished {
			t.Errorf("раунд %d: заявок %d (ожидалось %d), статус %s", no, len(subs), want, rd.Status)
		}
	}
	// задания: разведка во втором раунде у двоих, реплика в третьем только у Глеба
	var scouts, says int
	for _, req := range player.requests {
		if strings.Contains(req.Input, "Фаза: раунд, раунд 2") && strings.Contains(req.Input, "Задания") && strings.Contains(req.Input, "/scout") {
			scouts++
		}
		if strings.Contains(req.Input, "Фаза: раунд, раунд 3") && strings.Contains(req.Input, "Задания") && strings.Contains(req.Input, "/say") {
			says++
		}
	}
	if scouts == 0 || says == 0 {
		t.Errorf("задания не дошли до игроков: scout=%d say=%d", scouts, says)
	}
	if rep.Calls == 0 || !strings.Contains(rep.Summary, "| player |") {
		t.Errorf("вызовы игрока не учтены: calls=%d\n%s", rep.Calls, rep.Summary)
	}
	for _, want := range []string{"## Раунд 1", "## Раунд 2", "## Раунд 3", "**Марта → личка:** /act", "**Нина → личка:** /act", "**Глеб → личка:** /say"} {
		if !strings.Contains(rep.Transcript, want) {
			t.Errorf("в транскрипте нет %q", want)
		}
	}
	for _, want := range []string{"## Раунды", "| 3 | published |", "## Карта и квесты", "Клеток:", "Outbox:"} {
		if !strings.Contains(rep.Summary, want) {
			t.Errorf("в сводке нет %q", want)
		}
	}
	if strings.Contains(rep.Summary, "Сводка неполна") {
		t.Errorf("сводка неполна:\n%s", rep.Summary)
	}
}

// TestRunner_Endless: бесконечная игра (ADR-0037) — `max_rounds 0`, после последнего раунда расписания администратор
// завершает игру `/finish yes`, прогон доигран до финала без досрочной остановки, сводка отмечает режим.
func TestRunner_Endless(t *testing.T) {
	sc := Scenario{Name: "endless", Rounds: 2, Endless: true, RoundMinutes: 2, Profile: "sim-test", PlayerModel: "fake-player", MaxActions: 4,
		Players: []Persona{
			{Name: "Марта", Username: "marta", JoinsRound: 0, Persona: "p", Character: "c"},
			{Name: "Глеб", Username: "gleb", JoinsRound: 2, Persona: "p", Character: "c"},
		},
		Budget: Budget{MaxCalls: 500}}
	rep, store, _, err := runScenario(t, sc)
	if err != nil {
		t.Fatalf("прогон: %v\n%s", err, rep.Transcript)
	}
	if rep.Status != model.GameFinished || rep.Rounds != 2 || rep.Stopped != "" {
		t.Fatalf("итог: статус %s, раундов %d, остановка %q\n%s", rep.Status, rep.Rounds, rep.Stopped, rep.Summary)
	}
	g, err := store.GetGame(context.Background(), rep.GameID)
	if err != nil || !g.Endless() || g.Settings.MaxRounds != 0 {
		t.Fatalf("игра не бесконечная: %+v, %v", g.Settings, err)
	}
	for _, want := range []string{"/finish yes", "бесконечная игра: после раунда 2", "история завершена", "игра завершена"} {
		if !strings.Contains(rep.Transcript, want) {
			t.Errorf("в транскрипте нет %q", want)
		}
	}
	if !strings.Contains(rep.Summary, "Бесконечная игра") || !strings.Contains(rep.Summary, "Партия доиграна до финала") {
		t.Errorf("сводка без отметки режима:\n%s", rep.Summary)
	}
}
