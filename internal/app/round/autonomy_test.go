package round

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"taletogether/internal/adapter/postgres/fake"
	"taletogether/internal/app/delivery"
	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
	"taletogether/internal/service/engine"
)

// oracleLLM — провайдер для автономного прогона: намерения строит по снимку замороженного раунда из хранилища
// (как сделала бы модель), по номеру раунда имитирует сбои: ошибка интерпретатора, ошибка рассказчика, паника конвейера.
type oracleLLM struct {
	store        *fake.Store
	game         model.GameID
	interpDown   map[int]bool
	narratorDown map[int]bool
	crashes      map[int]int
	calls        int
}

func (o *oracleLLM) Complete(ctx context.Context, call applm.Call, req model.LLMRequest) (model.LLMResponse, error) {
	o.calls++
	r, err := o.store.LastRound(ctx, o.game)
	if err != nil {
		return model.LLMResponse{}, err
	}
	if o.crashes[r.RoundNo] > 0 {
		o.crashes[r.RoundNo]--
		panic(fmt.Sprintf("pipeline crashed in round %d", r.RoundNo))
	}
	if call.Role == model.RoleNarrator {
		if o.narratorDown[r.RoundNo] {
			return model.LLMResponse{}, errors.New("narrator down")
		}
		return model.LLMResponse{JSON: json.RawMessage(fmt.Sprintf(`{"chronicle":"Раунд %d прошёл.","scenes":[]}`, r.RoundNo)), TokensIn: 100, TokensOut: 20}, nil
	}
	if o.interpDown[r.RoundNo] {
		return model.LLMResponse{}, errors.New("interpreter down")
	}
	if r.Snapshot == nil {
		return model.LLMResponse{}, errors.New("no snapshot")
	}
	var intents []map[string]any
	for _, sub := range r.Snapshot.Submissions {
		c, _ := r.Snapshot.Character(sub.CharacterID)
		in := map[string]any{"submission_id": sub.SubmissionID, "character_id": sub.CharacterID, "action_type": "wait", "target": "", "approach_idx": nil, "attribute": nil, "skill": nil, "risk": nil, "on_success": nil, "on_fail": nil, "push": false, "ambiguities": []string{}, "fallback": nil}
		if loc, ok := r.Snapshot.Location(*c.LocationID); ok && sub.Text == "идти дальше" && len(loc.Links) > 0 {
			in["action_type"], in["target"] = "move", loc.Links[0].To
		}
		intents = append(intents, in)
	}
	raw, _ := json.Marshal(map[string]any{"intents": intents})
	return model.LLMResponse{JSON: raw, TokensIn: 200, TokensOut: 50}, nil
}

// flakySender — Telegram с временными сбоями: каждая четвёртая попытка падает, десятая просит подождать.
type flakySender struct {
	attempts int
	sent     int
}

func (f *flakySender) Send(context.Context, model.TelegramChatID, string, model.Keyboard) (int64, error) {
	f.attempts++
	switch {
	case f.attempts == 10:
		return 0, &model.RetryAfterError{After: 20 * time.Second, Err: errors.New("too many requests")}
	case f.attempts%4 == 0:
		return 0, errors.New("network")
	}
	f.sent++
	return int64(f.sent), nil
}

// TestTenRounds_Autonomous — критерий готовности пункта 10 в песочнице фейков: 10 раундов с 5 персонажами по
// расписанию с ночной паузой, сбоями интерпретатора, рассказчика, конвейера (откат и повтор, затем `failed`),
// пропуском без заявок и сбоями Telegram; без ручного вмешательства, без потерь и дублей; состояние мира
// совпадает со свёрткой журнала событий, каждый раунд воспроизводим.
func TestTenRounds_Autonomous(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	w.clock.t = time.Date(2026, 10, 10, 19, 35, 0, 0, time.UTC)
	g := w.game
	g.PlayFrom, g.PlayTo = "08:00", "20:00"
	g.Settings.MaxRounds = 10
	g.Settings.MinRoundAnswers = 1
	if err := w.store.UpdateGame(ctx, g); err != nil {
		t.Fatal(err)
	}
	season, _ := w.store.ActiveSeason(ctx, g.ID)
	start := w.chars[0].LocationID
	for i, nick := range []string{"Дан", "Ева"} {
		u, _ := w.store.UpsertUser(ctx, model.TelegramUser{ID: model.TelegramUserID(20 + i), Username: nick})
		_ = w.store.AddMember(ctx, model.GameMember{GameID: g.ID, UserID: u.ID, Role: model.RolePlayer, Status: model.MemberActive})
		ch, err := w.store.CreateCharacter(ctx, model.Character{GameID: g.ID, SeasonID: season.ID, UserID: u.ID, ModelID: 1, Nickname: nick, Archetype: "warrior", Status: model.CharacterActive, FactionID: w.chars[0].FactionID, HP: 8, LocationID: start,
			Attributes: map[string]int{"strength": 4, "agility": 3, "wits": 2, "will": 3}, Skills: map[string]int{"melee": 3}})
		if err != nil {
			t.Fatal(err)
		}
		w.chars = append(w.chars, ch)
	}
	for _, c := range w.chars {
		_ = w.store.MarkStarted(ctx, c.UserID)
	}
	llm := &oracleLLM{store: w.store, game: g.ID, interpDown: map[int]bool{3: true, 7: true}, narratorDown: map[int]bool{4: true}, crashes: map[int]int{5: 1, 6: 3}}
	w.svc.LLM = llm
	j := &fakeJournal{}
	w.svc.Journal = j
	sender := &flakySender{}
	worker := delivery.New(w.store, sender, w.clock, delivery.Settings{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	deliverAll := func() {
		for pass := 0; pass < 50; pass++ {
			if _, err := worker.Deliver(ctx); err != nil {
				t.Fatal(err)
			}
			pending := false
			for _, m := range w.outbox(t) {
				pending = pending || m.Status == model.OutboxPending || m.Status == model.OutboxSending
			}
			if !pending {
				return
			}
			w.clock.t = w.clock.t.Add(5 * time.Second)
		}
		t.Fatal("outbox not drained")
	}
	plan := map[int][]string{ // заявки по раундам: номер → тексты персонажей 0..4 (пусто — без заявки)
		1:  {"идти дальше", "ждать", "осмотреть доску", "", ""},
		3:  {"идти к башне", "ждать", "", "ждать", ""},
		4:  {"идти дальше", "", "", "", "ждать"},
		5:  {"ждать", "ждать", "", "", ""},
		6:  {"идти дальше", "", "ждать", "", ""},
		7:  {"", "", "", "ждать", ""},
		8:  {"идти дальше", "ждать", "ждать", "ждать", "ждать"},
		9:  {"", "идти дальше", "", "", ""},
		10: {"ждать", "", "", "", "идти дальше"},
	}
	if _, err := w.svc.Begin(ctx, g, 1); err != nil {
		t.Fatal(err)
	}
	ticks, failedTicks := 0, 0
	submitted := map[int]bool{}
	for step := 0; step < 80; step++ {
		g = w.reload(t)
		if g.Status == model.GameFinished {
			break
		}
		if open, err := w.store.OpenRound(ctx, g.ID); err == nil && open.DeadlineAt.After(w.clock.t) && !submitted[open.RoundNo] {
			submitted[open.RoundNo] = true
			for i, text := range plan[open.RoundNo] {
				if text == "" {
					continue
				}
				c, _ := w.store.GetCharacter(ctx, g.ID, season.ID, w.chars[i].UserID)
				if _, _, err := w.svc.Submit(ctx, g, c, text); err != nil {
					t.Fatalf("round %d, %s: %v", open.RoundNo, c.Nickname, err)
				}
			}
		}
		if g.NextRoundAt == nil {
			t.Fatalf("active game without timer at step %d: %+v", step, g)
		}
		if g.NextRoundAt.After(w.clock.t) {
			w.clock.t = g.NextRoundAt.Add(time.Second)
		}
		ticks++
		if err := w.svc.Tick(ctx, w.clock.Now()); err != nil {
			failedTicks++
		}
		deliverAll()
	}
	g = w.reload(t)
	if g.Status != model.GameFinished || failedTicks != 4 {
		t.Fatalf("game = %+v, failed ticks = %d", g, failedTicks)
	}
	// Раунды: статусы и запасные пути по плану сбоев.
	want := map[int]model.RoundStatus{1: model.RoundPublished, 2: model.RoundSkipped, 3: model.RoundPublished, 4: model.RoundPublished, 5: model.RoundPublished, 6: model.RoundFailed, 7: model.RoundPublished, 8: model.RoundPublished, 9: model.RoundPublished, 10: model.RoundPublished}
	rounds := map[int]model.Round{}
	for no := 1; no <= 10; no++ {
		r, err := w.store.RoundByNo(ctx, g.ID, no)
		if err != nil || r.Status != want[no] {
			t.Fatalf("round %d = %+v, %v; want %s", no, r, err, want[no])
		}
		rounds[no] = r
	}
	if _, err := w.store.RoundByNo(ctx, g.ID, 11); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("round 11 exists: %v", err)
	}
	if m := rounds[3].Metrics; m == nil || !m.InterpreterFallback || m.NarratorFallback {
		t.Fatalf("round 3 metrics = %+v", m)
	}
	if m := rounds[4].Metrics; m == nil || m.InterpreterFallback || !m.NarratorFallback {
		t.Fatalf("round 4 metrics = %+v", m)
	}
	if m := rounds[5].Metrics; m == nil || m.Attempt != 2 || rounds[5].Attempts != 1 {
		t.Fatalf("round 5 metrics = %+v attempts %d", m, rounds[5].Attempts)
	}
	if rounds[6].Attempts != 3 || rounds[6].SkipReason == "" {
		t.Fatalf("round 6 = %+v", rounds[6])
	}
	// Ночная пауза: раунд 3 закрылся после 20:00, раунд 4 открылся в 08:00 следующего дня.
	if rounds[4].OpensAt.Hour() != 8 || rounds[4].OpensAt.Minute() != 0 || rounds[4].OpensAt.Day() != 11 {
		t.Fatalf("round 4 opened at %v; want 08:00 next day", rounds[4].OpensAt)
	}
	// Никто не потерян: у каждой заявки замороженного раунда ровно один итог, у каждого итога — уведомление в личку.
	msgs := w.outbox(t)
	notices := map[model.RoundID]int{}
	seen := map[string]bool{}
	for _, m := range msgs {
		if m.Status != model.OutboxSent || m.TelegramMessageID == nil {
			t.Fatalf("undelivered message: %+v", m)
		}
		if m.RoundID != nil {
			key := fmt.Sprintf("%d/%d/%s/%d", *m.RoundID, m.TelegramChatID, m.Kind, m.Part)
			if seen[key] {
				t.Fatalf("duplicate outbox message %s", key)
			}
			seen[key] = true
			if m.Kind == model.OutboxPrivate {
				notices[*m.RoundID]++
			}
		}
	}
	if sender.sent != len(msgs) {
		t.Fatalf("sent %d messages for %d outbox rows", sender.sent, len(msgs))
	}
	var prevState *model.RoundSnapshot
	var lastSeq int64
	for no := 1; no <= 10; no++ {
		r := rounds[no]
		if r.Status != model.RoundPublished {
			continue
		}
		results, _ := w.store.ListResolvedActions(ctx, g.ID, r.ID)
		if len(results) != len(r.Snapshot.Submissions) || notices[r.ID] != len(results) {
			t.Fatalf("round %d: %d submissions, %d results, %d notices", no, len(r.Snapshot.Submissions), len(results), notices[r.ID])
		}
		for _, res := range results {
			if res.Status != model.ResultResolved && res.Status != model.ResultRejected && res.Status != model.ResultAmbiguous {
				t.Fatalf("round %d: result without status %+v", no, res)
			}
		}
		events, _ := w.store.ListEvents(ctx, g.ID, r.ID)
		for _, ev := range events {
			if ev.Seq != lastSeq+1 {
				t.Fatalf("round %d: event seq %d after %d", no, ev.Seq, lastSeq)
			}
			lastSeq = ev.Seq
		}
		if n, same, err := w.svc.Replay(ctx, g, 1, no); err != nil || !same || n != len(events) {
			t.Fatalf("round %d replay = %d, %v, %v", no, n, same, err)
		}
		// Состояние мира: снимок раунда равен свёртке предыдущего опубликованного снимка с его событиями.
		if prevState != nil {
			for i, c := range r.Snapshot.Characters {
				p := prevState.Characters[i]
				if c.HP != p.HP || !reflect.DeepEqual(c.LocationID, p.LocationID) || !sameStrings(c.Conditions, p.Conditions) || !sameStrings(c.Knowledge, p.Knowledge) {
					t.Fatalf("round %d: %s state %+v differs from applied events %+v", no, c.Nickname, c, p)
				}
			}
		}
		state := engine.Clone(*r.Snapshot)
		if err := engine.Apply(&state, events); err != nil {
			t.Fatal(err)
		}
		prevState = &state
	}
	// Строки персонажей в хранилище совпадают со свёрткой последнего раунда.
	for _, p := range prevState.Characters {
		c, _ := w.store.GetCharacter(ctx, g.ID, season.ID, p.UserID)
		if c.HP != p.HP || !reflect.DeepEqual(c.LocationID, p.LocationID) || !sameStrings(c.Knowledge, p.Knowledge) {
			t.Fatalf("stored %s %+v differs from applied %+v", c.Nickname, c, p)
		}
	}
	if final := msgs[len(msgs)-1]; final.Kind != model.OutboxSystem || final.Text[:len("«Мир» — история завершена")] != "«Мир» — история завершена" {
		t.Fatalf("final = %+v", final)
	}
	if got := j.events(); got == "" || ticks < 12 {
		t.Fatalf("journal = %q, ticks = %d", got, ticks)
	}
}

// sameStrings сравнивает срезы, считая nil и пустой срез равными.
func sameStrings(a, b []string) bool {
	return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b))
}
