//go:build integration

package round

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"taletogether/internal/adapter/postgres"
	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
)

// pgWorld — игра на живом Postgres (DATABASE_URL_TEST): чат, сезон, фракции, локации жанра, один персонаж на старте.
type pgWorld struct {
	store *postgres.Store
	svc   *Service
	clock *clock
	game  model.Game
	char  model.Character
}

func newPGWorld(t *testing.T) *pgWorld {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_TEST")
	if dsn == "" {
		t.Skip("DATABASE_URL_TEST is not set")
	}
	ctx := context.Background()
	store, err := postgres.New(ctx, postgres.Settings{DSN: dsn}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	user, err := store.UpsertUser(ctx, model.TelegramUser{ID: model.TelegramUserID(time.Now().UnixNano() % 1_000_000_000), Username: "it"})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := store.CreateChat(ctx, model.Chat{TelegramChatID: model.TelegramChatID(-time.Now().UnixNano() % 1_000_000_000), Title: "IT", OwnerUserID: user.ID, BotStatus: model.BotMember})
	if err != nil {
		t.Fatal(err)
	}
	genre, err := store.GetGenreByCode(ctx, "fantasy")
	if err != nil {
		t.Fatal(err)
	}
	s := model.DefaultGameSettings()
	s.MinRoundAnswers = 1
	g, err := store.CreateGame(ctx, model.Game{ChatID: chat.ID, GenreID: genre.ID, Format: model.FormatNovel, Mode: model.ModeBalanced, Title: "Интеграция", Status: model.GameDraft, Timezone: "UTC", RoundMinutes: 10, Settings: s, JoinCode: fmt.Sprintf("IT%06d", time.Now().UnixNano()%1_000_000), CreatedByUserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	season, err := store.CreateSeason(ctx, model.Season{GameID: g.ID, SeasonNo: 1, Status: model.SeasonActive})
	if err != nil {
		t.Fatal(err)
	}
	var faction model.Faction
	for _, code := range model.FactionCodes {
		f, err := store.CreateFaction(ctx, model.Faction{GameID: g.ID, Code: code, Title: string(code), Relations: map[model.FactionCode]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if faction.ID == 0 {
			faction = f
		}
	}
	var start *model.LocationID
	for i, lt := range genre.Template.Locations {
		l, err := store.CreateLocation(ctx, model.Location{GameID: g.ID, Name: lt.Name, Description: lt.Description, Difficulty: lt.Difficulty, Links: lt.Links, Objects: lt.Objects, Active: true, Start: lt.Start || i == 0})
		if err != nil {
			t.Fatal(err)
		}
		if start == nil {
			id := l.ID
			start = &id
		}
	}
	if err := store.AddMember(ctx, model.GameMember{GameID: g.ID, UserID: user.ID, Role: model.RoleAdmin, Status: model.MemberActive}); err != nil {
		t.Fatal(err)
	}
	char, err := store.CreateCharacter(ctx, model.Character{GameID: g.ID, SeasonID: season.ID, UserID: user.ID, ModelID: genre.CharacterModelID, Nickname: "Элис", Archetype: "rogue", Status: model.CharacterActive, FactionID: faction.ID, HP: 6, LocationID: start, Conditions: []string{}, Knowledge: []string{},
		Attributes: map[string]int{"strength": 2, "agility": 4, "wits": 3, "will": 3}, Skills: map[string]int{"thievery": 3}})
	if err != nil {
		t.Fatal(err)
	}
	w := &pgWorld{store: store, clock: c, game: g, char: char}
	w.svc = New(store, allowAll{}, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.svc.seed = func() int64 { return 7 }
	return w
}

func (w *pgWorld) chronicles(t *testing.T) int {
	t.Helper()
	msgs, err := w.store.ListOutbox(context.Background(), w.game.ID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range msgs {
		if m.Kind == model.OutboxChronicle {
			n++
		}
	}
	return n
}

// shutdownLLM — рассказчик, во время вызова которого процесс получает сигнал остановки: контекст тика отменяется.
type shutdownLLM struct {
	cancel context.CancelFunc
	calls  int
}

func (s *shutdownLLM) Complete(ctx context.Context, call applm.Call, req model.LLMRequest) (model.LLMResponse, error) {
	s.calls++
	if call.Role == model.RoleNarrator && s.cancel != nil {
		s.cancel()
		s.cancel = nil
		return model.LLMResponse{}, ctx.Err()
	}
	if call.Role == model.RoleNarrator {
		return model.LLMResponse{JSON: json.RawMessage(`{"chronicle":"Тихо.","scenes":[]}`), TokensIn: 10, TokensOut: 5}, nil
	}
	return model.LLMResponse{}, errors.New("interpreter down") // разбор кодом
}

// TestIntegration_RestartMidRound: остановка процесса во время вызова рассказчика откатывает транзакцию закрытия
// (раунд открыт, событий и сообщений нет); после «перезапуска» следующий тик разрешает раунд ровно один раз.
func TestIntegration_RestartMidRound(t *testing.T) {
	w := newPGWorld(t)
	ctx := context.Background()
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	g, _ := w.store.GetGame(ctx, w.game.ID)
	r1, _, err := w.svc.Submit(ctx, g, w.char, "идти к башне")
	if err != nil {
		t.Fatal(err)
	}
	tickCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	llm := &shutdownLLM{cancel: cancel}
	w.svc.LLM = llm
	w.clock.t = w.clock.t.Add(10 * time.Minute)
	if err := w.svc.Tick(tickCtx, w.clock.Now()); err == nil {
		t.Fatal("tick survived shutdown")
	}
	open, err := w.store.OpenRound(ctx, w.game.ID)
	if err != nil || open.ID != r1.ID {
		t.Fatalf("round after shutdown = %+v, %v", open, err)
	}
	if events, _ := w.store.ListEvents(ctx, w.game.ID, r1.ID); len(events) != 0 {
		t.Fatalf("events survived rollback: %+v", events)
	}
	if n := w.chronicles(t); n != 0 {
		t.Fatalf("chronicles after rollback = %d", n)
	}
	// Перезапуск: новый контекст, тот же дедлайн.
	w.clock.t = w.clock.t.Add(5 * time.Second)
	if err := w.svc.Tick(ctx, w.clock.Now()); err != nil {
		t.Fatal(err)
	}
	done, _ := w.store.RoundByNo(ctx, w.game.ID, 1)
	if done.Status != model.RoundPublished || done.Metrics == nil || done.Metrics.Resolved != 1 {
		t.Fatalf("round after restart = %+v", done)
	}
	events, _ := w.store.ListEvents(ctx, w.game.ID, r1.ID)
	if len(events) == 0 || events[0].Seq != 1 {
		t.Fatalf("events after restart = %+v", events)
	}
	if n := w.chronicles(t); n != 1 {
		t.Fatalf("chronicles after restart = %d", n)
	}
	if open, err := w.store.OpenRound(ctx, w.game.ID); err != nil || open.RoundNo != 2 {
		t.Fatalf("next round = %+v, %v", open, err)
	}
	if n, same, err := w.svc.Replay(ctx, g, 1, 1); err != nil || !same || n != len(events) {
		t.Fatalf("replay = %d, %v, %v", n, same, err)
	}
}

// blockingLLM — интерпретатор, который сообщает о входе и ждёт разрешения: второй воркер в это время должен
// пропустить игру по SKIP LOCKED.
type blockingLLM struct {
	entered chan struct{}
	release chan struct{}
	calls   int
}

func (b *blockingLLM) Complete(ctx context.Context, call applm.Call, req model.LLMRequest) (model.LLMResponse, error) {
	b.calls++
	if call.Role == model.RoleInterpreter {
		close(b.entered)
		<-b.release
		return model.LLMResponse{}, errors.New("interpreter down")
	}
	return model.LLMResponse{}, errors.New("narrator down")
}

// TestIntegration_NoOverlap: два тика по одной игре одновременно — второй пропускает заблокированную строку,
// раунд разрешается один раз, следующий открывается один раз.
func TestIntegration_NoOverlap(t *testing.T) {
	w := newPGWorld(t)
	ctx := context.Background()
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	g, _ := w.store.GetGame(ctx, w.game.ID)
	if _, _, err := w.svc.Submit(ctx, g, w.char, "ждать"); err != nil {
		t.Fatal(err)
	}
	llm := &blockingLLM{entered: make(chan struct{}), release: make(chan struct{})}
	w.svc.LLM = llm
	w.clock.t = w.clock.t.Add(10 * time.Minute)
	first := make(chan error, 1)
	go func() { first <- w.svc.Tick(ctx, w.clock.Now()) }()
	select {
	case <-llm.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first tick did not reach the interpreter")
	}
	if err := w.svc.Tick(ctx, w.clock.Now()); err != nil { // игра под блокировкой первого воркера
		t.Fatalf("second tick = %v", err)
	}
	if open, err := w.store.OpenRound(ctx, w.game.ID); err != nil || open.RoundNo != 1 {
		t.Fatalf("second tick changed the round: %+v, %v", open, err)
	}
	close(llm.release)
	select {
	case err := <-first:
		if err != nil {
			t.Fatalf("first tick = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first tick did not finish")
	}
	if llm.calls != 2 { // интерпретатор и рассказчик по одному разу
		t.Fatalf("llm calls = %d", llm.calls)
	}
	if done, _ := w.store.RoundByNo(ctx, w.game.ID, 1); done.Status != model.RoundPublished {
		t.Fatalf("round 1 = %+v", done)
	}
	if n := w.chronicles(t); n != 1 {
		t.Fatalf("chronicles = %d", n)
	}
	if open, err := w.store.OpenRound(ctx, w.game.ID); err != nil || open.RoundNo != 2 {
		t.Fatalf("next round = %+v, %v", open, err)
	}
}
