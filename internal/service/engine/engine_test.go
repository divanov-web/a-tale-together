package engine

import (
	"encoding/json"
	"reflect"
	"testing"

	"taletogether/internal/model"
)

// cellar — снимок сценариев S06, S07, S10–S12: зал башни и подвал с запертой дверью (docs/game/scenarios.md).
func cellar(mode model.GameMode) model.RoundSnapshot {
	hall, cellarID := model.LocationID(1), model.LocationID(2)
	loc := func(id model.LocationID) *model.LocationID { return &id }
	return model.RoundSnapshot{
		SchemaVersion: 2, Mode: mode,
		Locations: []model.SnapshotLocation{
			{ID: hall, Name: "Зал башни", Description: "Пыльный зал.", Links: []model.LocationLink{{To: "Подвал башни", Title: "вниз в подвал"}, {To: "Тайный ход", Title: "тайный ход", Hidden: true}},
				Objects: []model.LocationObject{{Key: "statue", Title: "статуя", Description: "каменный рыцарь"}}},
			{ID: cellarID, Name: "Подвал башни", Description: "Сырой подвал.", Difficulty: 1, Links: []model.LocationLink{{To: "Зал башни", Title: "наверх в зал"}},
				Objects: []model.LocationObject{
					{Key: "cellar_door", Title: "запертая дверь", State: "locked", Approaches: []model.Approach{
						{Action: model.ActionInteract, Title: "выбить", Attribute: "strength", Difficulty: 2, OnSuccess: json.RawMessage(`{"state":"broken"}`), OnFail: json.RawMessage(`{"condition":"exhausted"}`)},
						{Action: model.ActionInteract, Title: "отпереть", Attribute: "agility", Skill: "thievery", Difficulty: 1, OnSuccess: json.RawMessage(`{"state":"open"}`)},
						{Action: model.ActionExplore, Title: "найти рычаг", Attribute: "wits", Skill: "awareness", Difficulty: 3, OnSuccess: json.RawMessage(`{"reveal":"hidden_lever"}`)},
					}},
					{Key: "hidden_lever", Title: "потайной рычаг", HiddenUntil: true, Approaches: []model.Approach{{Action: model.ActionInteract, Difficulty: 0, OnSuccess: json.RawMessage(`{"object":"cellar_door","state":"open"}`)}}},
				}},
		},
		Characters: []model.SnapshotCharacter{
			{ID: 11, Nickname: "Вор", Status: model.CharacterActive, HP: 6, MaxHP: 6, LocationID: loc(cellarID), Conditions: []string{},
				Attributes: map[string]int{"strength": 2, "agility": 4, "wits": 3, "will": 3}, Skills: map[string]int{"thievery": 3}, Inventory: []model.Item{{Key: "lockpicks", Title: "Отмычки", Bonus: &model.ItemBonus{Skill: "thievery", Dice: 1}}}},
			{ID: 12, Nickname: "Кузнец", Status: model.CharacterActive, HP: 8, MaxHP: 8, LocationID: loc(cellarID), Conditions: []string{},
				Attributes: map[string]int{"strength": 4, "agility": 2, "wits": 2, "will": 4}, Skills: map[string]int{"melee": 3}},
			{ID: 13, Nickname: "Маг", Status: model.CharacterActive, HP: 6, MaxHP: 6, LocationID: loc(hall), Conditions: []string{},
				Attributes: map[string]int{"strength": 2, "agility": 2, "wits": 4, "will": 4}, Skills: map[string]int{"awareness": 3}},
			{ID: 14, Nickname: "Паладин", Status: model.CharacterActive, HP: 8, MaxHP: 8, LocationID: loc(hall), Conditions: []string{},
				Attributes: map[string]int{"strength": 4, "agility": 3, "wits": 2, "will": 3}},
			{ID: 15, Nickname: "Призрак", Status: model.CharacterDead, HP: 0, MaxHP: 6, LocationID: loc(hall), Conditions: []string{}, DeathRoundsLeft: 0,
				Attributes: map[string]int{"strength": 2, "agility": 2, "wits": 4, "will": 4}},
		},
	}
}

func intents() []model.Intent {
	return []model.Intent{
		{SubmissionID: 1, CharacterID: 11, ActionType: model.ActionInteract, Target: "дверь", ApproachIdx: ptr(1)},
		{SubmissionID: 2, CharacterID: 12, ActionType: model.ActionInteract, Target: "дверь", ApproachIdx: ptr(0), Push: true},
		{SubmissionID: 3, CharacterID: 13, ActionType: model.ActionMove, Target: "подвал"},
		{SubmissionID: 4, CharacterID: 14, ActionType: model.ActionAttack, Target: "Вор"},
		{SubmissionID: 5, CharacterID: 15, ActionType: model.ActionWait},
		{SubmissionID: 6, CharacterID: 14, Ambiguities: []string{"что осмотреть"}},
	}
}

func ptr(v int) *int { return &v }

// TestScenario_S07_NobodyLost: шесть заявок → шесть итогов, у каждой ровно один статус и события по ссылкам.
func TestScenario_S07_NobodyLost(t *testing.T) {
	t.Parallel()
	res := Resolve(cellar(model.ModeBalanced), intents(), 42)
	if len(res.Results) != 6 {
		t.Fatalf("results = %d; want 6", len(res.Results))
	}
	byID := map[model.SubmissionID]model.ActionResult{}
	for _, r := range res.Results {
		byID[r.SubmissionID] = r
		if r.Status == "" {
			t.Errorf("submission %d without status", r.SubmissionID)
		}
		if len(r.EventSeqs) == 0 {
			t.Errorf("submission %d without events", r.SubmissionID)
		}
		for _, seq := range r.EventSeqs {
			if seq < 1 || int(seq) > len(res.Events) || res.Events[seq-1].Seq != seq {
				t.Errorf("submission %d references missing event %d", r.SubmissionID, seq)
			}
		}
	}
	if byID[1].Status != model.ResultResolved || byID[1].Check == nil || byID[1].Check.Pool != 8 || byID[1].Check.Difficulty != 2 {
		t.Errorf("thief = %+v", byID[1])
	}
	if byID[2].Status != model.ResultResolved || byID[2].Check == nil || byID[2].Check.Pool != 4 || byID[2].Check.Difficulty != 3 {
		t.Errorf("smith = %+v", byID[2])
	}
	if byID[3].Status != model.ResultResolved || byID[3].Summary != "переходит: Зал башни → Подвал башни" {
		t.Errorf("mage move = %+v", byID[3])
	}
	if byID[4].Status != model.ResultRejected || byID[4].Reason != "действие «атаковать» появится в пункте плана 12" {
		t.Errorf("attack = %+v", byID[4])
	}
	if byID[5].Status != model.ResultRejected || byID[5].Reason != "персонаж выбыл из игры до возвращения" {
		t.Errorf("dead wait = %+v", byID[5])
	}
	if byID[6].Status != model.ResultAmbiguous || byID[6].Reason != "заявка не разобрана; что осмотреть" {
		t.Errorf("ambiguous = %+v", byID[6])
	}
	// Призрак возвращается: счётчик истёк.
	last := res.Events[len(res.Events)-1]
	if last.Type != model.EventCharacterReturned || last.Visibility != model.VisibilityPublic {
		t.Errorf("last event = %+v", last)
	}
	for i, ev := range res.Events {
		if ev.Seq != int64(i+1) {
			t.Errorf("event %d seq = %d", i, ev.Seq)
		}
	}
}

// TestScenario_S06_Replay: тот же снимок и seed дают те же события и итоги; другой seed — другие кубики.
func TestScenario_S06_Replay(t *testing.T) {
	t.Parallel()
	a := Resolve(cellar(model.ModeMechanics), intents(), 7)
	b := Resolve(cellar(model.ModeMechanics), intents(), 7)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("replay with same seed differs")
	}
	c := Resolve(cellar(model.ModeMechanics), intents(), 8)
	same := true
	for i := range a.Results {
		if a.Results[i].Check != nil && c.Results[i].Check != nil && !reflect.DeepEqual(a.Results[i].Check.Dice, c.Results[i].Check.Dice) {
			same = false
		}
	}
	if same {
		t.Fatal("different seeds rolled identical dice for every check")
	}
}

// TestScenario_S10_Move: переход по выходу, скрытый выход и несуществующий отклоняются; событие видно в обеих локациях.
func TestScenario_S10_Move(t *testing.T) {
	t.Parallel()
	snap := cellar(model.ModeBalanced)
	res := Resolve(snap, []model.Intent{
		{SubmissionID: 1, CharacterID: 13, ActionType: model.ActionMove, Target: "Подвал башни"},
		{SubmissionID: 2, CharacterID: 14, ActionType: model.ActionMove, Target: "тайный ход"},
		{SubmissionID: 3, CharacterID: 11, ActionType: model.ActionMove, Target: "крыша"},
	}, 1)
	if res.Results[0].Status != model.ResultResolved || res.Results[1].Status != model.ResultRejected || res.Results[2].Status != model.ResultRejected {
		t.Fatalf("results = %+v", res.Results)
	}
	if res.Results[2].Reason != "из «Подвал башни» нет выхода «крыша»; выходы: наверх в зал (Зал башни)" {
		t.Fatalf("reason = %q", res.Results[2].Reason)
	}
	moved := 0
	for _, ev := range res.Events {
		if ev.Type == model.EventCharacterMoved {
			moved++
			if ev.Visibility != model.VisibilityLocation || ev.LocationID == nil {
				t.Fatalf("move event = %+v", ev)
			}
		}
	}
	if moved != 2 || *res.Events[0].LocationID != 1 || *res.Events[1].LocationID != 2 {
		t.Fatalf("move events = %+v", res.Events[:2])
	}
	// Свёртка событий даёт то же состояние.
	after := cellar(model.ModeBalanced)
	if err := Apply(&after, res.Events); err != nil {
		t.Fatal(err)
	}
	mage, _ := after.Character(13)
	if mage.LocationID == nil || *mage.LocationID != 2 {
		t.Fatalf("mage after apply = %+v", mage)
	}
}

// TestScenario_S11_Explore: осмотр локации и объекта даёт приватный факт; скрытый объект не виден до раскрытия.
func TestScenario_S11_Explore(t *testing.T) {
	t.Parallel()
	snap := cellar(model.ModeBalanced)
	res := Resolve(snap, []model.Intent{
		{SubmissionID: 1, CharacterID: 13, ActionType: model.ActionExplore},
		{SubmissionID: 2, CharacterID: 14, ActionType: model.ActionExplore, Target: "статуя"},
		{SubmissionID: 3, CharacterID: 11, ActionType: model.ActionExplore, Target: "рычаг"},
	}, 3)
	if res.Results[0].Status != model.ResultResolved || res.Results[1].Status != model.ResultResolved || res.Results[2].Status != model.ResultRejected {
		t.Fatalf("results = %+v", res.Results)
	}
	if res.Events[0].Type != model.EventFactLearned || res.Events[0].Visibility != model.VisibilityPrivate || !reflect.DeepEqual(res.Events[0].VisibleTo, []model.CharacterID{13}) {
		t.Fatalf("fact event = %+v", res.Events[0])
	}
	after := cellar(model.ModeBalanced)
	if err := Apply(&after, res.Events); err != nil {
		t.Fatal(err)
	}
	mage, _ := after.Character(13)
	if len(mage.Knowledge) != 1 || mage.Knowledge[0] != "Зал башни: Пыльный зал. Выходы: вниз в подвал (Подвал башни). Объекты: статуя." {
		t.Fatalf("knowledge = %q", mage.Knowledge)
	}
}

// TestScenario_S12_LockedDoor: проверка с форсированием и ценой, замена провала в повествовании, цепочка reveal → рычаг → дверь.
func TestScenario_S12_LockedDoor(t *testing.T) {
	t.Parallel()
	// Ищем seed, при котором Кузнец проваливает первый бросок и форсирует.
	var pushedSeed int64 = -1
	for seed := int64(1); seed < 200; seed++ {
		res := Resolve(cellar(model.ModeMechanics), []model.Intent{{SubmissionID: 2, CharacterID: 12, ActionType: model.ActionInteract, Target: "дверь", ApproachIdx: ptr(0), Push: true}}, seed)
		if res.Results[0].Check.Pushed {
			pushedSeed = seed
			break
		}
	}
	if pushedSeed < 0 {
		t.Fatal("no seed with a pushed roll")
	}
	res := Resolve(cellar(model.ModeMechanics), []model.Intent{{SubmissionID: 2, CharacterID: 12, ActionType: model.ActionInteract, Target: "дверь", ApproachIdx: ptr(0), Push: true}}, pushedSeed)
	r := res.Results[0]
	exhausted := false
	for _, ev := range res.Events {
		if ev.Type == model.EventConditionAdded {
			exhausted = true
		}
	}
	if !exhausted || len(r.Check.PushDice) != r.Check.Pool {
		t.Fatalf("pushed roll without cost: %+v events %+v", r, res.Events)
	}
	if r.Outcome == model.OutcomeSuccess {
		t.Fatalf("pushed success must be costly: %+v", r)
	}
	// Повествование: провал без deadly становится успехом с ценой, форсирование без состояния.
	narr := Resolve(cellar(model.ModeNarrative), []model.Intent{{SubmissionID: 2, CharacterID: 12, ActionType: model.ActionInteract, Target: "дверь", ApproachIdx: ptr(0)}}, pushedSeed)
	if narr.Results[0].Outcome == model.OutcomeFailure || narr.Results[0].Check.Difficulty != 2 {
		t.Fatalf("narrative = %+v", narr.Results[0])
	}
	for _, ev := range narr.Events {
		if ev.Type == model.EventConditionAdded {
			t.Fatalf("narrative push added a condition: %+v", ev)
		}
	}
	// Рычаг: найден (reveal), затем нажат — дверь открыта без броска.
	snap := cellar(model.ModeBalanced)
	snap.Locations[1].Objects[1].Revealed = true
	res = Resolve(snap, []model.Intent{{SubmissionID: 1, CharacterID: 11, ActionType: model.ActionInteract, Target: "рычаг"}}, 1)
	if res.Results[0].Status != model.ResultResolved || res.Results[0].Check != nil {
		t.Fatalf("lever = %+v", res.Results[0])
	}
	after := cellar(model.ModeBalanced)
	after.Locations[1].Objects[1].Revealed = true
	if err := Apply(&after, res.Events); err != nil {
		t.Fatal(err)
	}
	if after.Locations[1].Objects[0].State != "open" {
		t.Fatalf("door after lever = %+v", after.Locations[1].Objects[0])
	}
}

// TestHelp: помощь добавляет кубик к пулу цели в той же локации; не себе и не издалека.
func TestHelp(t *testing.T) {
	t.Parallel()
	res := Resolve(cellar(model.ModeBalanced), []model.Intent{
		{SubmissionID: 1, CharacterID: 12, ActionType: model.ActionHelp, Target: "Вор"},
		{SubmissionID: 2, CharacterID: 11, ActionType: model.ActionInteract, Target: "дверь", ApproachIdx: ptr(1)},
		{SubmissionID: 3, CharacterID: 13, ActionType: model.ActionHelp, Target: "Вор"},
		{SubmissionID: 4, CharacterID: 14, ActionType: model.ActionHelp, Target: "Паладин"},
	}, 5)
	by := map[model.SubmissionID]model.ActionResult{}
	for _, r := range res.Results {
		by[r.SubmissionID] = r
	}
	if by[1].Status != model.ResultResolved || by[2].Check.Pool != 9 || by[3].Status != model.ResultRejected || by[4].Status != model.ResultRejected {
		t.Fatalf("results = %+v", res.Results)
	}
}

// TestScenario_S13_FreeCheck: свободное испытание без подхода — сложность по таблице риска плюс локация,
// при успехе факт (и переход по эффекту move), при провале состояние; объекты локации не меняются.
func TestScenario_S13_FreeCheck(t *testing.T) {
	t.Parallel()
	free := model.Intent{SubmissionID: 1, CharacterID: 14, ActionType: model.ActionInteract, Attribute: "agility", Risk: model.RiskHard,
		OnSuccess: json.RawMessage(`{"fact":"Ров преодолён: тропа к башне.","move":"вниз в подвал","state":"broken"}`), OnFail: json.RawMessage(`{"condition":"wounded"}`)}
	var successes, failures int
	for seed := int64(1); seed <= 40; seed++ {
		snap := cellar(model.ModeBalanced)
		res := Resolve(snap, []model.Intent{free}, seed)
		r := res.Results[0]
		if r.Status != model.ResultResolved || r.Check == nil || r.Check.Pool != 3 || r.Check.Difficulty != 2 || r.Check.Attribute != "agility" {
			t.Fatalf("seed %d: result = %+v", seed, r)
		}
		var state model.RoundSnapshot
		state = Clone(snap)
		if err := Apply(&state, res.Events); err != nil {
			t.Fatal(err)
		}
		c, _ := state.Character(14)
		types := map[model.EventType]int{}
		for _, ev := range res.Events {
			types[ev.Type]++
		}
		if types[model.EventObjectChanged] != 0 {
			t.Fatalf("seed %d: free check changed objects: %+v", seed, res.Events)
		}
		switch r.Outcome {
		case model.OutcomeSuccess:
			successes++
			if types[model.EventFactLearned] != 1 || types[model.EventCharacterMoved] != 2 || *c.LocationID != 2 || c.HasCondition(model.ConditionWounded) {
				t.Fatalf("seed %d: success branch: %+v / %+v", seed, types, c)
			}
		case model.OutcomeFailure:
			failures++
			if types[model.EventConditionAdded] != 1 || !c.HasCondition(model.ConditionWounded) || *c.LocationID != 1 || types[model.EventFactLearned] != 0 {
				t.Fatalf("seed %d: fail branch: %+v / %+v", seed, types, c)
			}
		}
	}
	if successes == 0 || failures == 0 {
		t.Fatalf("branches: successes %d, failures %d", successes, failures)
	}
}

// TestFreeCheck_MoveGateAndSafeFallback: переход с испытанием не выполняется при провале;
// безопасное толкование (тип задан, неоднозначности есть) разрешается как обычное действие с пометкой в итоге.
func TestFreeCheck_MoveGateAndSafeFallback(t *testing.T) {
	t.Parallel()
	gate := model.Intent{SubmissionID: 1, CharacterID: 13, ActionType: model.ActionMove, Target: "подвал", Attribute: "wits", Risk: model.RiskHard, OnFail: json.RawMessage(`{"damage":1}`)}
	safe := model.Intent{SubmissionID: 2, CharacterID: 14, ActionType: model.ActionWait, Ambiguities: []string{"два сундука"}}
	var stayed, moved bool
	for seed := int64(1); seed <= 60; seed++ {
		res := Resolve(cellar(model.ModeBalanced), []model.Intent{gate, safe}, seed)
		g, s := res.Results[1], res.Results[0] // фазы: wait раньше move
		if g.Status != model.ResultResolved || g.Check == nil || g.Check.Difficulty != 2 || g.Check.Pool != 4 {
			t.Fatalf("seed %d: gate = %+v", seed, g)
		}
		if g.Outcome == model.OutcomeFailure {
			stayed = true
			if g.Summary != "пробует перейти: Зал башни → Подвал башни — провал" {
				t.Fatalf("seed %d: summary %q", seed, g.Summary)
			}
		} else {
			moved = true
			if g.Summary != "переходит: Зал башни → Подвал башни" {
				t.Fatalf("seed %d: summary %q", seed, g.Summary)
			}
		}
		if s.Status != model.ResultResolved || s.Summary != "ждёт и держится настороже (примечание: два сундука)" {
			t.Fatalf("safe = %+v", s)
		}
	}
	if !stayed || !moved {
		t.Fatalf("gate branches: stayed %v, moved %v", stayed, moved)
	}
}
