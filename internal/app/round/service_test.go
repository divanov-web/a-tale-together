package round

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"taletogether/internal/adapter/postgres/fake"
	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

type allowAll struct{}

func (allowAll) CanAdmin(context.Context, model.Game, model.UserID) (bool, error) { return true, nil }

// world — игра с чатом, сезоном, фракциями и двумя персонажами в фейковом хранилище.
type world struct {
	store *fake.Store
	svc   *Service
	clock *clock
	game  model.Game
	chars []model.Character
}

func newWorld(t *testing.T, settings model.GameSettings) *world {
	t.Helper()
	ctx := context.Background()
	c := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	store := fake.New(c.Now)
	user, _ := store.UpsertUser(ctx, model.TelegramUser{ID: 1, Username: "owner"})
	chat, _ := store.CreateChat(ctx, model.Chat{TelegramChatID: -100, Title: "Песочница", OwnerUserID: user.ID, BotStatus: model.BotMember})
	genre, _ := store.GetGenreByCode(ctx, "fantasy")
	g, err := store.CreateGame(ctx, model.Game{ChatID: chat.ID, GenreID: genre.ID, Title: "Мир", Status: model.GameDraft, Timezone: "UTC", RoundMinutes: 10, Settings: settings, JoinCode: "CODE", CreatedByUserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	season, _ := store.CreateSeason(ctx, model.Season{GameID: g.ID, SeasonNo: 1, Status: model.SeasonActive})
	f, _ := store.CreateFaction(ctx, model.Faction{GameID: g.ID, Code: model.FactionGood, Title: "Орден"})
	_, _ = store.CreateFaction(ctx, model.Faction{GameID: g.ID, Code: model.FactionEvil, Title: "Гильдия"})
	w := &world{store: store, clock: c, game: g}
	w.svc = New(store, allowAll{}, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.svc.AnnounceSkipped = true
	w.svc.seed = func() int64 { return 42 }
	for i, nick := range []string{"Элис", "Боб", "Кэт"} {
		u, _ := store.UpsertUser(ctx, model.TelegramUser{ID: model.TelegramUserID(10 + i), Username: nick})
		_ = store.AddMember(ctx, model.GameMember{GameID: g.ID, UserID: u.ID, Role: model.RolePlayer, Status: model.MemberActive})
		ch, err := store.CreateCharacter(ctx, model.Character{GameID: g.ID, SeasonID: season.ID, UserID: u.ID, ModelID: 1, Nickname: nick, Archetype: "rogue", Status: model.CharacterActive, FactionID: f.ID, HP: 6,
			Attributes: map[string]int{"strength": 2, "agility": 3, "wits": 4, "will": 3}, Skills: map[string]int{"awareness": 2}})
		if err != nil {
			t.Fatal(err)
		}
		w.chars = append(w.chars, ch)
	}
	return w
}

func (w *world) reload(t *testing.T) model.Game {
	t.Helper()
	g, err := w.store.GetGame(context.Background(), w.game.ID)
	if err != nil {
		t.Fatal(err)
	}
	w.game = g
	return g
}

func (w *world) tick(t *testing.T, d time.Duration) {
	t.Helper()
	w.clock.t = w.clock.t.Add(d)
	if err := w.svc.Tick(context.Background(), w.clock.Now()); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
}

func (w *world) outbox(t *testing.T) []model.OutboxMessage {
	t.Helper()
	msgs, err := w.store.ListOutbox(context.Background(), w.game.ID)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestBegin_OpensFirstRoundAndScene(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings())
	ctx := context.Background()
	g, err := w.svc.Begin(ctx, w.game, 1)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != model.GameActive || g.NextRoundAt == nil || !g.NextRoundAt.Equal(w.clock.t.Add(10*time.Minute)) {
		t.Fatalf("game after begin = %+v", g)
	}
	r, err := w.store.OpenRound(ctx, g.ID)
	if err != nil || r.RoundNo != 1 || r.SeasonID == 0 {
		t.Fatalf("open round = %+v, %v", r, err)
	}
	msgs := w.outbox(t)
	if len(msgs) != 2 || msgs[0].Kind != model.OutboxScene || msgs[1].Kind != model.OutboxRoundOpen || msgs[1].RoundID == nil || *msgs[1].RoundID != r.ID {
		t.Fatalf("outbox = %+v", msgs)
	}
	if msgs[0].TelegramChatID != -100 || msgs[0].Text != "«Мир» — начало истории\nВ Мир наступает осень." {
		t.Fatalf("scene = %+v", msgs[0])
	}
	if _, err := w.svc.Begin(ctx, g, 1); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second begin: %v", err)
	}
}

func TestBegin_RequiresCharacters(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings())
	ctx := context.Background()
	empty, _ := w.store.CreateChat(ctx, model.Chat{TelegramChatID: -200, OwnerUserID: 1})
	g, _ := w.store.CreateGame(ctx, model.Game{ChatID: empty.ID, GenreID: 1, Title: "Пусто", Status: model.GameDraft, Timezone: "UTC", RoundMinutes: 10, Settings: model.DefaultGameSettings(), JoinCode: "EMPTY", CreatedByUserID: 1})
	_, _ = w.store.CreateSeason(ctx, model.Season{GameID: g.ID, SeasonNo: 1, Status: model.SeasonActive})
	if _, err := w.svc.Begin(ctx, g, 1); !errors.Is(err, ErrNoCharacters) {
		t.Fatalf("begin without characters: %v", err)
	}
}

func TestTick_SkipWithoutQuorumAndCarrySubmissions(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings()) // кворум 2
	ctx := context.Background()
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	r1, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "иду в башню")
	if err != nil || r1.RoundNo != 1 {
		t.Fatalf("submit: %v", err)
	}
	w.tick(t, 5*time.Minute) // до дедлайна ничего не меняется
	if r, _ := w.store.OpenRound(ctx, w.game.ID); r.ID != r1.ID {
		t.Fatalf("round changed before deadline: %+v", r)
	}
	w.tick(t, 5*time.Minute)
	last, _ := w.store.LastRound(ctx, w.game.ID)
	if last.RoundNo != 2 || last.Status != model.RoundOpen {
		t.Fatalf("last round = %+v", last)
	}
	skipped, err := w.store.GetSubmission(ctx, w.game.ID, r1.ID, w.chars[0].ID)
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("submission stayed in skipped round: %+v, %v", skipped, err)
	}
	carried, err := w.store.GetSubmission(ctx, w.game.ID, last.ID, w.chars[0].ID)
	if err != nil || carried.RawText != "иду в башню" || carried.Status != model.SubmissionPending {
		t.Fatalf("carried submission = %+v, %v", carried, err)
	}
	msgs := w.outbox(t)
	if len(msgs) != 4 || msgs[2].Kind != model.OutboxRoundClosed || msgs[2].Text != "«Мир» — раунд 1. Недостаточное количество участников. Раунд пропущен." {
		t.Fatalf("outbox = %+v", msgs)
	}
	if msgs[3].Text != "«Мир» — раунд 2 открыт. Заявки до 12:20: /act <текст> в личке с ботом, одна на раунд. Перенесено заявок из пропущенного раунда: 1." {
		t.Fatalf("open text = %q", msgs[3].Text)
	}
	// Персонажи при пропуске не наказываются.
	for _, c := range w.chars {
		got, _ := w.store.GetCharacter(ctx, w.game.ID, c.SeasonID, c.UserID)
		if got.Status != model.CharacterActive || got.MissedRounds != 0 {
			t.Fatalf("character after skip = %+v", got)
		}
	}
	// Молчаливый пропуск вне dev.
	w.svc.AnnounceSkipped = false
	w.tick(t, 10*time.Minute) // раунд 2: заявка одна из двух → пропуск без сообщения
	msgs = w.outbox(t)
	if len(msgs) != 5 || msgs[4].Kind != model.OutboxRoundOpen {
		t.Fatalf("silent skip outbox = %+v", msgs)
	}
	info, err := w.svc.Info(ctx, w.game)
	if err != nil || info.Open == nil || info.Open.RoundNo != 3 || info.Last == nil || info.Last.SkipReason != "заявок 1 из 2" || info.Submissions != 1 {
		t.Fatalf("info = %+v, %v", info, err)
	}
}

func TestTick_FreezeSnapshotAndActivity(t *testing.T) {
	t.Parallel()
	s := model.DefaultGameSettings()
	s.MinRoundAnswers = 1
	w := newWorld(t, s)
	ctx := context.Background()
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	for i := 1; i <= 3; i++ {
		if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "действую"); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if _, _, err := w.svc.Submit(ctx, w.game, w.chars[1], "помогаю"); err != nil {
				t.Fatal(err)
			}
		}
		w.tick(t, 10*time.Minute)
		last, _ := w.store.LastRound(ctx, w.game.ID)
		frozen := last // открытый — последний; замороженный — предыдущий
		if frozen.RoundNo != i+1 {
			t.Fatalf("round %d: last = %+v", i, last)
		}
	}
	msgs := w.outbox(t)
	wantKinds := []model.OutboxKind{model.OutboxScene, model.OutboxRoundOpen, model.OutboxChronicle, model.OutboxRoundOpen, model.OutboxChronicle, model.OutboxRoundOpen, model.OutboxChronicle, model.OutboxRoundOpen}
	if len(msgs) != len(wantKinds) {
		t.Fatalf("outbox len = %d: %+v", len(msgs), msgs)
	}
	for i, k := range wantKinds {
		if msgs[i].Kind != k {
			t.Fatalf("outbox[%d] kind = %s; want %s", i, msgs[i].Kind, k)
		}
	}
	if msgs[2].Text != "«Мир» — раунд 1 — итоги\n• тихий раунд: никто ничего заметного не сделал" {
		t.Fatalf("closed text = %q", msgs[2].Text)
	}
	// Статусы: Элис active (0 пропусков), Боб waiting (2 пропуска), Кэт inactive (3 пропуска).
	want := map[string]struct {
		status model.CharacterStatus
		missed int
	}{"Элис": {model.CharacterActive, 0}, "Боб": {model.CharacterWaiting, 2}, "Кэт": {model.CharacterInactive, 3}}
	for _, c := range w.chars {
		got, _ := w.store.GetCharacter(ctx, w.game.ID, c.SeasonID, c.UserID)
		if got.Status != want[c.Nickname].status || got.MissedRounds != want[c.Nickname].missed {
			t.Fatalf("%s = %s/%d; want %+v", c.Nickname, got.Status, got.MissedRounds, want[c.Nickname])
		}
	}
	// Возврат заявкой.
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[2], "вернулась"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	got, _ := w.store.GetCharacter(ctx, w.game.ID, w.chars[2].SeasonID, w.chars[2].UserID)
	if got.Status != model.CharacterActive || got.MissedRounds != 0 {
		t.Fatalf("returned character = %+v", got)
	}
	info, _ := w.svc.Info(ctx, w.game)
	if info.Last == nil || info.Last.Status != model.RoundPublished || info.Last.Seed != 42 || info.Last.Snapshot == nil ||
		len(info.Last.Snapshot.Characters) != 3 || len(info.Last.Snapshot.Submissions) != 1 || info.Last.Snapshot.Submissions[0].Text != "вернулась" {
		t.Fatalf("frozen round = %+v", info.Last)
	}
	subs, _ := w.store.ListSubmissions(ctx, w.game.ID, info.Last.ID)
	if len(subs) != 1 || subs[0].Status != model.SubmissionFrozen {
		t.Fatalf("frozen submissions = %+v", subs)
	}
}

func TestSubmit_Validation(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings())
	ctx := context.Background()
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "рано"); !errors.Is(err, ErrNotActive) {
		t.Fatalf("draft: %v", err)
	}
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	cases := []struct {
		text string
		want error
	}{{"", ErrEmpty}, {"   ", ErrEmpty}, {string(make([]rune, 501)), ErrTooLong}}
	for _, tc := range cases {
		if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], tc.text); !errors.Is(err, tc.want) {
			t.Fatalf("Submit(%q) = %v; want %v", tc.text, err, tc.want)
		}
	}
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "первая"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "замена"); !errors.Is(err, ErrAlreadyActed) {
		t.Fatalf("replace forbidden by default: %v", err)
	}
	w.clock.t = w.clock.t.Add(time.Minute)
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "замена"); !errors.Is(err, ErrAlreadyActed) {
		t.Fatalf("replace forbidden after a minute too: %v", err)
	}
	// Настройка разработчика allow_resubmit: замена не чаще раза в минуту.
	w.game.Settings.AllowResubmit = true
	if err := w.store.UpdateGame(ctx, w.game); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "замена"); err != nil {
		t.Fatalf("replace after a minute with allow_resubmit: %v", err)
	}
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "ещё"); !errors.Is(err, ErrTooFrequent) {
		t.Fatalf("replace within a minute: %v", err)
	}
	w.clock.t = w.clock.t.Add(time.Minute)
	_, sub, err := w.svc.Submit(ctx, w.game, w.chars[0], "замена")
	if err != nil || sub.RawText != "замена" {
		t.Fatalf("replace after a minute = %+v, %v", sub, err)
	}
	subs, _ := w.store.ListSubmissions(ctx, w.game.ID, sub.RoundID)
	if len(subs) != 1 {
		t.Fatalf("replace created second submission: %+v", subs)
	}
	w.clock.t = w.clock.t.Add(10 * time.Minute) // дедлайн прошёл, планировщик ещё не сработал
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[1], "поздно"); !errors.Is(err, ErrNoOpenRound) {
		t.Fatalf("after deadline: %v", err)
	}
}

func TestPause_And_Next(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings())
	ctx := context.Background()
	if _, _, err := w.svc.Pause(ctx, w.game, 1); !errors.Is(err, ErrNotActive) {
		t.Fatalf("pause draft: %v", err)
	}
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	g, paused, err := w.svc.Pause(ctx, w.game, 1)
	if err != nil || !paused || g.Status != model.GamePaused || g.NextRoundAt != nil {
		t.Fatalf("pause = %+v, %v, %v", g, paused, err)
	}
	w.tick(t, time.Hour) // на паузе планировщик игру не трогает
	if r, _ := w.store.OpenRound(ctx, g.ID); r.RoundNo != 1 || r.Status != model.RoundOpen {
		t.Fatalf("round during pause = %+v", r)
	}
	if _, err := w.svc.Next(ctx, w.game, 1); !errors.Is(err, ErrPaused) {
		t.Fatalf("next on pause: %v", err)
	}
	g, paused, err = w.svc.Pause(ctx, w.game, 1)
	if err != nil || paused || g.Status != model.GameActive || g.NextRoundAt == nil || !g.NextRoundAt.Equal(w.clock.t.Add(10*time.Minute)) {
		t.Fatalf("resume = %+v, %v, %v", g, paused, err)
	}
	r, _ := w.store.OpenRound(ctx, g.ID)
	if !r.DeadlineAt.Equal(*g.NextRoundAt) {
		t.Fatalf("deadline after resume = %v; want %v", r.DeadlineAt, *g.NextRoundAt)
	}
	// Ручной шаг: раунд 1 без заявок пропущен, раунд 2 открыт.
	if _, err := w.svc.Next(ctx, g, 1); err != nil {
		t.Fatal(err)
	}
	open, _ := w.store.OpenRound(ctx, g.ID)
	if open.RoundNo != 2 {
		t.Fatalf("after next = %+v", open)
	}
	kinds := []model.OutboxKind{}
	for _, m := range w.outbox(t) {
		kinds = append(kinds, m.Kind)
	}
	if len(kinds) != 6 || kinds[2] != model.OutboxSystem || kinds[3] != model.OutboxSystem || kinds[4] != model.OutboxRoundClosed || kinds[5] != model.OutboxRoundOpen {
		// раунд 1 без заявок пропущен: round_closed с сообщением о пропуске
		t.Fatalf("outbox kinds = %v", kinds)
	}
}

func TestWindow(t *testing.T) {
	t.Parallel()
	at := func(h, m int) time.Time { return time.Date(2026, 10, 10, h, m, 0, 0, time.UTC) }
	cases := []struct {
		from, to string
		at       time.Time
		in       bool
	}{
		{"", "", at(3, 0), true},
		{"10:00", "22:00", at(9, 59), false},
		{"10:00", "22:00", at(10, 0), true},
		{"10:00", "22:00", at(21, 59), true},
		{"10:00", "22:00", at(22, 0), false},
		{"22:00", "02:00", at(23, 0), true},
		{"22:00", "02:00", at(1, 59), true},
		{"22:00", "02:00", at(2, 0), false},
		{"22:00", "02:00", at(12, 0), false},
	}
	for _, tc := range cases {
		if got := inWindow("UTC", tc.from, tc.to, tc.at); got != tc.in {
			t.Errorf("inWindow(%s-%s, %v) = %v; want %v", tc.from, tc.to, tc.at, got, tc.in)
		}
	}
	if got := nextWindowStart("UTC", "10:00", at(9, 30)); !got.Equal(at(10, 0)) {
		t.Errorf("next start same day = %v", got)
	}
	if got := nextWindowStart("UTC", "10:00", at(22, 30)); !got.Equal(at(10, 0).AddDate(0, 0, 1)) {
		t.Errorf("next start tomorrow = %v", got)
	}
	msk := time.FixedZone("MSK", 3*3600)
	if got := nextWindowStart("Europe/Moscow", "10:00", at(5, 0)); !got.Equal(time.Date(2026, 10, 10, 10, 0, 0, 0, msk)) {
		t.Errorf("next start moscow = %v", got)
	}
	if got := FormatTime(model.Game{Timezone: "Europe/Moscow"}, at(9, 5), at(8, 0)); got != "12:05" {
		t.Errorf("FormatTime same day = %q", got)
	}
	if got := FormatTime(model.Game{Timezone: "Europe/Moscow"}, at(22, 5), at(8, 0)); got != "11.10 01:05" {
		t.Errorf("FormatTime next day = %q", got)
	}
}

func TestTick_LockedOrDbErrorIsReported(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings())
	ctx := context.Background()
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.store.Err = errors.New("db down")
	if err := w.svc.Tick(ctx, w.clock.t.Add(time.Hour)); err == nil {
		t.Fatal("tick with broken store returned nil")
	}
}

// fakeLLM — провайдер через app/llm.Call: записанный JSON по роли или ошибка; запоминает вызовы.
type fakeLLM struct {
	json    string // ответ интерпретатора
	narr    string // ответ рассказчика; пусто — ошибка вызова (шаблонная хроника)
	err     error
	narrErr error
	calls   []applm.Call
	inputs  []string
}

func (f *fakeLLM) Complete(_ context.Context, call applm.Call, req model.LLMRequest) (model.LLMResponse, error) {
	f.calls = append(f.calls, call)
	f.inputs = append(f.inputs, req.Input)
	if call.Role == model.RoleNarrator {
		if f.narrErr != nil || f.narr == "" {
			return model.LLMResponse{}, errors.New("narrator down")
		}
		return model.LLMResponse{JSON: json.RawMessage(f.narr), TokensIn: 80, TokensOut: 40}, nil
	}
	if f.err != nil {
		return model.LLMResponse{}, f.err
	}
	return model.LLMResponse{JSON: json.RawMessage(f.json), TokensIn: 50, TokensOut: 10}, nil
}

func (f *fakeLLM) byRole(role model.LLMRole) []applm.Call {
	var out []applm.Call
	for _, c := range f.calls {
		if c.Role == role {
			out = append(out, c)
		}
	}
	return out
}

// towerWorld — мир с локациями жанра и персонажами на стартовой локации с характеристиками (в app/game это делает
// создание игры и персонажа).
func towerWorld(t *testing.T) *world {
	t.Helper()
	s := model.DefaultGameSettings()
	s.MinRoundAnswers = 1
	w := newWorld(t, s)
	ctx := context.Background()
	genre, _ := w.store.GetGenre(ctx, w.game.GenreID)
	var start model.LocationID
	for i, lt := range genre.Template.Locations {
		l, err := w.store.CreateLocation(ctx, model.Location{GameID: w.game.ID, Name: lt.Name, Description: lt.Description, Difficulty: lt.Difficulty, Links: lt.Links, Objects: lt.Objects, Active: true, Start: lt.Start})
		if err != nil {
			t.Fatal(err)
		}
		if lt.Start || i == 0 {
			start = l.ID
		}
	}
	for i := range w.chars {
		w.chars[i].LocationID = &start
		if err := w.store.SaveCharacterState(ctx, w.game.ID, w.chars[i]); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// TestResolve_Interpreter: заявки раунда уходят в интерпретатор одним вызовом с ролью и раундом; свободное
// испытание из ответа модели разрешается движком; при сбое модели — разбор кодом, раунд публикуется.
func TestResolve_Interpreter(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	llm := &fakeLLM{}
	w.svc.LLM = llm
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	_, sub1, err := w.svc.Submit(ctx, w.game, w.chars[0], "прислушиваюсь к толпе на площади")
	if err != nil {
		t.Fatal(err)
	}
	_, sub2, err := w.svc.Submit(ctx, w.game, w.chars[1], "иду к башне")
	if err != nil {
		t.Fatal(err)
	}
	llm.json = fmt.Sprintf(`{"intents":[
	 {"submission_id":%d,"character_id":%d,"action_type":"explore","target":"","approach_idx":null,"attribute":"wits","skill":"awareness","risk":"normal","on_success":{"fact":"На площади тихо.","move":null,"reveal":null},"on_fail":{"condition":"exhausted","damage":null,"fact":null},"push":false,"ambiguities":[],"fallback":null},
	 {"submission_id":%d,"character_id":%d,"action_type":"move","target":"Зал Серой башни","approach_idx":null,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null}]}`,
		sub1.ID, sub1.CharacterID, sub2.ID, sub2.CharacterID)
	w.tick(t, 10*time.Minute)
	if calls := llm.byRole(model.RoleInterpreter); len(calls) != 1 || calls[0].Round == nil || calls[0].Game == nil || calls[0].PromptVersion == "" {
		t.Fatalf("llm calls = %+v", llm.calls)
	}
	info, _ := w.svc.Info(ctx, w.game)
	actions, _ := w.store.ListResolvedActions(ctx, w.game.ID, info.Last.ID)
	if len(actions) != 2 {
		t.Fatalf("actions = %+v", actions)
	}
	byChar := map[model.CharacterID]model.ActionResult{}
	for _, a := range actions {
		byChar[a.CharacterID] = a
	}
	free := byChar[w.chars[0].ID]
	if free.Status != model.ResultResolved || free.Check == nil || free.Check.Attribute != "wits" || free.Check.Difficulty != 1 || !free.Intent.FreeCheck() {
		t.Fatalf("free action = %+v", free)
	}
	if mv := byChar[w.chars[1].ID]; mv.Status != model.ResultResolved || mv.Intent.Target != "Зал Серой башни" {
		t.Fatalf("move = %+v", mv)
	}
	// Сбой модели: разбор кодом, безопасное толкование, раунд опубликован.
	llm.err = errors.New("provider down")
	w.reload(t)
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "что-то невнятное"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	info, _ = w.svc.Info(ctx, w.game)
	actions, _ = w.store.ListResolvedActions(ctx, w.game.ID, info.Last.ID)
	if info.Last.Status != model.RoundPublished || len(actions) != 1 || actions[0].Intent.ActionType != model.ActionWait || len(actions[0].Intent.Ambiguities) == 0 {
		t.Fatalf("fallback round = %+v / %+v", info.Last.Status, actions)
	}
}

// TestResolve_Narrator: после движка рассказчик получает один вызов с ролью и раундом; хроника в группу состоит из
// его текста по сценам и подходов следующего раунда с шансами, посчитанными кодом; ответ сохранён у раунда и уходит
// выжимкой в следующий вызов; длинная хроника делится на части; при сбое рассказчика — шаблонная хроника, раунд опубликован.
func TestResolve_Narrator(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	llm := &fakeLLM{}
	w.svc.LLM = llm
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	_, sub, err := w.svc.Submit(ctx, w.game, w.chars[0], "иду к башне")
	if err != nil {
		t.Fatal(err)
	}
	llm.json = fmt.Sprintf(`{"intents":[{"submission_id":%d,"character_id":%d,"action_type":"move","target":"Зал Серой башни","approach_idx":null,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null}]}`, sub.ID, sub.CharacterID)
	w.tick(t, 10*time.Minute)
	info, _ := w.svc.Info(ctx, w.game)
	r1 := *info.Last
	locs, _ := w.store.ListLocations(ctx, w.game.ID)
	byName := map[string]model.LocationID{}
	for _, l := range locs {
		byName[l.Name] = l.ID
	}
	events, _ := w.store.ListEvents(ctx, w.game.ID, r1.ID)
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	// Рассказчик вызван и упал (ответа нет): шаблонная хроника с подходами, раунд опубликован, хроника у раунда пуста.
	calls := llm.byRole(model.RoleNarrator)
	if len(calls) != 1 || calls[0].Round == nil || *calls[0].Round != r1.ID || calls[0].PromptVersion != "narrator/v1" {
		t.Fatalf("narrator calls = %+v", calls)
	}
	if r1.Status != model.RoundPublished || r1.Narration != nil {
		t.Fatalf("round after narrator failure = %+v", r1)
	}
	msgs := w.outbox(t)
	fallback := msgs[2]
	if fallback.Kind != model.OutboxChronicle || !strings.Contains(fallback.Text, "«Мир» — раунд 1 — итоги\n• Элис уходит из «Рыночная площадь» в «Зал Серой башни»") {
		t.Fatalf("fallback chronicle = %q", fallback.Text)
	}
	if !strings.Contains(fallback.Text, "— Рыночная площадь (Боб, Кэт) —\nВарианты: прочитать (Разум/внимательность, сложность 1: Боб 67 %, Кэт 67 %).") {
		t.Fatalf("fallback options = %q", fallback.Text)
	}
	// Второй раунд: рассказчик отвечает; в его контексте сцены, события и выжимки нет (прошлой хроники не было).
	w.reload(t)
	_, sub, err = w.svc.Submit(ctx, w.game, w.chars[1], "читаю доску")
	if err != nil {
		t.Fatal(err)
	}
	llm.json = fmt.Sprintf(`{"intents":[{"submission_id":%d,"character_id":%d,"action_type":"explore","target":"notice_board","approach_idx":0,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null}]}`, sub.ID, sub.CharacterID)
	square, hall := byName["Рыночная площадь"], byName["Зал Серой башни"]
	llm.narr = fmt.Sprintf(`{"chronicle":"Осень в Мире густеет.","scenes":[{"location_id":%d,"party":"Площадь","text":"Боб читает объявление.","event_seqs":[3]},{"location_id":%d,"party":"Башня","text":"Элис ждёт в зале.","event_seqs":[]}]}`, square, hall)
	w.tick(t, 10*time.Minute)
	info, _ = w.svc.Info(ctx, w.game)
	r2 := *info.Last
	if r2.Narration == nil || r2.Narration.PromptVersion != "narrator/v1" || len(r2.Narration.Scenes) != 2 || r2.Narration.Chronicle != "Осень в Мире густеет." {
		t.Fatalf("narration = %+v", r2.Narration)
	}
	input := llm.inputs[len(llm.inputs)-1]
	if !strings.Contains(input, `"round_no":2`) || !strings.Contains(input, `"actor":"Боб"`) || !strings.Contains(input, `"previous":[]`) || !strings.Contains(input, `"tone":"Маленький город`) {
		t.Fatalf("narrator input = %s", input)
	}
	var chron model.OutboxMessage
	for _, m := range w.outbox(t) {
		if m.Kind == model.OutboxChronicle && m.RoundID != nil && *m.RoundID == r2.ID {
			chron = m
		}
	}
	if chron.ID == 0 {
		t.Fatalf("chronicle message for round 2 missing: %+v", w.outbox(t))
	}
	want := "«Мир» — раунд 2\nОсень в Мире густеет.\n\n— Площадь —\nБоб читает объявление.\nВарианты: прочитать (Разум/внимательность, сложность 1: Боб 67 %, Кэт 67 %).\n\n— Башня —\nЭлис ждёт в зале."
	if chron.Text != want {
		t.Fatalf("chronicle text = %q\nwant %q", chron.Text, want)
	}
	// Третий раунд: выжимка прошлой хроники в контексте; длинный ответ делится на части с подписью продолжения.
	w.reload(t)
	if _, sub, err = w.svc.Submit(ctx, w.game, w.chars[1], "жду"); err != nil {
		t.Fatal(err)
	}
	llm.json = fmt.Sprintf(`{"intents":[{"submission_id":%d,"character_id":%d,"action_type":"wait","target":"","approach_idx":null,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null}]}`, sub.ID, sub.CharacterID)
	long := strings.Repeat("Ветер гонит листья по площади. ", 200)
	llm.narr = fmt.Sprintf(`{"chronicle":"Долгий вечер.","scenes":[{"location_id":%d,"party":"Площадь","text":"%s","event_seqs":[]},{"location_id":%d,"party":"Башня","text":"Тихо.","event_seqs":[]}]}`, square, long, hall)
	w.tick(t, 10*time.Minute)
	input = llm.inputs[len(llm.inputs)-1]
	if !strings.Contains(input, `"previous":["Осень в Мире густеет."]`) {
		t.Fatalf("previous digest missing: %s", input)
	}
	info, _ = w.svc.Info(ctx, w.game)
	var parts []model.OutboxMessage
	for _, m := range w.outbox(t) {
		if m.Kind == model.OutboxChronicle && m.RoundID != nil && *m.RoundID == info.Last.ID {
			parts = append(parts, m)
		}
	}
	if len(parts) != 3 || parts[1].Part != 2 || !strings.HasPrefix(parts[1].Text, "«Мир» — раунд 3 (продолжение)\n") || len([]rune(parts[0].Text)) > partLimit || !strings.Contains(parts[2].Text, "— Башня —\nТихо.") {
		t.Fatalf("parts = %d: %+v", len(parts), parts)
	}
}
