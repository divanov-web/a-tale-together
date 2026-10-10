package round

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
)

// crashLLM — провайдер, который паникует заданное число вызовов (сбой конвейера внутри транзакции), затем работает как fakeLLM.
type crashLLM struct {
	fakeLLM
	crashes int
}

func (c *crashLLM) Complete(ctx context.Context, call applm.Call, req model.LLMRequest) (model.LLMResponse, error) {
	if c.crashes > 0 {
		c.crashes--
		panic("interpreter exploded")
	}
	return c.fakeLLM.Complete(ctx, call, req)
}

func moveJSON(sub model.Submission) string {
	return fmt.Sprintf(`{"intents":[{"submission_id":%d,"character_id":%d,"action_type":"move","target":"Зал Серой башни","approach_idx":null,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null}]}`, sub.ID, sub.CharacterID)
}

// TestTick_CrashRollsBackAndRetries: паника в конвейере откатывает транзакцию закрытия, раунд остаётся открытым с
// попыткой 1; следующий тик разрешает его с первого раза, без дублей событий и сообщений; метрики помнят номер попытки.
func TestTick_CrashRollsBackAndRetries(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	llm := &crashLLM{crashes: 1}
	w.svc.LLM = llm
	j := &fakeJournal{}
	w.svc.Journal = j
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	r1, sub, err := w.svc.Submit(ctx, w.game, w.chars[0], "иду к башне")
	if err != nil {
		t.Fatal(err)
	}
	llm.json = moveJSON(sub)
	w.clock.t = w.clock.t.Add(10 * time.Minute)
	if err := w.svc.Tick(ctx, w.clock.Now()); err == nil || !strings.Contains(err.Error(), "interpreter exploded") {
		t.Fatalf("tick with panic = %v", err)
	}
	open, err := w.store.OpenRound(ctx, w.game.ID)
	if err != nil || open.ID != r1.ID || open.Attempts != 1 || open.Status != model.RoundOpen {
		t.Fatalf("round after crash = %+v, %v", open, err)
	}
	if events, _ := w.store.ListEvents(ctx, w.game.ID, r1.ID); len(events) != 0 {
		t.Fatalf("events committed despite rollback: %+v", events)
	}
	if got := j.events(); got != "opened submission frozen attempt_failed" {
		t.Fatalf("journal = %q", got)
	}
	w.tick(t, time.Second)
	done, _ := w.store.RoundByNo(ctx, w.game.ID, 1)
	if done.Status != model.RoundPublished || done.Attempts != 1 || done.Metrics == nil || done.Metrics.Attempt != 2 || done.Metrics.Submissions != 1 || done.Metrics.Resolved != 1 || done.Metrics.LLMCalls < 1 {
		t.Fatalf("round after retry = %+v metrics %+v", done, done.Metrics)
	}
	events, _ := w.store.ListEvents(ctx, w.game.ID, r1.ID)
	if len(events) == 0 || events[0].Seq != 1 {
		t.Fatalf("events = %+v", events)
	}
	chronicles := 0
	for _, m := range w.outbox(t) {
		if m.Kind == model.OutboxChronicle {
			chronicles++
		}
	}
	if chronicles != 1 {
		t.Fatalf("chronicles = %d", chronicles)
	}
	if last := j.recs[len(j.recs)-1]; last.Event != "opened" || last.RoundNo != 2 || j.recs[len(j.recs)-2].Event != "metrics" || j.recs[len(j.recs)-2].Attempt != 2 {
		t.Fatalf("journal tail = %+v", j.recs[len(j.recs)-3:])
	}
}

// TestTick_FailsAfterMaxAttempts: три сбоя подряд → раунд `failed` с причиной, сообщение в группу, заявка переносится
// в следующий раунд, который разрешается нормально.
func TestTick_FailsAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	llm := &crashLLM{crashes: 3}
	w.svc.LLM = llm
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	r1, sub, err := w.svc.Submit(ctx, w.game, w.chars[0], "иду к башне")
	if err != nil {
		t.Fatal(err)
	}
	llm.json = moveJSON(sub)
	w.clock.t = w.clock.t.Add(10 * time.Minute)
	for i := 1; i <= 3; i++ {
		if err := w.svc.Tick(ctx, w.clock.Now()); err == nil {
			t.Fatalf("tick %d succeeded unexpectedly", i)
		}
		w.clock.t = w.clock.t.Add(10 * time.Second)
	}
	failed, _ := w.store.RoundByNo(ctx, w.game.ID, 1)
	if failed.Status != model.RoundFailed || failed.Attempts != 3 || !strings.HasPrefix(failed.SkipReason, "сбой разрешения: ") || failed.ClosedAt == nil {
		t.Fatalf("failed round = %+v", failed)
	}
	open, err := w.store.OpenRound(ctx, w.game.ID)
	if err != nil || open.RoundNo != 2 {
		t.Fatalf("next round = %+v, %v", open, err)
	}
	if _, err := w.store.GetSubmission(ctx, w.game.ID, r1.ID, w.chars[0].ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("submission stayed in failed round: %v", err)
	}
	carried, err := w.store.GetSubmission(ctx, w.game.ID, open.ID, w.chars[0].ID)
	if err != nil || carried.ID != sub.ID {
		t.Fatalf("carried = %+v, %v", carried, err)
	}
	msgs := w.outbox(t)
	if len(msgs) != 4 || msgs[2].Kind != model.OutboxRoundClosed || msgs[2].RoundID == nil || *msgs[2].RoundID != r1.ID ||
		msgs[2].Text != "«Мир» — раунд 1 не удалось разрешить: сбой на стороне бота. Заявки перенесены в следующий раунд." {
		t.Fatalf("outbox = %+v", msgs)
	}
	if !strings.Contains(msgs[3].Text, "Перенесено заявок из пропущенного раунда: 1") {
		t.Fatalf("open text = %q", msgs[3].Text)
	}
	info, _ := w.svc.Info(ctx, w.reload(t))
	if info.Last == nil || info.Last.Status != model.RoundFailed || info.Open == nil || info.Submissions != 1 {
		t.Fatalf("info = %+v", info)
	}
	w.tick(t, 11*time.Minute) // дедлайн округлён вверх до минуты
	done, _ := w.store.RoundByNo(ctx, w.game.ID, 2)
	if done.Status != model.RoundPublished || done.Attempts != 0 {
		t.Fatalf("round 2 = %+v", done)
	}
}

// TestFinish: /finish с открытым раундом разрешает его при одной заявке без кворума, ставит финальную хронику,
// игра `finished`, планировщик её не берёт, заявки и пауза отвечают «игра завершена».
func TestFinish(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	w.game.Settings.MinRoundAnswers = 2
	ctx := context.Background()
	if err := w.store.UpdateGame(ctx, w.game); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Finish(ctx, w.game, 1); !errors.Is(err, ErrNotActive) {
		t.Fatalf("finish draft: %v", err)
	}
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "идти к башне"); err != nil {
		t.Fatal(err)
	}
	w.clock.t = w.clock.t.Add(3 * time.Minute)
	g, err := w.svc.Finish(ctx, w.game, 1)
	if err != nil || g.Status != model.GameFinished || g.NextRoundAt != nil {
		t.Fatalf("finish = %+v, %v", g, err)
	}
	r1, _ := w.store.RoundByNo(ctx, g.ID, 1)
	if r1.Status != model.RoundPublished || r1.ClosedAt == nil || !r1.ClosedAt.Equal(w.clock.t) {
		t.Fatalf("round at finish = %+v", r1)
	}
	msgs := w.outbox(t)
	final := msgs[len(msgs)-1]
	if final.Kind != model.OutboxSystem || !strings.HasPrefix(final.Text, "«Мир» — история завершена\nИгру завершил администратор. Раундов сыграно: 1.\nПерсонажи:\n• Элис — ") {
		t.Fatalf("final text = %q", final.Text)
	}
	if !strings.Contains(final.Text, "Зал Серой башни") || !strings.Contains(final.Text, "HP 6/6") {
		t.Fatalf("final text = %q", final.Text)
	}
	for _, m := range msgs {
		if m.Kind == model.OutboxRoundOpen && m.RoundID != nil && *m.RoundID != r1.ID {
			t.Fatalf("round opened after finish: %+v", m)
		}
	}
	w.clock.t = w.clock.t.Add(time.Hour)
	if err := w.svc.Tick(ctx, w.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if last, _ := w.store.LastRound(ctx, g.ID); last.RoundNo != 1 {
		t.Fatalf("scheduler touched finished game: %+v", last)
	}
	if _, _, err := w.svc.Submit(ctx, g, w.chars[1], "ждать"); !errors.Is(err, ErrFinished) {
		t.Fatalf("submit after finish: %v", err)
	}
	if _, _, err := w.svc.Pause(ctx, g, 1); !errors.Is(err, ErrFinished) {
		t.Fatalf("pause after finish: %v", err)
	}
	if _, err := w.svc.Finish(ctx, g, 1); !errors.Is(err, ErrFinished) {
		t.Fatalf("second finish: %v", err)
	}
}

// TestBoundary_MaxRounds: после закрытия раунда с номером max_rounds игра завершается вместо открытия следующего.
func TestBoundary_MaxRounds(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	w.game.Settings.MaxRounds = 2
	ctx := context.Background()
	if err := w.store.UpdateGame(ctx, w.game); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	for i := 1; i <= 2; i++ {
		if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "ждать"); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		w.tick(t, 10*time.Minute)
	}
	if w.game.Status != model.GameFinished || w.game.NextRoundAt != nil {
		t.Fatalf("game after max rounds = %+v", w.game)
	}
	if last, _ := w.store.LastRound(ctx, w.game.ID); last.RoundNo != 2 || last.Status != model.RoundPublished {
		t.Fatalf("last round = %+v", last)
	}
	msgs := w.outbox(t)
	if final := msgs[len(msgs)-1]; !strings.Contains(final.Text, "Сыграно 2 раундов из 2. Раундов сыграно: 2.") {
		t.Fatalf("final = %q", final.Text)
	}
}

// TestBoundary_EndsAt: следующий раунд не открывается, когда наступила дата окончания.
func TestBoundary_EndsAt(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ends := w.clock.t.Add(25 * time.Minute)
	w.game.Settings.EndsAt = &ends
	ctx := context.Background()
	if err := w.store.UpdateGame(ctx, w.game); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	w.tick(t, 10*time.Minute) // раунд 1 пропущен, раунд 2 открыт (12:10)
	w.tick(t, 10*time.Minute) // раунд 2 пропущен, раунд 3 открыт (12:20)
	if open, err := w.store.OpenRound(ctx, w.game.ID); err != nil || open.RoundNo != 3 {
		t.Fatalf("open = %+v, %v", open, err)
	}
	w.tick(t, 10*time.Minute) // 12:30 ≥ 12:25: игра завершена
	if w.game.Status != model.GameFinished {
		t.Fatalf("game = %+v", w.game)
	}
	msgs := w.outbox(t)
	if final := msgs[len(msgs)-1]; !strings.Contains(final.Text, "Наступила дата окончания 10.10.2026. Раундов сыграно: 3.") {
		t.Fatalf("final = %q", final.Text)
	}
}

// TestReset: сброс возвращает игру в черновик, удаляет ход игры и возвращает персонажей на старт; /begin работает заново.
func TestReset(t *testing.T) {
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
	llm.json = moveJSON(sub)
	w.tick(t, 10*time.Minute)
	moved, _ := w.store.GetCharacter(ctx, w.game.ID, w.chars[0].SeasonID, w.chars[0].UserID)
	if moved.LocationID == nil || *moved.LocationID == *w.chars[0].LocationID {
		t.Fatalf("character did not move: %+v", moved)
	}
	g, err := w.svc.Reset(ctx, w.game, 1)
	if err != nil || g.Status != model.GameDraft || g.NextRoundAt != nil {
		t.Fatalf("reset = %+v, %v", g, err)
	}
	if _, err := w.store.LastRound(ctx, g.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("rounds after reset: %v", err)
	}
	if msgs := w.outbox(t); len(msgs) != 0 {
		t.Fatalf("outbox after reset = %+v", msgs)
	}
	if events, _ := w.store.ListEvents(ctx, g.ID, 0); len(events) != 0 {
		t.Fatalf("events after reset = %+v", events)
	}
	back, _ := w.store.GetCharacter(ctx, g.ID, w.chars[0].SeasonID, w.chars[0].UserID)
	if back.LocationID == nil || *back.LocationID != *w.chars[0].LocationID || back.HP != 6 || back.Status != model.CharacterActive || len(back.Knowledge) != 0 {
		t.Fatalf("character after reset = %+v", back)
	}
	if g, err = w.svc.Begin(ctx, g, 1); err != nil || g.Status != model.GameActive {
		t.Fatalf("begin after reset = %+v, %v", g, err)
	}
	if open, err := w.store.OpenRound(ctx, g.ID); err != nil || open.RoundNo != 1 {
		t.Fatalf("round after reset = %+v, %v", open, err)
	}
}

// TestDetail: карточка раунда по номеру и последнего раунда.
func TestDetail(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	if _, err := w.svc.Detail(ctx, w.game, 0); !errors.Is(err, ErrNoRound) {
		t.Fatalf("detail without rounds: %v", err)
	}
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[1], "ждать"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	d, err := w.svc.Detail(ctx, w.game, 1)
	if err != nil || d.Round.RoundNo != 1 || d.Round.Status != model.RoundPublished || len(d.Submissions) != 1 || len(d.Results) != 1 || d.Events != 1 || d.Names[w.chars[1].ID] != "Боб" || d.Round.Metrics == nil {
		t.Fatalf("detail = %+v, %v", d, err)
	}
	if last, err := w.svc.Detail(ctx, w.game, 0); err != nil || last.Round.RoundNo != 2 || last.Round.Status != model.RoundOpen {
		t.Fatalf("last detail = %+v, %v", last, err)
	}
}
