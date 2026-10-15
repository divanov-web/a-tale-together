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
	"taletogether/internal/service/narrator"
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

// begin — запуск кампании (ADR-0024): Begin переводит игру в `starting`, тик планировщика на том же времени делает
// старт; возвращает игру после старта.
func (w *world) begin(t *testing.T) model.Game {
	t.Helper()
	g, err := w.svc.Begin(context.Background(), w.game, 1)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != model.GameStarting || g.NextRoundAt == nil || !g.NextRoundAt.Equal(w.clock.t) {
		t.Fatalf("game after Begin = %+v", g)
	}
	w.tick(t, 0)
	return w.game
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
	g := w.begin(t)
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
	if msgs[0].TelegramChatID != -100 || msgs[0].Text != "«Мир» — начало истории\n\nВ Мир наступает осень." {
		t.Fatalf("scene = %+v", msgs[0])
	}
	if _, err := w.svc.Begin(ctx, g, 1); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second begin: %v", err)
	}
}

// TestBegin_StartingIsAsync (ADR-0024): после Begin игра в `starting` без сцены и раунда; повторный Begin, пауза и
// заявка отвечают «уже запускается»; тик делает старт один раз, следующий тик ничего не добавляет.
func TestBegin_StartingIsAsync(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings())
	ctx := context.Background()
	g, err := w.svc.Begin(ctx, w.game, 1)
	if err != nil || g.Status != model.GameStarting {
		t.Fatalf("begin = %+v, %v", g, err)
	}
	if msgs := w.outbox(t); len(msgs) != 0 {
		t.Fatalf("outbox before tick = %+v", msgs)
	}
	if _, err := w.svc.Begin(ctx, g, 1); !errors.Is(err, ErrStarting) {
		t.Fatalf("second begin: %v", err)
	}
	if _, _, err := w.svc.Pause(ctx, g, 1); !errors.Is(err, ErrStarting) {
		t.Fatalf("pause while starting: %v", err)
	}
	if _, _, err := w.svc.Submit(ctx, g, w.chars[0], "ждать"); !errors.Is(err, ErrStarting) {
		t.Fatalf("submit while starting: %v", err)
	}
	w.tick(t, 0)
	if w.game.Status != model.GameActive {
		t.Fatalf("game after tick = %+v", w.game)
	}
	w.tick(t, time.Second)
	if msgs := w.outbox(t); len(msgs) != 2 || msgs[0].Kind != model.OutboxScene || msgs[1].Kind != model.OutboxRoundOpen {
		t.Fatalf("outbox after start = %+v", msgs)
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

// TestBegin_SetupNotReady (ADR-0023): в `setup` кампания не запускается, пауза и завершение тоже отвечают «не настроен».
func TestBegin_SetupNotReady(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings())
	ctx := context.Background()
	w.game.Status = model.GameSetup
	if err := w.store.UpdateGame(ctx, w.game); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Begin(ctx, w.game, 1); !errors.Is(err, ErrSetup) {
		t.Fatalf("begin in setup: %v", err)
	}
	if _, _, err := w.svc.Pause(ctx, w.game, 1); !errors.Is(err, ErrSetup) {
		t.Fatalf("pause in setup: %v", err)
	}
	if _, err := w.svc.Finish(ctx, w.game, 1); !errors.Is(err, ErrSetup) {
		t.Fatalf("finish in setup: %v", err)
	}
}

func TestTick_SkipWithoutQuorumAndCarrySubmissions(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings()) // кворум 2
	ctx := context.Background()
	w.begin(t)
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
	if len(msgs) != 3 || msgs[2].Kind != model.OutboxRoundOpen || msgs[2].RoundID == nil || *msgs[2].RoundID != last.ID {
		t.Fatalf("outbox = %+v", msgs)
	}
	// Пропуск и открытие — одно сообщение (B4).
	if msgs[2].Text != "Раунд 1\nНедостаточное количество участников. Раунд пропущен.\n\nРаунд 2 открыт\nЗаявки до 12:20: /act <текст> здесь или в личке с ботом. Соседние места: /scout\nПеренесено заявок из пропущенного раунда: 1." {
		t.Fatalf("open text = %q", msgs[2].Text)
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
	if len(msgs) != 4 || msgs[3].Kind != model.OutboxRoundOpen || strings.Contains(msgs[3].Text, SkippedText) {
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
	w.begin(t)
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
	if msgs[2].Text != "Раунд 1 — итоги\n\n• тихий раунд: никто ничего заметного не сделал" {
		t.Fatalf("closed text = %q", msgs[2].Text)
	}
	// Статусы: Элис active (0 пропусков), Боб waiting (2 пропуска), Кэт leaving (3 пропуска — порог, один раунд; B13).
	want := map[string]struct {
		status model.CharacterStatus
		missed int
	}{"Элис": {model.CharacterActive, 0}, "Боб": {model.CharacterWaiting, 2}, "Кэт": {model.CharacterLeaving, 3}}
	for _, c := range w.chars {
		got, _ := w.store.GetCharacter(ctx, w.game.ID, c.SeasonID, c.UserID)
		if got.Status != want[c.Nickname].status || got.MissedRounds != want[c.Nickname].missed {
			t.Fatalf("%s = %s/%d; want %+v", c.Nickname, got.Status, got.MissedRounds, want[c.Nickname])
		}
	}
	// Ещё один пропуск: Кэт inactive, в снимке статус для рассказчика уже «вне повествования».
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "действую"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	got, _ := w.store.GetCharacter(ctx, w.game.ID, w.chars[2].SeasonID, w.chars[2].UserID)
	if got.Status != model.CharacterInactive || got.MissedRounds != 4 {
		t.Fatalf("inactive character = %s/%d", got.Status, got.MissedRounds)
	}
	// Возврат заявкой после inactive: active, пропуски обнулены, отметка returned.
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[2], "вернулась"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	got, _ = w.store.GetCharacter(ctx, w.game.ID, w.chars[2].SeasonID, w.chars[2].UserID)
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
	w.begin(t)
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
	w.begin(t)
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
	if len(kinds) != 5 || kinds[2] != model.OutboxSystem || kinds[3] != model.OutboxSystem || kinds[4] != model.OutboxRoundOpen {
		// раунд 1 без заявок пропущен: сообщение о пропуске в одном round_open с анонсом раунда 2
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
	w.begin(t)
	w.store.Err = errors.New("db down")
	if err := w.svc.Tick(ctx, w.clock.t.Add(time.Hour)); err == nil {
		t.Fatal("tick with broken store returned nil")
	}
}

// fakeLLM — провайдер через app/llm.Call: записанный JSON по роли или ошибка; запоминает вызовы.
type fakeLLM struct {
	json    string // ответ интерпретатора
	narr    string // ответ рассказчика; пусто — ошибка вызова (шаблонная хроника)
	opening string // ответ завязки; пусто — ошибка вызова (стартовая сцена жанра)
	setups  string // ответ генерации мира; пусто — ошибка вызова (попытка не удалась)
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
		return model.LLMResponse{JSON: json.RawMessage(f.narr), Usage: model.LLMUsage{Input: 80, Output: 40}}, nil
	}
	if call.Role == model.RoleOpening {
		if f.opening == "" {
			return model.LLMResponse{}, errors.New("opening down")
		}
		return model.LLMResponse{JSON: json.RawMessage(f.opening), Usage: model.LLMUsage{Input: 60, Output: 30}}, nil
	}
	if call.Role == model.RoleWorldgen {
		if f.setups == "" {
			return model.LLMResponse{}, errors.New("worldgen down")
		}
		return model.LLMResponse{JSON: json.RawMessage(f.setups), Usage: model.LLMUsage{Input: 70, Output: 35}}, nil
	}
	if f.err != nil {
		return model.LLMResponse{}, f.err
	}
	return model.LLMResponse{JSON: json.RawMessage(f.json), Usage: model.LLMUsage{Input: 50, Output: 10}}, nil
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

// towerWorld — мир на сетке (пункт 27) с персонажами на стартовой клетке: площадь жанра `(0,0)` с доской объявлений,
// к северу зал Серой башни `(0,1)`, за ним подвал `(0,2)` с запертой дверью (в app/game стартовую клетку создаёт
// создание игры, остальное достраивает рассказчик).
func towerWorld(t *testing.T) *world {
	t.Helper()
	s := model.DefaultGameSettings()
	s.MinRoundAnswers = 1
	w := newWorld(t, s)
	ctx := context.Background()
	genre, _ := w.store.GetGenre(ctx, w.game.GenreID)
	lt := genre.Template.Locations[0]
	cells := []model.Location{
		{GameID: w.game.ID, Name: lt.Name, Description: lt.Description, Difficulty: lt.Difficulty, Terrain: lt.Terrain, Objects: lt.Objects, Start: true, X: 0, Y: 0},
		{GameID: w.game.ID, Name: "Зал Серой башни", Description: "Пыльный зал.", Terrain: "ruins", X: 0, Y: 1},
		{GameID: w.game.ID, Name: "Подвал башни", Description: "Сырой подвал.", Difficulty: 1, Terrain: "cellar", X: 0, Y: 2,
			Objects: []model.LocationObject{{Key: "cellar_door", Title: "запертая дверь", State: "locked", Approaches: []model.Approach{
				{Action: model.ActionInteract, Title: "выбить", Attribute: "strength", Skill: "athletics", Difficulty: 4, OnSuccess: json.RawMessage(`{"state":"broken"}`), OnFail: json.RawMessage(`{"condition":"exhausted"}`)},
				{Action: model.ActionInteract, Title: "отпереть", Attribute: "agility", Skill: "thievery", Difficulty: 4, OnSuccess: json.RawMessage(`{"state":"open"}`)},
			}}}},
	}
	var start model.LocationID
	for i, cell := range cells {
		l, err := w.store.CreateLocation(ctx, cell)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
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
	w.begin(t)
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
	if free.Status != model.ResultResolved || free.Check == nil || free.Check.Attribute != "wits" || free.Check.Difficulty != 4 || !free.Intent.FreeCheck() {
		t.Fatalf("free action = %+v", free)
	}
	if mv := byChar[w.chars[1].ID]; mv.Status != model.ResultResolved || mv.Intent.Target != "north" {
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
	w.begin(t)
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
	// Пункт 19: уход Элис из группы по умолчанию даёт `party_split` и пару событий перехода; пункт 27: первый вход
	// фракции в зал — приватное `cell_discovered`, недостающие соседи зала (запад, восток) — шаблонные карточки
	// `cell_generated`, потому что рассказчик упал; пункт 28: первый этап шаблонного сюжетного квеста Ордена
	// размещается в зале шаблонным объектом «след» (`object_created`, `quest_stage_placed`).
	if len(events) != 8 || events[0].Type != model.EventPartySplit || events[1].Type != model.EventCharacterMoved || events[3].Type != model.EventCellDiscovered || events[4].Type != model.EventCellGenerated || events[5].Type != model.EventCellGenerated || events[6].Type != model.EventObjectCreated || events[7].Type != model.EventQuestStagePlaced {
		t.Fatalf("events = %+v", events)
	}
	// Карта: три клетки мира, три шаблонных соседа площади от стартового пакета без модели, два соседа зала.
	if locs, _ := w.store.ListLocations(ctx, w.game.ID); len(locs) != 8 || locs[6].Stage != model.StageCard || locs[6].Name != model.TemplateCardName || locs[6].X != -1 || locs[6].Y != 1 || locs[7].X != 1 {
		t.Fatalf("locations after templated neighbors = %+v", locs)
	}
	// Рассказчик вызван и упал (ответа нет): шаблонная хроника с подходами, раунд опубликован, хроника у раунда пуста.
	calls := llm.byRole(model.RoleNarrator)
	if len(calls) != 1 || calls[0].Round == nil || *calls[0].Round != r1.ID || calls[0].PromptVersion != narrator.PromptVersion {
		t.Fatalf("narrator calls = %+v", calls)
	}
	if r1.Status != model.RoundPublished || r1.Narration != nil {
		t.Fatalf("round after narrator failure = %+v", r1)
	}
	msgs := w.outbox(t)
	fallback := msgs[2]
	if fallback.Kind != model.OutboxChronicle || !strings.Contains(fallback.Text, "Раунд 1 — итоги\n\n• Элис (@Элис) отделяется от группы и уходит своей дорогой из «Рыночная площадь» в «Зал Серой башни»\n• Элис уходит из «Рыночная площадь» в «Зал Серой башни»") {
		t.Fatalf("fallback chronicle = %q", fallback.Text)
	}
	if !strings.Contains(fallback.Text, "— Рыночная площадь (Боб, Кэт) —\n\nВарианты:\n• разобрать приписку (Разум/внимательность, сложность 4)") {
		t.Fatalf("fallback options = %q", fallback.Text)
	}
	// Второй раунд: рассказчик отвечает; в его контексте сцены, события и выжимка только из завязки (прошлой хроники не было).
	w.reload(t)
	_, sub, err = w.svc.Submit(ctx, w.game, w.chars[1], "читаю доску")
	if err != nil {
		t.Fatal(err)
	}
	llm.json = fmt.Sprintf(`{"intents":[{"submission_id":%d,"character_id":%d,"action_type":"explore","target":"notice_board","approach_idx":0,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null}]}`, sub.ID, sub.CharacterID)
	square, hall := byName["Рыночная площадь"], byName["Зал Серой башни"]
	// Сцены по группам (пункт 19): 1 — «Ночной дозор» на площади, 2 — «Вольные клинки» в зале.
	// Событие осмотра — seq 9: в первом раунде восемь событий (пункт 27 добавил открытие клетки и две карточки,
	// пункт 28 — объект и привязку шаблонного этапа квеста).
	llm.narr = `{"chronicle":"Осень в Мире густеет.","scenes":[{"scene_id":1,"text":"Боб читает объявление.","event_seqs":[9]},{"scene_id":2,"text":"Элис ждёт в зале.","event_seqs":[]}]}`
	w.tick(t, 10*time.Minute)
	info, _ = w.svc.Info(ctx, w.game)
	r2 := *info.Last
	if r2.Narration == nil || r2.Narration.PromptVersion != narrator.PromptVersion || len(r2.Narration.Scenes) != 2 || r2.Narration.Chronicle != "Осень в Мире густеет." {
		t.Fatalf("narration = %+v", r2.Narration)
	}
	if r2.Narration.Scenes[0].LocationID != square || r2.Narration.Scenes[1].LocationID != hall {
		t.Fatalf("scene locations = %+v", r2.Narration.Scenes)
	}
	input := llm.inputs[len(llm.inputs)-1]
	if !strings.Contains(input, `"round_no":2`) || !strings.Contains(input, `"actor":"Боб"`) || !strings.Contains(input, `"previous":["В Мир наступает осень."]`) || !strings.Contains(input, `"tone":"Маленький город`) || strings.Contains(input, "Ночной дозор") || strings.Contains(input, "leader") {
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
	// B36: переходов среди «Вариантов» нет.
	want := "Раунд 2\n\nОсень в Мире густеет.\n\n— Рыночная площадь —\nБоб (@Боб) читает объявление.\n\nВарианты:\n• разобрать приписку (Разум/внимательность, сложность 4)\n\n— Зал Серой башни —\nЭлис (@Элис) ждёт в зале.\n\nВарианты:\n• разобраться на месте (без броска)"
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
	llm.narr = fmt.Sprintf(`{"chronicle":"Долгий вечер.","scenes":[{"scene_id":1,"text":"%s","event_seqs":[]},{"scene_id":2,"text":"Тихо.","event_seqs":[]}]}`, long)
	w.tick(t, 10*time.Minute)
	input = llm.inputs[len(llm.inputs)-1]
	if !strings.Contains(input, `"previous":["В Мир наступает осень.","Осень в Мире густеет."]`) {
		t.Fatalf("previous digest missing: %s", input)
	}
	info, _ = w.svc.Info(ctx, w.game)
	var parts []model.OutboxMessage
	for _, m := range w.outbox(t) {
		if m.Kind == model.OutboxChronicle && m.RoundID != nil && *m.RoundID == info.Last.ID {
			parts = append(parts, m)
		}
	}
	if len(parts) != 3 || parts[1].Part != 2 || !strings.HasPrefix(parts[1].Text, "Раунд 3 (продолжение)\n") || len([]rune(parts[0].Text)) > partLimit || !strings.Contains(parts[2].Text, "— Зал Серой башни —\nТихо.") {
		t.Fatalf("parts = %d: %+v", len(parts), parts)
	}
}

func TestDeadline_RoundsDownToMinute(t *testing.T) {
	base := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if got := deadline(base.Add(20*time.Second), 5); !got.Equal(base.Add(5 * time.Minute)) {
		t.Errorf("deadline with seconds = %v", got)
	}
	if got := deadline(base, 5); !got.Equal(base.Add(5 * time.Minute)) {
		t.Errorf("deadline on the minute = %v", got)
	}
	// Следующий раунд открыт через 40 с после дедлайна предыдущего: шаг между дедлайнами остаётся ровно 5 минут.
	prev := base.Add(5 * time.Minute)
	if got := deadline(prev.Add(40*time.Second), 5); !got.Equal(prev.Add(5 * time.Minute)) {
		t.Errorf("deadline after resolution = %v", got)
	}
}

// TestFreeze_EntryMarks: первый раунд — все персонажи `new`; новый персонаж посреди игры — `new`; заявка после
// `inactive` — `returned`; остальные без отметки (B1).
func TestFreeze_EntryMarks(t *testing.T) {
	t.Parallel()
	s := model.DefaultGameSettings()
	s.MinRoundAnswers = 1
	s.InactiveAfterRounds = 1
	w := newWorld(t, s)
	ctx := context.Background()
	w.begin(t)
	w.reload(t)
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[1], "осматриваюсь"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute) // раунд 1 заморожен: все впервые
	r1, _ := w.store.RoundByNo(ctx, w.game.ID, 1)
	for _, c := range r1.Snapshot.Characters {
		if c.Entry != model.EntryNew {
			t.Fatalf("round 1 entry of %s = %q", c.Nickname, c.Entry)
		}
	}
	// Элис пропустила раунд 1 → leaving (порог 1); её заявка даёт отметку returned. Новый персонаж Дэн вступает перед раундом 2.
	u, _ := w.store.UpsertUser(ctx, model.TelegramUser{ID: 99, Username: "dan"})
	_ = w.store.AddMember(ctx, model.GameMember{GameID: w.game.ID, UserID: u.ID, Role: model.RolePlayer, Status: model.MemberActive})
	dan, err := w.store.CreateCharacter(ctx, model.Character{GameID: w.game.ID, SeasonID: w.chars[0].SeasonID, UserID: u.ID, ModelID: 1, Nickname: "Дэн", Archetype: "rogue", Status: model.CharacterActive, FactionID: w.chars[0].FactionID,
		Attributes: map[string]int{"strength": 2, "agility": 3, "wits": 4, "will": 3}, Skills: map[string]int{"awareness": 2}, LocationID: w.chars[0].LocationID})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []model.Character{w.chars[0], dan} {
		if _, _, err := w.svc.Submit(ctx, w.game, c, "осматриваюсь"); err != nil {
			t.Fatal(err)
		}
	}
	w.tick(t, 10*time.Minute)
	r2, _ := w.store.RoundByNo(ctx, w.game.ID, 2)
	got := map[string]string{}
	for _, c := range r2.Snapshot.Characters {
		got[c.Nickname] = c.Entry
	}
	if got["Дэн"] != model.EntryNew || got["Элис"] != model.EntryReturned || got["Боб"] != "" || got["Кэт"] != "" {
		t.Fatalf("round 2 entries = %v", got)
	}
}

func TestTagMentions(t *testing.T) {
	t.Parallel()
	tags := map[string]string{"Кот": "@cat", "Котофей": "@kotofey", "Элис": "@alice", "Без": "@none"}
	text := "Раунд 3\nКотофей и Кот идут к Элис.\n\n— Площадь (Кот, Элис) —\nКот ждёт. Элис смотрит на Кота."
	want := "Раунд 3\nКотофей (@kotofey) и Кот (@cat) идут к Элис (@alice).\n\n— Площадь (Кот, Элис) —\nКот ждёт. Элис смотрит на Кота."
	if got := tagMentions(text, tags); got != want {
		t.Errorf("tagMentions =\n%s\nwant\n%s", got, want)
	}
	if got := tagMentions("нет никого", nil); got != "нет никого" {
		t.Errorf("no tags = %q", got)
	}
}

// TestActivity: пропуски → waiting, на пороге один раунд leaving, дальше inactive; заявка возвращает active из любого статуса (B13).
func TestActivity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status model.CharacterStatus
		missed int
		acted  bool
		want   model.CharacterStatus
		wantN  int
	}{
		{model.CharacterActive, 0, false, model.CharacterWaiting, 1},
		{model.CharacterWaiting, 1, false, model.CharacterWaiting, 2},
		{model.CharacterWaiting, 2, false, model.CharacterLeaving, 3},
		{model.CharacterLeaving, 3, false, model.CharacterInactive, 4},
		{model.CharacterInactive, 4, false, model.CharacterInactive, 5},
		{model.CharacterLeaving, 3, true, model.CharacterActive, 0},
		{model.CharacterInactive, 7, true, model.CharacterActive, 0},
	}
	for _, c := range cases {
		got, n := activity(model.Character{Status: c.status, MissedRounds: c.missed}, c.acted, 3)
		if got != c.want || n != c.wantN {
			t.Errorf("activity(%s/%d, acted=%v) = %s/%d; want %s/%d", c.status, c.missed, c.acted, got, n, c.want, c.wantN)
		}
	}
	if got, n := activity(model.Character{Status: model.CharacterActive}, false, 1); got != model.CharacterLeaving || n != 1 {
		t.Errorf("threshold 1: %s/%d", got, n)
	}
	// Уход по слову игрока (ADR-0026): без ожидания порога leaving, затем inactive; пропуски считаются; заявка возвращает.
	away := []struct {
		status model.CharacterStatus
		missed int
		acted  bool
		want   model.CharacterStatus
		wantN  int
	}{
		{model.CharacterActive, 0, false, model.CharacterLeaving, 1},
		{model.CharacterWaiting, 1, false, model.CharacterLeaving, 2},
		{model.CharacterLeaving, 1, false, model.CharacterInactive, 2},
		{model.CharacterInactive, 2, false, model.CharacterInactive, 3},
		{model.CharacterLeaving, 1, true, model.CharacterActive, 0},
	}
	for _, c := range away {
		got, n := activity(model.Character{Status: c.status, MissedRounds: c.missed, Away: true}, c.acted, 3)
		if got != c.want || n != c.wantN {
			t.Errorf("activity(away %s/%d, acted=%v) = %s/%d; want %s/%d", c.status, c.missed, c.acted, got, n, c.want, c.wantN)
		}
	}
}

// TestAway_LeaveAndBack — S20 (пункт 20, ADR-0026): /away снимает заявку и с ближайшей заморозки ведёт в leaving, затем
// inactive; /back при открытом раунде — заявка «жду», возврат в active с отметкой returned; до заморозки — только отмена.
func TestAway_LeaveAndBack(t *testing.T) {
	t.Parallel()
	s := model.DefaultGameSettings()
	s.MinRoundAnswers = 1
	w := newWorld(t, s)
	ctx := context.Background()
	w.begin(t)
	w.reload(t)
	alice, bob := w.chars[0], w.chars[1]
	r1, _, err := w.svc.Submit(ctx, w.game, bob, "осматриваюсь")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Back(ctx, w.game, bob); !errors.Is(err, ErrNotAway) {
		t.Fatalf("Back in story: %v", err)
	}
	cancelled, err := w.svc.Away(ctx, w.game, bob)
	if err != nil || !cancelled {
		t.Fatalf("Away = %v, %v", cancelled, err)
	}
	if _, err := w.store.GetSubmission(ctx, w.game.ID, r1.ID, bob.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("submission after Away: %v", err)
	}
	bob, _ = w.store.GetCharacter(ctx, w.game.ID, bob.SeasonID, bob.UserID)
	if !bob.Away {
		t.Fatal("away flag not set")
	}
	if _, err := w.svc.Away(ctx, w.game, bob); !errors.Is(err, ErrAlreadyAway) {
		t.Fatalf("second Away: %v", err)
	}
	// До заморозки /back только отменяет уход: заявки нет.
	if r, err := w.svc.Back(ctx, w.game, bob); err != nil || r != nil {
		t.Fatalf("Back before freeze = %v, %v", r, err)
	}
	bob, _ = w.store.GetCharacter(ctx, w.game.ID, bob.SeasonID, bob.UserID)
	if bob.Away || bob.Status != model.CharacterActive {
		t.Fatalf("after cancel: away=%v status=%s", bob.Away, bob.Status)
	}
	if _, err := w.store.GetSubmission(ctx, w.game.ID, r1.ID, bob.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("submission after cancel: %v", err)
	}
	if _, err := w.svc.Away(ctx, w.game, bob); err != nil {
		t.Fatal(err)
	}
	// Заморозка 1: Боб leaving с одним пропуском; заморозка 2: inactive.
	for i, want := range []model.CharacterStatus{model.CharacterLeaving, model.CharacterInactive} {
		if _, _, err := w.svc.Submit(ctx, w.game, alice, "осматриваюсь"); err != nil {
			t.Fatal(err)
		}
		w.tick(t, 10*time.Minute)
		bob, _ = w.store.GetCharacter(ctx, w.game.ID, bob.SeasonID, bob.UserID)
		if bob.Status != want || bob.MissedRounds != i+1 || !bob.Away {
			t.Fatalf("round %d: status=%s missed=%d away=%v", i+1, bob.Status, bob.MissedRounds, bob.Away)
		}
		r, _ := w.store.RoundByNo(ctx, w.game.ID, i+1)
		for _, c := range r.Snapshot.Characters {
			if c.ID == bob.ID && (c.Status != want || !c.Away) {
				t.Fatalf("snapshot %d: %+v", i+1, c)
			}
		}
	}
	// /back при открытом раунде 3: заявка «жду», после заморозки active с отметкой returned, пропуски обнулены.
	r3, err := w.svc.Back(ctx, w.game, bob)
	if err != nil || r3 == nil || r3.RoundNo != 3 {
		t.Fatalf("Back = %v, %v", r3, err)
	}
	if sub, err := w.store.GetSubmission(ctx, w.game.ID, r3.ID, bob.ID); err != nil || sub.RawText != BackSubmission {
		t.Fatalf("back submission = %+v, %v", sub, err)
	}
	w.tick(t, 10*time.Minute)
	bob, _ = w.store.GetCharacter(ctx, w.game.ID, bob.SeasonID, bob.UserID)
	if bob.Status != model.CharacterActive || bob.MissedRounds != 0 || bob.Away {
		t.Fatalf("after back: status=%s missed=%d away=%v", bob.Status, bob.MissedRounds, bob.Away)
	}
	r, _ := w.store.RoundByNo(ctx, w.game.ID, 3)
	for _, c := range r.Snapshot.Characters {
		if c.ID == bob.ID && c.Entry != model.EntryReturned {
			t.Fatalf("entry after back = %q", c.Entry)
		}
	}
	// Любая заявка снимает пометку; /away выбывшему по смерти — отказ.
	if _, err := w.svc.Away(ctx, w.game, bob); err != nil {
		t.Fatal(err)
	}
	bob, _ = w.store.GetCharacter(ctx, w.game.ID, bob.SeasonID, bob.UserID)
	if _, _, err := w.svc.Submit(ctx, w.game, bob, "иду на север"); err != nil {
		t.Fatal(err)
	}
	if bob, _ = w.store.GetCharacter(ctx, w.game.ID, bob.SeasonID, bob.UserID); bob.Away {
		t.Fatal("submission did not clear away")
	}
	dead := bob
	dead.Status, dead.DeathRoundsLeft = model.CharacterDead, 2
	if _, err := w.svc.Away(ctx, w.game, dead); !errors.Is(err, ErrDead) {
		t.Fatalf("Away dead: %v", err)
	}
}
