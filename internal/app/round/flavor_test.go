package round

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
)

// TestResolve_FlavorToNarrator (S24, ADR-0039): окраска из намерения итога `resolved` уходит рассказчику в сцену
// присутствия персонажа (`flavors`), хранится в намерении итога и показана игроку строкой «Рассказчику».
func TestResolve_FlavorToNarrator(t *testing.T) {
	t.Parallel()
	w := startWorld(t)
	ctx := context.Background()
	llm := &worldLLM{opening: fmt.Sprintf(startPackageJSON, w.locations(t)[0].ID), echoFlavor: true}
	w.svc.LLM = llm
	if err := w.store.MarkStarted(ctx, w.chars[0].UserID); err != nil { // личка открыта: уведомление о заявке уходит
		t.Fatal(err)
	}
	w.begin(t)
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "оглядываюсь на спутников и прошу напомнить, что мы ищем"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	r1, _ := w.store.RoundByNo(ctx, w.game.ID, 1)
	if r1.Status != model.RoundPublished {
		t.Fatalf("round 1 = %s", r1.Status)
	}
	narratorInput := llm.inputs[len(llm.inputs)-1]
	if !strings.Contains(narratorInput, `"flavors":[{"nickname":"Элис","text":"оглядываюсь на спутников и прошу напомнить, что мы ищем"}]`) {
		t.Fatalf("narrator input without flavors: %s", narratorInput)
	}
	actions, err := w.store.ListResolvedActions(ctx, w.game.ID, r1.ID)
	if err != nil || len(actions) != 1 || actions[0].Status != model.ResultResolved || actions[0].Intent.ActionType != model.ActionWait || !strings.Contains(actions[0].Intent.Flavor, "прошу напомнить") {
		t.Fatalf("resolved actions = %+v, %v", actions, err)
	}
	var notice string
	for _, msg := range w.outbox(t) {
		if msg.Kind == model.OutboxPrivate {
			notice = msg.Text
		}
	}
	if !strings.Contains(notice, "Рассказчику: «оглядываюсь на спутников и прошу напомнить, что мы ищем»") {
		t.Fatalf("notice without flavor line: %q", notice)
	}
}
