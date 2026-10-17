package round

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
	"taletogether/internal/service/rules"
)

// TestHint_Validation (пункт 35, ADR-0038): просьба принимается при игре `active`, живом персонаже в истории и открытом
// раунде; вторая просьба той же фракции до выдачи — `ErrHintPending`; после дедлайна — `ErrNoOpenRound`.
func TestHint_Validation(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings())
	ctx := context.Background()
	if _, err := w.svc.Hint(ctx, w.game, w.chars[0]); !errors.Is(err, ErrNotActive) {
		t.Fatalf("draft: %v", err)
	}
	w.begin(t)
	dead := w.chars[1]
	dead.Status, dead.DeathRoundsLeft = model.CharacterDead, 2
	if _, err := w.svc.Hint(ctx, w.game, dead); !errors.Is(err, ErrDead) {
		t.Fatalf("dead: %v", err)
	}
	gone := w.chars[1]
	gone.Status = model.CharacterInactive
	if _, err := w.svc.Hint(ctx, w.game, gone); !errors.Is(err, ErrAbsent) {
		t.Fatalf("inactive: %v", err)
	}
	r, err := w.svc.Hint(ctx, w.game, w.chars[0])
	if err != nil || r.RoundNo != 1 {
		t.Fatalf("Hint = %+v, %v", r, err)
	}
	if _, err := w.svc.Hint(ctx, w.game, w.chars[1]); !errors.Is(err, ErrHintPending) {
		t.Fatalf("second request of the faction: %v", err)
	}
	factions, _ := w.store.ListFactions(ctx, w.game.ID)
	if factions[0].HintRound == nil || *factions[0].HintRound != 1 {
		t.Fatalf("faction flag = %+v", factions[0])
	}
	cds, _ := w.store.ListCooldowns(ctx, w.game.ID, model.CooldownFaction, int64(factions[0].ID))
	if len(cds) != 1 || cds[0].Action != model.CooldownHint || cds[0].LastRound != 1 {
		t.Fatalf("cooldown = %+v", cds)
	}
	w.clock.t = w.clock.t.Add(10 * time.Minute)
	if _, err := w.svc.Hint(ctx, w.game, w.chars[1]); !errors.Is(err, ErrNoOpenRound) {
		t.Fatalf("after deadline: %v", err)
	}
}

// TestHint_GivenAndCooldown (S23, ADR-0038): просьба попадает в снимок и во вход рассказчика (`hints` с никнеймами
// фракции), после хроники с `hints_given` флаг снят, событие `hint_given` записано, журнал получил `hint`;
// следующая просьба на перезарядке 5 раундов с текстом «через N раундов (с раунда M)», с раунда M проходит.
func TestHint_GivenAndCooldown(t *testing.T) {
	t.Parallel()
	w := startWorld(t)
	ctx := context.Background()
	j := &fakeJournal{}
	w.svc.Journal = j
	llm := &worldLLM{opening: fmt.Sprintf(startPackageJSON, w.locations(t)[0].ID)}
	w.svc.LLM = llm
	w.begin(t)
	if _, err := w.svc.Hint(ctx, w.game, w.chars[0]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "осматриваюсь"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	r1, _ := w.store.RoundByNo(ctx, w.game.ID, 1)
	if r1.Status != model.RoundPublished || r1.Snapshot == nil || len(r1.Snapshot.Hints) != 1 || r1.Snapshot.Hints[0].Faction != model.FactionGood || r1.Snapshot.Hints[0].Round != 1 {
		t.Fatalf("round 1 = %s, hints %+v", r1.Status, r1.Snapshot)
	}
	narratorInput := llm.inputs[len(llm.inputs)-1]
	if !strings.Contains(narratorInput, `"hints":[{"faction":"good","characters":["Элис","Боб","Кэт"]}]`) {
		t.Fatalf("narrator input without hints: %s", narratorInput)
	}
	factions, _ := w.store.ListFactions(ctx, w.game.ID)
	if factions[0].HintRound != nil {
		t.Fatalf("flag not cleared after chronicle: %+v", factions[0])
	}
	events, _ := w.store.ListEvents(ctx, w.game.ID, r1.ID)
	given := 0
	for _, ev := range events {
		if ev.Type == model.EventHintGiven && ev.Visibility == model.VisibilityPublic && strings.Contains(string(ev.Payload), `"faction":"good"`) {
			given++
		}
	}
	if given != 1 {
		t.Fatalf("hint_given events = %d", given)
	}
	hints := 0
	for _, rec := range j.recs {
		if rec.Event == "hint" {
			hints++
		}
	}
	if hints != 1 {
		t.Fatalf("journal hint records = %d", hints)
	}
	// Раунд 2: перезарядка с раунда 1 на 5 раундов — доступно с раунда 6.
	var cooldown *HintCooldownError
	_, err := w.svc.Hint(ctx, w.game, w.chars[1])
	if !errors.As(err, &cooldown) || !errors.Is(err, ErrHintCooldown) || cooldown.Left != 4 || cooldown.ReadyAt != 6 {
		t.Fatalf("round 2 hint = %v", err)
	}
	if err.Error() != "подсказку уже просили, следующая через 4 раундов (с раунда 6)" {
		t.Fatalf("cooldown text = %q", err.Error())
	}
	for no := 2; no < 6; no++ {
		if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "жду"); err != nil {
			t.Fatalf("round %d submit: %v", no, err)
		}
		w.tick(t, 10*time.Minute)
	}
	if open, _ := w.store.OpenRound(ctx, w.game.ID); open.RoundNo != 6 {
		t.Fatalf("open round = %d; want 6", open.RoundNo)
	}
	if r, err := w.svc.Hint(ctx, w.game, w.chars[1]); err != nil || r.RoundNo != 6 {
		t.Fatalf("round 6 hint = %+v, %v", r, err)
	}
	if rules.CooldownRounds[model.CooldownHint] != 5 {
		t.Fatalf("hint cooldown = %d; want 5", rules.CooldownRounds[model.CooldownHint])
	}
}

// TestHint_CarriedOnFailure (ADR-0038): модель не отметила подсказку в `hints_given` (или хроника шаблонная) — флаг
// остаётся, просьба уходит в снимок следующего раунда; после выдачи перезарядка считается от раунда выдачи.
func TestHint_CarriedOnFailure(t *testing.T) {
	t.Parallel()
	w := startWorld(t)
	ctx := context.Background()
	llm := &worldLLM{opening: fmt.Sprintf(startPackageJSON, w.locations(t)[0].ID), noHints: true}
	w.svc.LLM = llm
	w.begin(t)
	if _, err := w.svc.Hint(ctx, w.game, w.chars[0]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "жду"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	r1, _ := w.store.RoundByNo(ctx, w.game.ID, 1)
	if r1.Status != model.RoundPublished || r1.Metrics == nil || r1.Metrics.NarratorFallback {
		t.Fatalf("round 1 = %+v", r1)
	}
	factions, _ := w.store.ListFactions(ctx, w.game.ID)
	if factions[0].HintRound == nil || *factions[0].HintRound != 1 {
		t.Fatalf("flag after undone hint = %+v", factions[0])
	}
	if events, _ := w.store.ListEvents(ctx, w.game.ID, r1.ID); countType(events, model.EventHintGiven) != 0 {
		t.Fatalf("hint_given without hints_given: %+v", events)
	}
	if _, err := w.svc.Hint(ctx, w.game, w.chars[1]); !errors.Is(err, ErrHintPending) {
		t.Fatalf("pending hint blocks a new request: %v", err)
	}
	// Хроника шаблонная (модель упала): просьба переживает и её.
	w.svc.LLM = nil
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "жду"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	factions, _ = w.store.ListFactions(ctx, w.game.ID)
	if factions[0].HintRound == nil {
		t.Fatalf("flag lost on template chronicle: %+v", factions[0])
	}
	// Раунд 3: модель снова отвечает и отмечает подсказку — флаг снят, перезарядка от раунда 3 (доступно с 8).
	llm.noHints = false
	w.svc.LLM = llm
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "жду"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	r3, _ := w.store.RoundByNo(ctx, w.game.ID, 3)
	if len(r3.Snapshot.Hints) != 1 || r3.Snapshot.Hints[0].Round != 1 {
		t.Fatalf("round 3 snapshot hints = %+v", r3.Snapshot.Hints)
	}
	factions, _ = w.store.ListFactions(ctx, w.game.ID)
	if factions[0].HintRound != nil {
		t.Fatalf("flag after delivery = %+v", factions[0])
	}
	var cooldown *HintCooldownError
	if _, err := w.svc.Hint(ctx, w.game, w.chars[1]); !errors.As(err, &cooldown) || cooldown.ReadyAt != 8 {
		t.Fatalf("cooldown after late delivery = %v; want ready at 8", err)
	}
}

// countType — число событий типа t.
func countType(events []model.Event, t model.EventType) int {
	n := 0
	for _, ev := range events {
		if ev.Type == t {
			n++
		}
	}
	return n
}
