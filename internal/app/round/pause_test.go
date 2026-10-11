package round

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
)

// TestTick_PauseAfterSkippedRounds (B11): после `pause_after_skipped` пропущенных подряд раундов игра встаёт на паузу
// с сообщением в группу и записью журнала; `/pause` возобновляет и обнуляет счётчик; раунд с кворумом обнуляет
// счётчик; 0 выключает автопаузу.
func TestTick_PauseAfterSkippedRounds(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings()) // кворум 2, автопауза после 3
	ctx := context.Background()
	j := &fakeJournal{}
	w.svc.Journal = j
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	w.tick(t, 10*time.Minute) // раунд 1 пропущен, раунд 2 открыт
	w.tick(t, 10*time.Minute) // раунд 2 пропущен, раунд 3 открыт
	if w.game.Status != model.GameActive || w.game.SkippedStreak != 2 {
		t.Fatalf("after two skips: status %s, streak %d", w.game.Status, w.game.SkippedStreak)
	}
	// Одна заявка из двух кворума не даёт: третий пропуск подряд — пауза, заявка ждёт следующего раунда.
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "жду гонца"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	if w.game.Status != model.GamePaused || w.game.NextRoundAt != nil || w.game.SkippedStreak != 3 {
		t.Fatalf("after third skip = %+v", w.game)
	}
	if _, err := w.store.OpenRound(ctx, w.game.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("round opened during auto pause: %v", err)
	}
	msgs := w.outbox(t)
	note, pause := msgs[len(msgs)-2], msgs[len(msgs)-1]
	if note.Kind != model.OutboxRoundClosed || note.Text != "Раунд 3\nНедостаточное количество участников. Раунд пропущен." {
		t.Fatalf("skipped note = %+v", note)
	}
	if pause.Kind != model.OutboxSystem || pause.Text != "Игра на паузе\n3 раунда подряд без кворума. Возобновить: /pause." {
		t.Fatalf("pause message = %+v", pause)
	}
	if !strings.Contains(j.events(), "skipped paused") {
		t.Fatalf("journal = %q", j.events())
	}
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[1], "поздно"); !errors.Is(err, ErrPaused) {
		t.Fatalf("submit on auto pause: %v", err)
	}
	w.tick(t, time.Hour) // планировщик игру на паузе не трогает
	if last, _ := w.store.LastRound(ctx, w.game.ID); last.RoundNo != 3 {
		t.Fatalf("scheduler touched paused game: %+v", last)
	}
	// Возобновление: раунд 4 с перенесённой заявкой, счётчик обнулён.
	g, paused, err := w.svc.Pause(ctx, w.game, 1)
	if err != nil || paused || g.Status != model.GameActive || g.SkippedStreak != 0 {
		t.Fatalf("resume = %+v, %v, %v", g, paused, err)
	}
	w.reload(t)
	open, err := w.store.OpenRound(ctx, w.game.ID)
	if err != nil || open.RoundNo != 4 {
		t.Fatalf("round after resume = %+v, %v", open, err)
	}
	if last := w.outbox(t)[len(w.outbox(t))-1]; !strings.Contains(last.Text, "Раунд 4 открыт") || !strings.Contains(last.Text, "Перенесено заявок из пропущенного раунда: 1") {
		t.Fatalf("resume text = %q", last.Text)
	}
	w.tick(t, 10*time.Minute) // раунд 4: одна перенесённая заявка — пропуск, счётчик 1, игра идёт
	if w.game.Status != model.GameActive || w.game.SkippedStreak != 1 {
		t.Fatalf("after resume skip = %+v", w.game)
	}
	// Кворум собран: раунд разрешён, счётчик обнулён.
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[1], "иду следом"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	if w.game.Status != model.GameActive || w.game.SkippedStreak != 0 {
		t.Fatalf("after quorum = %+v", w.game)
	}
	if r5, _ := w.store.RoundByNo(ctx, w.game.ID, 5); r5.Status != model.RoundPublished {
		t.Fatalf("round 5 = %+v", r5)
	}
	// Выключено: сколько угодно пропусков подряд без паузы.
	w.game.Settings.PauseAfterSkipped = 0
	if err := w.store.UpdateGame(ctx, w.game); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		w.tick(t, 10*time.Minute)
	}
	if w.game.Status != model.GameActive || w.game.SkippedStreak != 4 {
		t.Fatalf("disabled auto pause = %+v", w.game)
	}
}
