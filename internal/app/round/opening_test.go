package round

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// TestBegin_Opening: при /begin рассказчик получает один вызов с ролью `opening` без раунда и только с открытыми
// данными; его текст уходит стартовой сценой, хранится у игры и попадает первой строкой выжимки в контекст хроники.
func TestBegin_Opening(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	for _, c := range w.chars {
		if err := w.store.UpdateCharacterBio(ctx, w.game.ID, c.ID, "Любит шумные площади", "Шпион Гильдии"); err != nil {
			t.Fatal(err)
		}
	}
	text := "Над Миром висит туман. Элис, Боб и Кэт стоят на Рыночной площади, и толпа у башни растёт с каждой минутой: кто-то видел свет в её окнах."
	llm := &fakeLLM{opening: fmt.Sprintf(`{"text":%q}`, text)}
	w.svc.LLM = llm
	g, err := w.svc.Begin(ctx, w.game, 1)
	if err != nil {
		t.Fatal(err)
	}
	calls := llm.byRole(model.RoleOpening)
	if len(calls) != 1 || calls[0].Round != nil || calls[0].Game == nil || calls[0].PromptVersion != narrator.OpeningPromptVersion {
		t.Fatalf("opening calls = %+v", calls)
	}
	in := llm.inputs[0]
	if !strings.Contains(in, `"nickname":"Элис"`) || !strings.Contains(in, "Любит шумные площади") || !strings.Contains(in, `"start":true`) || !strings.Contains(in, `"exits":["Зал Серой башни"]`) {
		t.Fatalf("opening input = %s", in)
	}
	if strings.Contains(in, "Шпион") {
		t.Fatalf("secret bio leaked into opening input: %s", in)
	}
	if g.OpeningScene != text {
		t.Fatalf("game opening = %q", g.OpeningScene)
	}
	if msgs := w.outbox(t); msgs[0].Kind != model.OutboxScene || msgs[0].Text != "«Мир» — начало истории\n\n"+text {
		t.Fatalf("scene message = %+v", msgs[0])
	}
	// Хроника первого раунда получает завязку первой строкой `previous`.
	w.reload(t)
	_, sub, err := w.svc.Submit(ctx, w.game, w.chars[0], "иду к башне")
	if err != nil {
		t.Fatal(err)
	}
	llm.json = fmt.Sprintf(`{"intents":[{"submission_id":%d,"character_id":%d,"action_type":"move","target":"Зал Серой башни","approach_idx":null,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null}]}`, sub.ID, sub.CharacterID)
	llm.narr = `{"chronicle":"Туман редеет.","scenes":[]}`
	w.tick(t, 10*time.Minute)
	if narr := llm.byRole(model.RoleNarrator); len(narr) == 0 || !strings.Contains(llm.inputs[len(llm.inputs)-1], `"previous":["Над Миром висит туман.`) {
		t.Fatalf("narrator input without opening: %s", llm.inputs[len(llm.inputs)-1])
	}
}

// TestBegin_OpeningFallback: без ответа рассказчика стартовая сцена берётся из шаблона жанра и тоже хранится у игры.
func TestBegin_OpeningFallback(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	llm := &fakeLLM{}
	w.svc.LLM = llm
	g, err := w.svc.Begin(ctx, w.game, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.byRole(model.RoleOpening)) != 1 || g.OpeningScene != "В Мир наступает осень." {
		t.Fatalf("fallback opening = %q, calls %+v", g.OpeningScene, llm.calls)
	}
	if msgs := w.outbox(t); msgs[0].Text != "«Мир» — начало истории\n\nВ Мир наступает осень." {
		t.Fatalf("scene message = %q", msgs[0].Text)
	}
}
