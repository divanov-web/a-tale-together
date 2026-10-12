package narrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"taletogether/internal/model"
)

// questTasks — задания пункта 28: побочный квест Ордену, размещение этапа «Найти след» в лесу (там уже стоит «алтарь»
// с подходом, зажигающим его), пересмотр цели Гильдии после закрытого квеста.
func questTaskSet() Tasks {
	altar := model.LocationObject{Key: "altar", Title: "алтарь", State: "dark", Approaches: []model.Approach{{Action: model.ActionInteract, Title: "зажечь", Difficulty: 0, OnSuccess: json.RawMessage(`{"state":"lit"}`)}}}
	stage := model.QuestStage{ID: 5, QuestID: 9, StageNo: 1, Key: "trail", Title: "Найти след", Summary: "Где видели гонца.", Condition: model.StageCondition{Kind: model.ConditionFact}}
	closed := model.QuestID(3)
	return Tasks{
		Quests:        []QuestTask{{FactionID: 1, Faction: "good", Kind: model.QuestSide, Goal: "Найти гонца.", MinStages: 2, MaxStages: 4, Threshold: 1}},
		Stages:        []StageTask{{QuestID: 9, QuestTitle: "След гонца", StageKey: "trail", StageTitle: "Найти след", StageSummary: "Где видели гонца.", ConditionKind: model.ConditionFact, LocationID: 2, CellName: "Лес", Objects: []string{"altar"}, Existing: []model.LocationObject{altar}, Stage: stage}},
		Goals:         []GoalTask{{FactionID: 2, Faction: "evil", Current: "Груз.", Stage: "выжидание", Closed: "Тёмный груз", QuestID: &closed, Fallback: "Перехватить."}},
		StoryTemplate: &model.QuestTemplate{Title: "Шаблон", Stages: []model.StageTemplate{{Key: "s", Title: "Шаг", Distance: 1, Condition: model.ConditionVisited}}},
	}
}

const questResponse = `{"chronicle":"Лес шепчет.","scenes":[{"scene_id":1,"text":"Вор входит под ели.","event_seqs":[]}],
 "world":{"cells":[],"neighbors":[],
  "quests":[{"faction":"GOOD","title":"Пропавшая коза","summary":"Коза мельника ушла в лес.","stages":[
     {"key":"Goat Trail","title":"Найти следы козы","summary":"Понять, куда ушла коза.","distance":0,"condition":"fact","reward":"Коза у мельницы."},
     {"key":"goat","title":"Вернуть козу","summary":"Коза дома.","distance":2,"condition":"wish","reward":""},
     {"key":"goat trail","title":"Дубль","summary":"","distance":1,"condition":"visited","reward":""},
     {"key":"a","title":"Четвёртый","summary":"","distance":1,"condition":"visited","reward":""},
     {"key":"b","title":"Пятый лишний","summary":"","distance":1,"condition":"visited","reward":""}]},
   {"faction":"evil","title":"Чужой","summary":"Не из задания.","stages":[]}],
  "stages":[{"quest_id":9,"stage_key":"trail","objects":[
     {"key":"altar","title":"второй алтарь","description":"","state":"","hidden":false,"approaches":[]},
     {"key":"footprints","title":"следы у тропы","description":"","state":"","hidden":false,"approaches":[
       {"action":"explore","title":"разобрать следы","attribute":"wits","skill":"awareness","difficulty":5,"on_success":{"state":null,"reveal":null,"fact":"Гонца увели на север."},"on_fail":null}]}],
     "condition":{"kind":"fact","object":null,"state":null,"fact":"Гонца увели на север."}}],
  "goals":[{"faction":"evil","text":"Груз потерян; теперь Гильдии нужен новый путь на юг.","stage":"после провала"}]}}`

// TestNarrate_QuestPackage: вход содержит активные квесты и задания с лимитами; квест принят по заданию фракции
// (регистр кода не важен), этапы сверх максимума отброшены, ключи нормализованы и уникальны, расстояние и условие
// вне каталога заменены; чужой квест отброшен; размещение этапа: объект с занятым ключом отброшен, условие `fact`
// принято, потому что его даёт подход нового объекта; цель принята.
func TestNarrate_QuestPackage(t *testing.T) {
	t.Parallel()
	st := gridState()
	f := &fakeLLM{responses: []string{questResponse}}
	svc := newService(f)
	quests := []QuestInfo{{Faction: "good", Kind: model.QuestStory, Title: "След гонца", Summary: "Гонец пропал.", Goal: "Найти гонца.", Next: &QuestStageInfo{Title: "Найти след", Summary: "Где видели гонца."}}}
	res, err := svc.Narrate(context.Background(), Params{Title: "Мир", Mode: model.ModeBalanced, RoundNo: 2, State: &st, Def: def(), Tasks: questTaskSet(), Quests: quests})
	if err != nil {
		t.Fatal(err)
	}
	in := f.requests[0].Input
	for _, want := range []string{`"quests":[{"faction":"good","kind":"story","title":"След гонца"`, `"next":{"title":"Найти след"`, `"closed":[]`, `"generate":{"cells":[],"neighbors":[]`, `"quests":{"quests":[{"faction":"good","kind":"side","goal":"Найти гонца.","min_stages":2,"max_stages":4}]`, `"stages":[{"quest_id":9,"quest_title":"След гонца","stage_key":"trail"`, `"objects":["altar"]`, `"goals":[{"faction":"evil","current":"Груз.","current_stage":"выжидание","closed_quest":"Тёмный груз"}]`, `"quest_limits":{"story_stages":"3–6","side_stages":"2–4","max_distance":3,"condition_kinds":["object_state","fact","visited"]`} {
		if !strings.Contains(in, want) {
			t.Fatalf("input without %s: %s", want, in)
		}
	}
	w := res.World
	if len(w.Quests) != 1 || w.Quests[0].FactionID != 1 || w.Quests[0].Kind != model.QuestSide || w.Quests[0].Title != "Пропавшая коза" || w.Quests[0].Threshold != 1 || w.Quests[0].Template {
		t.Fatalf("quests = %+v", w.Quests)
	}
	stages := w.Quests[0].Stages
	if len(stages) != 4 || stages[0].Key != "goat_trail" || stages[0].Distance != 1 || stages[0].Condition.Kind != model.ConditionFact || stages[0].Reward.Fact != "Коза у мельницы." ||
		stages[1].Condition.Kind != model.ConditionVisited || stages[2].Key != "goat_trail_2" || stages[3].Key != "a" {
		t.Fatalf("stages = %+v", stages)
	}
	if len(w.Stages) != 1 || w.Stages[0].QuestID != 9 || w.Stages[0].LocationID != 2 || w.Stages[0].Stage.Key != "trail" || w.Stages[0].Template {
		t.Fatalf("placements = %+v", w.Stages)
	}
	if objs := w.Stages[0].Objects; len(objs) != 1 || objs[0].Key != "footprints" || len(objs[0].Approaches) != 1 {
		t.Fatalf("stage objects = %+v", objs)
	}
	if c := w.Stages[0].Condition; c.Kind != model.ConditionFact || c.Fact != "Гонца увели на север." {
		t.Fatalf("condition = %+v", c)
	}
	if len(w.Goals) != 1 || w.Goals[0].FactionID != 2 || w.Goals[0].QuestID == nil || *w.Goals[0].QuestID != 3 || w.Goals[0].Stage != "после провала" || w.Goals[0].Template {
		t.Fatalf("goals = %+v", w.Goals)
	}
	joined := strings.Join(res.Issues, "\n")
	for _, want := range []string{"этапов больше 4", "расстояние 0 вне 1–3", `условие "wish" не из каталога`, "квест фракции «evil» не из задания", "объект «altar» уже есть в клетке"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("issues without %q: %s", want, joined)
		}
	}
	if !strings.Contains(string(Schema), `"required": ["cells", "neighbors", "quests", "stages", "goals"]`) {
		t.Fatal("schema without quest blocks")
	}
}

// TestValidateCondition: условие принимается только достижимым: `object_state` по подходу, ставящему состояние;
// недостижимое заменяется первым достижимым, без подходов — `visited`; пропущенное размещение — шаблон «след».
func TestValidateCondition(t *testing.T) {
	t.Parallel()
	tasks := questTaskSet()
	task := tasks.Stages[0]
	lit := rawCondition{Kind: "object_state", Object: strPtr("altar"), State: strPtr("lit")}
	if c, issues := validateCondition(lit, task.Existing, task); c.Kind != model.ConditionObjectState || c.Object != "altar" || c.State != "lit" || len(issues) != 0 {
		t.Fatalf("lit = %+v, %v", c, issues)
	}
	broken := rawCondition{Kind: "object_state", Object: strPtr("altar"), State: strPtr("broken")}
	if c, issues := validateCondition(broken, task.Existing, task); c.Kind != model.ConditionObjectState || c.State != "lit" || len(issues) != 2 {
		t.Fatalf("broken = %+v, %v", c, issues)
	}
	wrongFact := rawCondition{Kind: "fact", Fact: strPtr("Нет такого.")}
	if c, _ := validateCondition(wrongFact, nil, task); c.Kind != model.ConditionVisited {
		t.Fatalf("no approaches = %+v", c)
	}
	out, issues := validateStages(nil, tasks.Stages, def())
	if len(out) != 1 || !out[0].Template || out[0].Objects[0].Key != "trace_trail" || out[0].Condition.Kind != model.ConditionFact || len(issues) != 1 {
		t.Fatalf("template placement = %+v, %v", out, issues)
	}
	// Шаблон без модели: обязательный сюжетный — из шаблона жанра, побочный — ждёт; цель — шаблон.
	tasks.Quests = append(tasks.Quests, QuestTask{FactionID: 3, Faction: "x", Kind: model.QuestStory, Mandatory: true})
	w := TemplateWorld(tasks)
	if len(w.Quests) != 1 || !w.Quests[0].Template || w.Quests[0].Title != "Шаблон" || w.Quests[0].FactionID != 3 || len(w.Stages) != 1 || len(w.Goals) != 1 || !w.Goals[0].Template || w.Goals[0].Text != "Перехватить." || w.Templated() != 3 {
		t.Fatalf("template world = %+v", w)
	}
}

func strPtr(s string) *string { return &s }

// TestOpening_Quests: завязка отдаёт цель и сюжетный квест каждой фракции; квест с одним этапом принят с замечанием,
// пропущенная фракция получает шаблонный квест и цель.
func TestOpening_Quests(t *testing.T) {
	t.Parallel()
	p := openingParams()
	p.Quests = []QuestTask{{FactionID: 1, Faction: "good", Kind: model.QuestStory, MinStages: 3, MaxStages: 6, Mandatory: true}, {FactionID: 2, Faction: "evil", Kind: model.QuestStory, MinStages: 3, MaxStages: 6, Mandatory: true}}
	p.Goals = []GoalTask{{FactionID: 1, Faction: "good", Fallback: "Хранить."}, {FactionID: 2, Faction: "evil", Fallback: "Свобода."}}
	p.Template = &model.QuestTemplate{Title: "Шаблон", Stages: []model.StageTemplate{{Key: "s", Title: "Шаг", Distance: 1, Condition: model.ConditionVisited}}}
	resp := `{"text":"` + goodOpening + `","factions":[{"code":"good","goal":{"text":"Узнать, откуда дым.","stage":"тревога"},"quest":{"faction":"good","title":"Дым","summary":"Кто жжёт?","stages":[{"key":"src","title":"Источник","summary":"Откуда дым.","distance":1,"condition":"fact","reward":"Лагерь."}]}}],` + goodStart + `}`
	var got model.LLMRequest
	svc := New(Func(func(_ context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		got = req
		return model.LLMResponse{JSON: []byte(resp)}, nil
	}), slog.Default())
	res, err := svc.Opening(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Input, `"quests":{"quests":[{"faction":"good","kind":"story"`) || !strings.Contains(got.Input, `"goals":[{"faction":"good","current":""`) {
		t.Fatalf("input = %s", got.Input)
	}
	if len(res.Quests) != 2 || res.Quests[0].Title != "Дым" || len(res.Quests[0].Stages) != 1 || res.Quests[0].Template || !res.Quests[1].Template || res.Quests[1].FactionID != 2 {
		t.Fatalf("quests = %+v", res.Quests)
	}
	if len(res.Goals) != 2 || res.Goals[0].Text != "Узнать, откуда дым." || res.Goals[0].Template || !res.Goals[1].Template || res.Goals[1].Text != "Свобода." {
		t.Fatalf("goals = %+v", res.Goals)
	}
	joined := strings.Join(res.Issues, "\n")
	for _, want := range []string{"этапов 1, меньше 3", "квест для фракции «evil» не предложен", "цель фракции «evil» не сформулирована"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("issues without %q: %s", want, joined)
		}
	}
	if !strings.Contains(string(OpeningSchema), `"required": ["text", "factions", "start", "neighbors"]`) {
		t.Fatal("opening schema without factions")
	}
}
