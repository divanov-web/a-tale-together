package round

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
)

// fakeJournal собирает записи журнала чатов.
type fakeJournal struct{ recs []model.ChatLogRecord }

func (j *fakeJournal) Write(_ context.Context, rec model.ChatLogRecord) { j.recs = append(j.recs, rec) }

func (j *fakeJournal) events() string {
	parts := make([]string, 0, len(j.recs))
	for _, r := range j.recs {
		parts = append(parts, r.Event)
	}
	return strings.Join(parts, " ")
}

func TestJournal_RoundEvents(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	llm := &fakeLLM{}
	w.svc.LLM = llm
	j := &fakeJournal{}
	w.svc.Journal = j
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	_, sub, err := w.svc.Submit(ctx, w.game, w.chars[0], "иду к башне")
	if err != nil {
		t.Fatal(err)
	}
	llm.json = fmt.Sprintf(`{"intents":[{"submission_id":%d,"character_id":%d,"action_type":"move","target":"Зал Серой башни","approach_idx":null,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null}]}`, sub.ID, sub.CharacterID)
	llm.narr = `{"chronicle":"Вечер.","scenes":[{"location_id":2,"party":"Башня","text":"Элис входит в зал.","event_seqs":[1,2]}]}`
	w.tick(t, 10*time.Minute)
	if got := j.events(); got != "opened submission frozen interpreted narrated resolved published opened" {
		t.Fatalf("events = %q", got)
	}
	for _, r := range j.recs {
		if r.Kind != model.ChatLogRound || r.Chat != -100 || r.GameID != w.game.ID || r.RoundID == 0 || r.RoundNo == 0 || r.TS.IsZero() {
			t.Fatalf("record = %+v", r)
		}
	}
	sd := j.recs[1].Data.(map[string]any)
	if sd["character"] != "Элис" || sd["text"] != "иду к башне" {
		t.Fatalf("submission data = %+v", sd)
	}
	fd := j.recs[2].Data.(map[string]any)
	if fd["seed"] != int64(42) || len(fd["submissions"].([]map[string]any)) != 1 || len(fd["characters"].([]map[string]any)) != 3 {
		t.Fatalf("frozen data = %+v", fd)
	}
	if id := j.recs[3].Data.(map[string]any); id["calls"] != 1 || len(id["intents"].([]model.Intent)) != 1 {
		t.Fatalf("interpreted data = %+v", id)
	}
	if nd := j.recs[4].Data.(map[string]any); nd["calls"] == nil || nd["narration"].(model.Narration).Chronicle != "Вечер." {
		t.Fatalf("narrated data = %+v", nd)
	}
	if rd := j.recs[5].Data.(map[string]any); len(rd["results"].([]model.ActionResult)) != 1 || len(rd["events"].([]model.Event)) == 0 {
		t.Fatalf("resolved data = %+v", rd)
	}
	if pd := j.recs[6].Data.(map[string]any); pd["chronicle_parts"] != 1 || pd["notices"] != 0 {
		t.Fatalf("published data = %+v", pd)
	}
	if j.recs[7].RoundNo != 2 {
		t.Fatalf("next round = %+v", j.recs[7])
	}
	// Без заявок раунд пропускается: запись с причиной, затем новый раунд.
	j.recs = nil
	w.tick(t, 10*time.Minute)
	if got := j.events(); got != "skipped opened" || j.recs[0].Data.(map[string]any)["reason"] == "" {
		t.Fatalf("skip events = %q, %+v", got, j.recs[0])
	}
}
