package quest

import (
	"strings"
	"testing"

	"taletogether/internal/model"
)

func loc(id model.LocationID) *model.LocationID { return &id }

// questState — снимок S17: площадь (0,0), лес (0,1), холм (0,3); Вор (фракция 1) в лесу, Маг (фракция 2) на холме.
func questState() *model.RoundSnapshot {
	return &model.RoundSnapshot{
		Locations: []model.SnapshotLocation{
			{ID: 1, Name: "Площадь", X: 0, Y: 0, Start: true, Visited: true, Objects: []model.LocationObject{}},
			{ID: 2, Name: "Лес", X: 0, Y: 1, Visited: true, Objects: []model.LocationObject{{Key: "altar", Title: "алтарь", State: "dark"}}},
			{ID: 4, Name: "Холм", X: 0, Y: 3, Visited: true, Objects: []model.LocationObject{}},
		},
		Characters: []model.SnapshotCharacter{
			{ID: 11, Nickname: "Вор", Status: model.CharacterActive, FactionID: 1, LocationID: loc(2), Knowledge: []string{"Гонец свернул с дороги."}},
			{ID: 12, Nickname: "Маг", Status: model.CharacterActive, FactionID: 2, LocationID: loc(4)},
		},
	}
}

func stage(no int, key string, distance int, kind model.ConditionKind, status model.StageStatus, at *model.LocationID) model.QuestStage {
	return model.QuestStage{ID: model.QuestStageID(no), StageNo: no, Key: key, Title: key, Distance: distance, Condition: model.StageCondition{Kind: kind}, Status: status, LocationID: at}
}

// TestQuestPlan_Issue (S17): выдача по счётчику и лимитам: 0 сюжетных → сюжетный сразу; 1 активный и 1 клетка →
// побочный; 2 активных и 3 клетки → нет, 4 → сюжетный; 3 активных → нет; побочный занят → сюжетный.
func TestQuestPlan_Issue(t *testing.T) {
	t.Parallel()
	story := func(id model.QuestID, status model.QuestStatus) model.Quest {
		return model.Quest{ID: id, FactionID: 1, Kind: model.QuestStory, Status: status, Stages: []model.QuestStage{stage(1, "a", 1, model.ConditionVisited, model.StageBound, loc(2))}}
	}
	side := func(id model.QuestID) model.Quest {
		return model.Quest{ID: id, FactionID: 1, Kind: model.QuestSide, Status: model.QuestActive, Stages: []model.QuestStage{stage(1, "b", 1, model.ConditionVisited, model.StageBound, loc(2))}}
	}
	cases := []struct {
		name     string
		explored int
		quests   []model.Quest
		want     string // "" — задания нет
	}{
		{"no story: mandatory", 0, nil, "story mandatory"},
		{"done story only: mandatory", 0, []model.Quest{story(1, model.QuestDone)}, "story mandatory"},
		{"one active, zero cells", 0, []model.Quest{story(1, model.QuestActive)}, ""},
		{"one active, one cell: side", 1, []model.Quest{story(1, model.QuestActive)}, "side 1"},
		{"two active, three cells", 3, []model.Quest{story(1, model.QuestActive), side(2)}, ""},
		{"two active, four cells: story", 4, []model.Quest{story(1, model.QuestActive), side(2)}, "story 4"},
		{"three active: closed", 9, []model.Quest{story(1, model.QuestActive), side(2), story(3, model.QuestActive)}, ""},
		{"side slot taken: story", 1, []model.Quest{side(2)}, "story mandatory"},
		{"two stories: side", 4, []model.Quest{story(1, model.QuestActive), story(3, model.QuestActive)}, "side 4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := Input{Factions: []model.Faction{{ID: 1, ExploredSinceReset: c.explored}}, Quests: c.quests, State: questState(), StoryLimit: 2, SideLimit: 1}
			got := Plan(in)
			if c.want == "" {
				if len(got.Issue) != 0 {
					t.Fatalf("unexpected issue %+v", got.Issue)
				}
				return
			}
			if len(got.Issue) != 1 {
				t.Fatalf("issue = %+v, want %s", got.Issue, c.want)
			}
			is := got.Issue[0]
			desc := string(is.Kind) + " "
			if is.Mandatory {
				desc += "mandatory"
			} else {
				desc += strings.TrimSpace(strings.Repeat(" ", 0) + itoa(is.Threshold))
			}
			if desc != c.want || is.FactionID != 1 {
				t.Fatalf("issue = %+v (%s), want %s", is, desc, c.want)
			}
		})
	}
}

func itoa(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+n))) }

// TestQuestPlan_Bind (S17): первый незакрытый этап без клетки привязывается к клетке персонажа фракции на расстоянии
// ≥ заданного от клетки предыдущего этапа (первый — от стартовой); ближе — ждёт; привязанный этап заданий не даёт.
func TestQuestPlan_Bind(t *testing.T) {
	t.Parallel()
	st := questState()
	first := model.Quest{ID: 1, FactionID: 1, Kind: model.QuestStory, Status: model.QuestActive, Stages: []model.QuestStage{stage(1, "trail", 1, model.ConditionFact, model.StagePending, nil), stage(2, "camp", 2, model.ConditionVisited, model.StagePending, nil)}}
	far := model.Quest{ID: 2, FactionID: 1, Kind: model.QuestSide, Status: model.QuestActive, Stages: []model.QuestStage{stage(1, "hill", 3, model.ConditionVisited, model.StagePending, nil)}}
	second := model.Quest{ID: 3, FactionID: 2, Kind: model.QuestStory, Status: model.QuestActive, Stages: []model.QuestStage{stage(1, "a", 1, model.ConditionFact, model.StageDone, loc(2)), stage(2, "b", 2, model.ConditionVisited, model.StagePending, nil)}}
	bound := model.Quest{ID: 4, FactionID: 2, Kind: model.QuestSide, Status: model.QuestActive, Stages: []model.QuestStage{stage(1, "x", 1, model.ConditionVisited, model.StageBound, loc(4))}}
	in := Input{Factions: []model.Faction{{ID: 1}, {ID: 2}}, Quests: []model.Quest{first, far, second, bound}, State: st, StoryLimit: 2, SideLimit: 1,
		Locations: []model.Location{{ID: 1, X: 0, Y: 0, Start: true}, {ID: 2, X: 0, Y: 1}, {ID: 4, X: 0, Y: 3}}}
	got := Plan(in)
	if len(got.Bind) != 2 {
		t.Fatalf("bind = %+v", got.Bind)
	}
	// Вор в лесу (0,1): расстояние 1 от площади — первый этап квеста 1 привязывается; квест 2 требует 3 — ждёт.
	if b := got.Bind[0]; b.Quest.ID != 1 || b.Stage.Key != "trail" || b.LocationID != 2 {
		t.Fatalf("bind[0] = %+v", b)
	}
	// Маг на холме (0,3): второй этап квеста 3 на расстоянии 2 от леса (клетка первого этапа) — привязывается.
	if b := got.Bind[1]; b.Quest.ID != 3 || b.Stage.Key != "b" || b.LocationID != 4 {
		t.Fatalf("bind[1] = %+v", b)
	}
	// Цели: у фракций нет цели → задание на первую версию.
	if len(got.Goals) != 2 || got.Goals[0].FactionID != 1 || got.Goals[0].Closed != nil {
		t.Fatalf("goals = %+v", got.Goals)
	}
	// Цель есть, сюжетный квест закрыт после неё → пересмотр; цель уже ссылается на закрытый квест → нет.
	done := model.Quest{ID: 5, FactionID: 1, Kind: model.QuestStory, Status: model.QuestDone}
	in.Quests = append(in.Quests, done)
	in.Goals = []model.FactionGoal{{FactionID: 1, Version: 1, Text: "Найти гонца."}, {FactionID: 2, Version: 1, Text: "Груз."}}
	got = Plan(in)
	if len(got.Goals) != 1 || got.Goals[0].FactionID != 1 || got.Goals[0].Closed == nil || got.Goals[0].Closed.ID != 5 || got.Goals[0].Current.Text != "Найти гонца." {
		t.Fatalf("goal revision = %+v", got.Goals)
	}
	five := model.QuestID(5)
	in.Goals[0].QuestID = &five
	if got = Plan(in); len(got.Goals) != 0 {
		t.Fatalf("goal already revised: %+v", got.Goals)
	}
	// S22 (ADR-0031): клетка логова сил зла этапом не бывает — Маг на холме (0,3) стоит в логове, этап квеста 3 ждёт.
	in.Factions = []model.Faction{{ID: 1}, {ID: 2, Lair: &model.Coord{X: 0, Y: 3}}}
	got = Plan(in)
	if len(got.Bind) != 1 || got.Bind[0].Quest.ID != 1 {
		t.Fatalf("bind with lair = %+v", got.Bind)
	}
	// B54: расстояние 0 — этап там, где персонаж стоит сейчас: Паладин фракции 1 на стартовой площади (0,0), первый
	// этап «канат» про объект площади привязывается к ней же, не ждёт перехода.
	st.Characters = append(st.Characters, model.SnapshotCharacter{ID: 13, Nickname: "Паладин", Status: model.CharacterActive, FactionID: 1, LocationID: loc(1)})
	here := model.Quest{ID: 6, FactionID: 1, Kind: model.QuestStory, Status: model.QuestActive, Stages: []model.QuestStage{stage(1, "rope", 0, model.ConditionFact, model.StagePending, nil)}}
	in.Quests = []model.Quest{here}
	in.Factions = []model.Faction{{ID: 1}}
	got = Plan(in)
	if len(got.Bind) != 1 || got.Bind[0].Quest.ID != 6 || got.Bind[0].LocationID != 1 {
		t.Fatalf("bind at distance 0 = %+v", got.Bind)
	}
}

// TestQuestCheck_Conditions (S17): `fact` — факт у персонажа фракции (чужая фракция не считается); `object_state` —
// объект клетки этапа в состоянии; `visited` — клетка посещена; закрывается только первый незакрытый этап; последний
// этап закрывает квест; непривязанный этап не проверяется.
func TestQuestCheck_Conditions(t *testing.T) {
	t.Parallel()
	st := questState()
	fact := model.StageCondition{Kind: model.ConditionFact, Fact: "Гонец свернул с дороги."}
	dark := model.StageCondition{Kind: model.ConditionObjectState, Object: "altar", State: "lit"}
	quests := []model.Quest{
		{ID: 1, FactionID: 1, Status: model.QuestActive, Stages: []model.QuestStage{{ID: 1, StageNo: 1, Key: "a", Status: model.StageBound, LocationID: loc(2), Condition: fact, Reward: model.StageReward{Fact: "Награда."}}, {ID: 2, StageNo: 2, Key: "b", Status: model.StagePending, Condition: model.StageCondition{Kind: model.ConditionVisited}}}},
		{ID: 2, FactionID: 2, Status: model.QuestActive, Stages: []model.QuestStage{{ID: 3, StageNo: 1, Key: "a", Status: model.StageBound, LocationID: loc(2), Condition: fact}}},
		{ID: 3, FactionID: 1, Status: model.QuestActive, Stages: []model.QuestStage{{ID: 4, StageNo: 1, Key: "a", Status: model.StageBound, LocationID: loc(2), Condition: dark}}},
		{ID: 4, FactionID: 2, Status: model.QuestActive, Stages: []model.QuestStage{{ID: 5, StageNo: 1, Key: "a", Status: model.StageDone, LocationID: loc(2)}, {ID: 6, StageNo: 2, Key: "b", Status: model.StageBound, LocationID: loc(4), Condition: model.StageCondition{Kind: model.ConditionVisited}}}},
		{ID: 5, FactionID: 1, Status: model.QuestDone, Stages: []model.QuestStage{{ID: 7, StageNo: 1, Key: "a", Status: model.StageBound, LocationID: loc(2), Condition: fact}}},
	}
	done := Check(st, quests)
	if len(done) != 2 {
		t.Fatalf("done = %+v", done)
	}
	if d := done[0]; d.Quest.ID != 1 || d.Stage.Key != "a" || d.QuestDone || d.Stage.Reward.Fact != "Награда." {
		t.Fatalf("done[0] = %+v", d)
	}
	if d := done[1]; d.Quest.ID != 4 || d.Stage.Key != "b" || !d.QuestDone {
		t.Fatalf("done[1] = %+v", d)
	}
	// Алтарь зажжён → этап квеста 3 закрывается.
	st.Locations[1].Objects[0].State = "lit"
	if done = Check(st, quests[2:3]); len(done) != 1 || !done[0].QuestDone {
		t.Fatalf("object_state = %+v", done)
	}
}

// TestTemplateQuestAndStage: шаблон жанра даёт сюжетный квест с условиями по видам; без шаблона — запасной квест;
// шаблонное размещение этапа — объект «след» с подходом без броска, условие — его факт, ключ не повторяет объекты;
// этап `visited` — без объектов, условие остаётся `visited` (B45).
func TestTemplateQuestAndStage(t *testing.T) {
	t.Parallel()
	tpl := &model.QuestTemplate{Title: "След гонца", Summary: "Гонец пропал.", Stages: []model.StageTemplate{{Key: "trail", Title: "Найти след", Summary: "Где видели.", Distance: 1, Condition: model.ConditionFact, Reward: "Факт."}, {Key: "seal", Title: "Печать", Distance: 9, Condition: ""}}}
	q := TemplateQuest(tpl, model.Faction{ID: 7, GameID: 3})
	if q.FactionID != 7 || q.Kind != model.QuestStory || len(q.Stages) != 2 || q.Stages[0].Condition.Kind != model.ConditionFact || q.Stages[0].Reward.Fact != "Факт." || q.Stages[1].Distance != model.MaxStageDistance || q.Stages[1].Condition.Kind != model.ConditionVisited {
		t.Fatalf("template quest = %+v", q)
	}
	if q := TemplateQuest(nil, model.Faction{ID: 7}); len(q.Stages) != 3 || q.Title == "" {
		t.Fatalf("fallback quest = %+v", q)
	}
	if g := TemplateGoal(model.Faction{ID: 7, Description: "Хранители."}, ""); g.Text != "Хранители." || g.Stage == "" {
		t.Fatalf("goal = %+v", g)
	}
	objects, cond := TemplateStage(model.QuestStage{Key: "trail", Title: "Найти след", Summary: "Где видели гонца."}, []model.LocationObject{{Key: "trace_trail"}})
	if len(objects) != 1 || objects[0].Key != "trace_trail_2" || objects[0].Title != "след: найти след" || len(objects[0].Approaches) != 1 || objects[0].Approaches[0].Attribute != "" {
		t.Fatalf("objects = %+v", objects)
	}
	if cond.Kind != model.ConditionFact || cond.Fact != "Этап «Найти след»: Где видели гонца." || !strings.Contains(string(objects[0].Approaches[0].OnSuccess), cond.Fact) {
		t.Fatalf("condition = %+v, approach %s", cond, objects[0].Approaches[0].OnSuccess)
	}
	if objects, cond := TemplateStage(model.QuestStage{Key: "lookout", Title: "Осмотреться", Condition: model.StageCondition{Kind: model.ConditionVisited}}, nil); len(objects) != 0 || cond.Kind != model.ConditionVisited || cond.Fact != "" {
		t.Fatalf("visited template = %+v, %+v", objects, cond)
	}
}
