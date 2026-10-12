package round

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
)

func (w *world) quests(t *testing.T) []model.Quest {
	t.Helper()
	q, err := w.store.ListQuests(context.Background(), w.game.ID)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func (w *world) factions(t *testing.T) []model.Faction {
	t.Helper()
	f, err := w.store.ListFactions(context.Background(), w.game.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestBegin_QuestStart (S17): `/begin` в вызове завязки даёт каждой фракции цель (версия 1) и сюжетный квест с этапами
// без клеток; без модели — цель из шаблона жанра и шаблонный квест «След гонца»; `/reset` всё убирает.
func TestBegin_QuestStart(t *testing.T) {
	t.Parallel()
	w := startWorld(t)
	ctx := context.Background()
	start := w.locations(t)[0]
	w.svc.LLM = &worldLLM{opening: fmt.Sprintf(startPackageJSON, start.ID)}
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	factions := w.factions(t)
	goals, _ := w.store.ListFactionGoals(ctx, w.game.ID)
	if len(goals) != 2 || goals[0].FactionID != factions[0].ID || goals[0].Version != 1 || goals[0].Text != "Узнать, откуда дым, и защитить город." || goals[0].Stage != "тревога" || goals[1].Text != "Обернуть тревогу города себе на пользу." {
		t.Fatalf("goals = %+v", goals)
	}
	quests := w.quests(t)
	if len(quests) != 2 || quests[0].FactionID != factions[0].ID || quests[0].Kind != model.QuestStory || quests[0].Title != "Дым с севера" || quests[0].CreatedRoundID != nil || len(quests[0].Stages) != 3 || quests[1].Title != "Тёмный груз" {
		t.Fatalf("quests = %+v", quests)
	}
	if st := quests[0].Stages; st[0].Key != "smoke_source" || st[0].Status != model.StagePending || st[0].LocationID != nil || st[0].Condition.Kind != model.ConditionFact || st[0].Reward.Fact != "Дым идёт от лагеря на холме." || st[1].Distance != 2 || st[2].Condition.Kind != model.ConditionObjectState {
		t.Fatalf("stages = %+v", st)
	}
	if _, err := w.svc.Reset(ctx, w.reload(t), 1); err != nil {
		t.Fatal(err)
	}
	if q := w.quests(t); len(q) != 0 {
		t.Fatalf("quests after reset = %+v", q)
	}
	if g, _ := w.store.ListFactionGoals(ctx, w.game.ID); len(g) != 0 {
		t.Fatalf("goals after reset = %+v", g)
	}

	down := startWorld(t)
	down.svc.LLM = &worldLLM{}
	if _, err := down.svc.Begin(ctx, down.game, 1); err != nil {
		t.Fatal(err)
	}
	goals, _ = down.store.ListFactionGoals(ctx, down.game.ID)
	quests = down.quests(t)
	if len(goals) != 2 || goals[0].Text != "Найти пропавшего гонца и вернуть печать Ордена." || len(quests) != 2 || quests[0].Title != "След гонца" || len(quests[0].Stages) != 3 || quests[1].FactionID != goals[1].FactionID {
		t.Fatalf("template start: goals = %+v, quests = %+v", goals, quests)
	}
}

// TestScenario_S17_Lifecycle: этап проявляется в клетке по расстоянию, закрывается по условию (`fact` — осмотр
// зацепки, `visited` — в раунде входа, `object_state` — сдвинутый камень), награда уходит в знания фракции, квест
// закрывается, счётчик обнуляется, цель пересматривается новой версией и обязательный сюжетный выдаётся заново в том
// же раунде; побочный квест приходит после одной открытой клетки.
func TestScenario_S17_Lifecycle(t *testing.T) {
	t.Parallel()
	w := startWorld(t)
	ctx := context.Background()
	start := w.locations(t)[0]
	llm := &worldLLM{opening: fmt.Sprintf(startPackageJSON, start.ID)}
	w.svc.LLM = llm
	for _, c := range w.chars {
		_ = w.store.MarkStarted(ctx, c.UserID) // личные уведомления приходят только открывшим бота
	}
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	order := w.factions(t)[0]
	alice := w.chars[0]
	act := func(text string) {
		t.Helper()
		w.reload(t)
		for _, c := range w.chars {
			if _, _, err := w.svc.Submit(ctx, w.game, c, text); err != nil {
				t.Fatal(err)
			}
		}
		w.tick(t, 10*time.Minute)
	}
	last := func() model.Round {
		t.Helper()
		info, err := w.svc.Info(ctx, w.game)
		if err != nil || info.Last == nil {
			t.Fatalf("info = %+v, %v", info, err)
		}
		return *info.Last
	}
	user, err := w.store.GetUser(ctx, alice.UserID)
	if err != nil {
		t.Fatal(err)
	}
	notice := func(r model.Round) string {
		for _, m := range w.outbox(t) {
			if m.Kind == model.OutboxPrivate && m.RoundID != nil && *m.RoundID == r.ID && m.TelegramChatID == user.PrivateChat() {
				return m.Text
			}
		}
		return ""
	}

	// Раунд 1: все на север в (0,1). Первый этап (расстояние 1) проявляется зацепкой; одна открытая клетка при одном
	// активном квесте даёт побочный квест; счётчик 1 − 1 = 0.
	act("идти на север")
	r := last()
	story := w.quests(t)[0]
	if st := story.Stages[0]; st.Status != model.StageBound || st.LocationID == nil || *st.LocationID != w.at(t, 0, 1).ID || st.Condition.Kind != model.ConditionFact || st.Condition.Fact != "Подсказка: Найти источник дыма" {
		t.Fatalf("stage 1 after round 1 = %+v", st)
	}
	if quests := w.quests(t); len(quests) != 3 || quests[2].Kind != model.QuestSide || quests[2].FactionID != order.ID || quests[2].CreatedRoundID == nil || *quests[2].CreatedRoundID != r.ID {
		t.Fatalf("quests after round 1 = %+v", quests)
	}
	if f := w.factions(t)[0]; f.ExploredSinceReset != 0 {
		t.Fatalf("explored after issue = %d", f.ExploredSinceReset)
	}
	if m := r.Metrics; m.QuestsIssued != 1 || m.StagesPlaced != 1 || m.StagesDone != 0 {
		t.Fatalf("metrics round 1 = %+v", m)
	}
	if text := notice(r); !strings.Contains(text, "• новое дело фракции: «Дело side» — Описание дела.") || !strings.Contains(text, "• «Дым с севера»: этап «Найти источник дыма» — здесь, в «Опушка (подробно)»") || !strings.Contains(text, "• изучить зацепку (без броска)") {
		t.Fatalf("notice round 1 = %q", text)
	}
	// Рассказчик получил активные квесты с ближайшим этапом и подсказкой.
	if in := llm.inputs[len(llm.inputs)-1]; !strings.Contains(in, `"quests":[{"faction":"good","kind":"story","title":"Дым с севера"`) || !strings.Contains(in, `"next":{"title":"Найти источник дыма","summary":"Понять, откуда идёт дым.","placed":false}`) {
		t.Fatalf("narrator input round 1 = %s", in)
	}

	// Раунд 2: осмотр зацепки даёт факт → этап закрыт до вызова рассказчика, награда в знаниях всех персонажей Ордена.
	act("изучить зацепку")
	r = last()
	story = w.quests(t)[0]
	if story.Stages[0].Status != model.StageDone || story.Stages[0].DoneRoundID == nil || *story.Stages[0].DoneRoundID != r.ID || story.Status != model.QuestActive {
		t.Fatalf("stage 1 after round 2 = %+v", story.Stages[0])
	}
	if m := r.Metrics; m.StagesDone != 1 {
		t.Fatalf("metrics round 2 = %+v", m)
	}
	if text := notice(r); !strings.Contains(text, "• «Дым с севера»: этап «Найти источник дыма» выполнен — Дым идёт от лагеря на холме.") {
		t.Fatalf("notice round 2 = %q", text)
	}
	chars, _ := w.store.ListCharacters(ctx, w.game.ID, w.chars[0].SeasonID)
	for _, c := range chars {
		if !containsStr(c.Knowledge, "Дым идёт от лагеря на холме.") {
			t.Fatalf("reward not in knowledge of %s: %v", c.Nickname, c.Knowledge)
		}
	}
	if in := llm.inputs[len(llm.inputs)-1]; !strings.Contains(in, `"closed":["Найти источник дыма"]`) || !strings.Contains(in, `"next":{"title":"Выйти к лагерю"`) {
		t.Fatalf("narrator input round 2 = %s", in)
	}

	// Раунд 3: шаг на север в (0,2) — расстояние 1 от клетки первого этапа, второму нужно 2: этап ждёт.
	act("идти на север")
	if st := w.quests(t)[0].Stages[1]; st.Status != model.StagePending {
		t.Fatalf("stage 2 bound too early: %+v", st)
	}
	// Раунд 4: (0,3) — расстояние 2: этап `visited` проявляется и закрывается в том же раунде.
	act("идти на север")
	r = last()
	if st := w.quests(t)[0].Stages[1]; st.Status != model.StageDone || st.LocationID == nil || *st.LocationID != w.at(t, 0, 3).ID || st.Condition.Kind != model.ConditionVisited {
		t.Fatalf("stage 2 after round 4 = %+v", st)
	}
	if m := r.Metrics; m.StagesPlaced != 1 || m.StagesDone != 1 {
		t.Fatalf("metrics round 4 = %+v", m)
	}
	// Раунд 5: (0,4) — третий этап (расстояние 1, состояние объекта): камень на месте. Раунд 6: сдвинуть → квест
	// закрыт, счётчик обнулён (три клетки с выдачи побочного; закрытие этапа счётчик не трогает).
	act("идти на север")
	if st := w.quests(t)[0].Stages[2]; st.Status != model.StageBound || st.Condition.Kind != model.ConditionObjectState || st.Condition.Object != "clue_chief" || st.Condition.State != "moved" {
		t.Fatalf("stage 3 after round 5 = %+v", st)
	}
	if f := w.factions(t)[0]; f.ExploredSinceReset != 3 {
		t.Fatalf("explored before finish = %d", f.ExploredSinceReset)
	}
	act("сдвинуть камень")
	r = last()
	story = w.quests(t)[0]
	if story.Status != model.QuestDone || story.DoneRoundID == nil || *story.DoneRoundID != r.ID || story.Stages[2].Status != model.StageDone {
		t.Fatalf("story after round 6 = %+v", story)
	}
	if f := w.factions(t)[0]; f.ExploredSinceReset != 0 {
		t.Fatalf("explored after finish = %d", f.ExploredSinceReset)
	}
	// В том же раунде (планирование идёт после проверки этапов): цель Ордена пересмотрена новой версией со ссылкой на
	// закрытый квест, сюжетных не осталось → новый сюжетный выдан сразу, вне счётчика.
	if text := notice(r); !strings.Contains(text, "• «Дым с севера» завершён") || !strings.Contains(text, "• цель фракции теперь: Цель фракции good после «Дым с севера».") || !strings.Contains(text, "• новое дело фракции: «Дело story»") {
		t.Fatalf("notice round 6 = %q", text)
	}
	events, _ := w.store.ListEvents(ctx, w.game.ID, r.ID)
	var done, finished int
	for _, ev := range events {
		switch ev.Type {
		case model.EventQuestStageDone:
			done++
			if ev.Visibility != model.VisibilityPrivate || len(ev.VisibleTo) != 3 {
				t.Fatalf("stage done visibility = %+v", ev)
			}
		case model.EventQuestDone:
			finished++
		}
	}
	if done != 1 || finished != 1 {
		t.Fatalf("quest events: done %d, finished %d", done, finished)
	}
	if n, same, err := w.svc.Replay(ctx, w.game, 1, r.RoundNo); err != nil || !same || n == 0 {
		t.Fatalf("replay = %d, %v, %v", n, same, err)
	}
	goals, _ := w.store.ListFactionGoals(ctx, w.game.ID)
	if len(goals) != 2 || goals[0].Version != 2 || goals[0].QuestID == nil || *goals[0].QuestID != story.ID || !strings.Contains(goals[0].Text, "после «Дым с севера»") || goals[1].Version != 1 {
		t.Fatalf("goals after round 6 = %+v", goals)
	}
	quests := w.quests(t)
	if len(quests) != 4 || quests[3].Kind != model.QuestStory || quests[3].FactionID != order.ID || quests[3].Status != model.QuestActive || quests[3].Stages[0].Status != model.StagePending {
		t.Fatalf("quests after round 6 = %+v", quests)
	}
	// Раунд 7: первый этап нового сюжетного (расстояние 1 от площади) проявляется там, где стоит группа.
	act("ждать")
	r = last()
	if st := w.quests(t)[3].Stages[0]; st.Status != model.StageBound || st.LocationID == nil || *st.LocationID != w.at(t, 0, 4).ID {
		t.Fatalf("new story stage after round 7 = %+v", st)
	}
	if text := notice(r); !strings.Contains(text, "• «Дело story»: этап «Шаг 1» — здесь, в «Клетка 0,4 (подробно)»") {
		t.Fatalf("notice round 7 = %q", text)
	}
	// Модель выдала побочный и новый сюжетный; разместила три этапа первого сюжетного, этап побочного (раунд 3,
	// расстояние 1 от площади) и этап нового сюжетного.
	if llm.quests != 2 || llm.placed != 5 {
		t.Fatalf("model quests = %d, placed = %d", llm.quests, llm.placed)
	}
}

func containsStr(list []string, s string) bool {
	for _, it := range list {
		if it == s {
			return true
		}
	}
	return false
}
