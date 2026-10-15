package interpreter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"taletogether/internal/model"
)

func loc(id model.LocationID) *model.LocationID { return &id }

// cellar — снимок по образцу сценариев S12–S13: подвал с запертой дверью и скрытым рычагом, зал со статуей.
func cellar() model.RoundSnapshot {
	return model.RoundSnapshot{
		SchemaVersion: 2, Mode: model.ModeBalanced,
		Locations: []model.SnapshotLocation{
			{ID: 1, Name: "Зал башни", Description: "Пыльный зал.", X: 0, Y: 0, Stage: model.StageDetailed, Start: true,
				Objects: []model.LocationObject{{Key: "statue", Title: "статуя", Description: "каменный рыцарь"}}},
			{ID: 2, Name: "Подвал башни", Description: "Сырой подвал.", Difficulty: 1, X: 0, Y: -1, Stage: model.StageDetailed,
				Objects: []model.LocationObject{
					{Key: "cellar_door", Title: "запертая дверь", State: "locked", Approaches: []model.Approach{
						{Action: model.ActionInteract, Title: "выбить", Attribute: "strength", Difficulty: 2},
						{Action: model.ActionInteract, Title: "отпереть", Attribute: "agility", Skill: "thievery", Difficulty: 1},
					}},
					{Key: "hidden_lever", Title: "потайной рычаг", HiddenUntil: true},
				}},
		},
		Characters: []model.SnapshotCharacter{
			{ID: 11, Nickname: "Вор", Status: model.CharacterActive, LocationID: loc(2), Attributes: map[string]int{"strength": 2, "agility": 4}, Skills: map[string]int{"thievery": 3, "melee": 0}, Knowledge: []string{"a", "b", "c", strings.Repeat("д", 200)}},
			{ID: 12, Nickname: "Кузнец", Status: model.CharacterActive, LocationID: loc(2), Attributes: map[string]int{"strength": 4, "agility": 2}},
			{ID: 13, Nickname: "Маг", Status: model.CharacterActive, LocationID: loc(1), Attributes: map[string]int{"wits": 4}},
			{ID: 15, Nickname: "Призрак", Status: model.CharacterDead, LocationID: loc(1)},
		},
		Submissions: []model.SnapshotSubmission{
			{SubmissionID: 71, CharacterID: 11, Text: "отпереть дверь"},
			{SubmissionID: 72, CharacterID: 12, Text: "простукиваю стену рядом с дверью"},
			{SubmissionID: 73, CharacterID: 13, Text: "иду вниз"},
		},
	}
}

func def() model.CharacterModelDef {
	return model.CharacterModelDef{
		Attributes: []model.AttributeDef{{Key: "strength", Title: "Сила"}, {Key: "agility", Title: "Ловкость"}, {Key: "wits", Title: "Разум"}},
		Skills:     []model.SkillDef{{Key: "thievery", Title: "Воровство", Attribute: "agility"}, {Key: "awareness", Title: "Внимательность", Attribute: "wits"}, {Key: "melee", Title: "Ближний бой", Attribute: "strength"}},
	}
}

type fakeLLM struct {
	responses []string
	err       error
	requests  []model.LLMRequest
}

func (f *fakeLLM) Complete(_ context.Context, req model.LLMRequest) (model.LLMResponse, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return model.LLMResponse{}, f.err
	}
	out := f.responses[0]
	if len(f.responses) > 1 {
		f.responses = f.responses[1:]
	}
	return model.LLMResponse{JSON: json.RawMessage(out), Usage: model.LLMUsage{Input: 100, Output: 20}}, nil
}

func newService(f *fakeLLM) *Service {
	return New(f, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

const goodResponse = `{"intents":[
 {"submission_id":71,"character_id":11,"action_type":"interact","target":"cellar_door","approach_idx":1,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null},
 {"submission_id":72,"character_id":12,"action_type":"explore","target":"cellar_door","approach_idx":null,"attribute":"wits","skill":"awareness","risk":"hard","on_success":{"fact":"За стеной пустота.","move":null,"reveal":"hidden_lever"},"on_fail":{"condition":"exhausted","damage":null,"fact":null},"push":true,"ambiguities":[],"fallback":null},
 {"submission_id":73,"character_id":13,"action_type":"move","target":"Подвал башни","approach_idx":null,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null}
]}`

// TestInterpret_Valid: подход объекта, свободное испытание с эффектами, переход по имени соседа → цель нормализована в направление.
func TestInterpret_Valid(t *testing.T) {
	t.Parallel()
	f := &fakeLLM{responses: []string{goodResponse}}
	res, err := newService(f).Interpret(context.Background(), cellar(), def())
	if err != nil {
		t.Fatal(err)
	}
	if res.Calls != 1 || res.Invalid != 0 || res.TokensIn != 100 || len(res.Intents) != 3 {
		t.Fatalf("result = %+v", res)
	}
	a, b, c := res.Intents[0], res.Intents[1], res.Intents[2]
	if a.ActionType != model.ActionInteract || a.Target != "cellar_door" || a.ApproachIdx == nil || *a.ApproachIdx != 1 || a.Attribute != "" || a.RawText != "отпереть дверь" {
		t.Errorf("approach intent = %+v", a)
	}
	if b.ActionType != model.ActionExplore || !b.FreeCheck() || b.Attribute != "wits" || b.Skill != "awareness" || b.Risk != model.RiskHard || !b.Push ||
		string(b.OnSuccess) != `{"fact":"За стеной пустота.","reveal":"hidden_lever"}` || string(b.OnFail) != `{"condition":"exhausted"}` {
		t.Errorf("free intent = %+v %s %s", b, b.OnSuccess, b.OnFail)
	}
	if c.ActionType != model.ActionMove || c.Target != "south" {
		t.Errorf("move intent = %+v", c)
	}
	req := f.requests[0]
	if req.Role != model.RoleInterpreter || req.System == "" || len(req.JSONSchema) == 0 || !strings.Contains(req.Input, `"text":"отпереть дверь"`) {
		t.Errorf("request = %+v", req)
	}
}

// TestInterpret_RetryThenSafe: невалидные намерения уходят в повтор с ошибками; после повтора — запасное действие
// модели или wait; пропущенная заявка получает wait.
func TestInterpret_RetryThenSafe(t *testing.T) {
	t.Parallel()
	first := `{"intents":[
 {"submission_id":71,"character_id":11,"action_type":"interact","target":"cellar_door","approach_idx":5,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":{"action_type":"explore","target":"cellar_door"}},
 {"submission_id":72,"character_id":12,"action_type":"interact","target":"стена","approach_idx":null,"attribute":"strength","skill":null,"risk":"normal","on_success":{"fact":"x","move":null,"reveal":null},"on_fail":null,"push":false,"ambiguities":[],"fallback":null},
 {"submission_id":99,"character_id":1,"action_type":"wait","target":"","approach_idx":null,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null}
]}`
	second := `{"intents":[
 {"submission_id":71,"character_id":11,"action_type":"interact","target":"cellar_door","approach_idx":7,"attribute":null,"skill":null,"risk":null,"on_success":null,"on_fail":null,"push":false,"ambiguities":[],"fallback":null},
 {"submission_id":72,"character_id":12,"action_type":"interact","target":"","approach_idx":null,"attribute":"strength","skill":null,"risk":"normal","on_success":{"fact":"Стена глухая.","move":null,"reveal":null},"on_fail":{"condition":null,"damage":1,"fact":null},"push":false,"ambiguities":["стены как объекта нет"],"fallback":null}
]}`
	f := &fakeLLM{responses: []string{first, second}}
	res, err := newService(f).Interpret(context.Background(), cellar(), def())
	if err != nil {
		t.Fatal(err)
	}
	if res.Calls != 2 || res.Invalid != 2 || len(res.Intents) != 3 {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(f.requests[1].Input, "заявка 71: approach_idx 5") || !strings.Contains(f.requests[1].Input, "заявка 73: намерение не возвращено") {
		t.Errorf("retry input = %q", f.requests[1].Input)
	}
	a, b, c := res.Intents[0], res.Intents[1], res.Intents[2]
	if a.ActionType != model.ActionExplore || a.Target != "cellar_door" || len(a.Ambiguities) != 1 || !strings.HasPrefix(a.Ambiguities[0], "безопасное толкование") {
		t.Errorf("fallback intent = %+v", a)
	}
	if b.ActionType != model.ActionInteract || b.Target != "" || b.Attribute != "strength" || string(b.OnFail) != `{"damage":1}` || len(b.Ambiguities) != 1 {
		t.Errorf("fixed intent = %+v", b)
	}
	if c.ActionType != model.ActionWait || c.CharacterID != 13 || len(c.Ambiguities) != 1 || !strings.Contains(c.Ambiguities[0], "не возвращено") {
		t.Errorf("missing intent = %+v", c)
	}
}

// TestInterpret_Rejections: ошибки валидации по каждому правилу → безопасное толкование без повторов.
func TestInterpret_Rejections(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"unknown action":    `"action_type":"fly","target":""`,
		"hidden exit":       `"action_type":"move","target":"Тайный ход"`,
		"hidden object":     `"action_type":"explore","target":"hidden_lever"`,
		"no approach":       `"action_type":"interact","target":"statue"`,
		"bad attribute":     `"action_type":"interact","target":"","attribute":"luck","risk":"normal","on_success":{"fact":"x","move":null,"reveal":null}`,
		"bad skill":         `"action_type":"interact","target":"","attribute":"wits","skill":"magic","risk":"normal","on_success":{"fact":"x","move":null,"reveal":null}`,
		"bad risk":          `"action_type":"interact","target":"","attribute":"wits","risk":"deadly","on_success":{"fact":"x","move":null,"reveal":null}`,
		"bad condition":     `"action_type":"interact","target":"","attribute":"wits","risk":"normal","on_success":{"fact":"x","move":null,"reveal":null},"on_fail":{"condition":"cursed","damage":null,"fact":null}`,
		"damage over limit": `"action_type":"interact","target":"","attribute":"wits","risk":"normal","on_success":{"fact":"x","move":null,"reveal":null},"on_fail":{"condition":null,"damage":5,"fact":null}`,
		"no success effect": `"action_type":"interact","target":"","attribute":"wits","risk":"normal","on_success":null`,
		"help far":          `"action_type":"help","target":"Вор"`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			snap := cellar()
			snap.Submissions = []model.SnapshotSubmission{{SubmissionID: 73, CharacterID: 13, Text: "x"}}
			resp := `{"intents":[{"submission_id":73,"character_id":13,` + body + `,"push":false,"ambiguities":[],"fallback":null}]}`
			f := &fakeLLM{responses: []string{resp}}
			svc := newService(f)
			svc.Retries = 0
			res, err := svc.Interpret(context.Background(), snap, def())
			if err != nil {
				t.Fatal(err)
			}
			if res.Invalid != 1 || res.Intents[0].ActionType != model.ActionWait || len(res.Intents[0].Ambiguities) != 1 {
				t.Fatalf("%s: %+v", name, res.Intents[0])
			}
		})
	}
}

// TestInterpret_ErrorsAndFallback: сбой модели или ответ не по схеме — ошибка вызывающему; Fallback разбирает кодом.
func TestInterpret_ErrorsAndFallback(t *testing.T) {
	t.Parallel()
	if _, err := newService(&fakeLLM{err: errors.New("down")}).Interpret(context.Background(), cellar(), def()); err == nil || !strings.Contains(err.Error(), "down") {
		t.Fatalf("err = %v", err)
	}
	if _, err := newService(&fakeLLM{responses: []string{`{"intents": "no"}`}}).Interpret(context.Background(), cellar(), def()); err == nil {
		t.Fatal("schema violation not reported")
	}
	if _, err := New(nil, slog.Default()).Interpret(context.Background(), cellar(), def()); !errors.Is(err, ErrNoLLM) {
		t.Fatalf("nil llm err = %v", err)
	}
	empty := cellar()
	empty.Submissions = nil
	if res, err := newService(&fakeLLM{}).Interpret(context.Background(), empty, def()); err != nil || res.Calls != 0 {
		t.Fatalf("empty = %+v %v", res, err)
	}
	ints := Fallback(cellar(), "интерпретатор недоступен")
	if len(ints) != 3 || ints[0].ActionType != model.ActionWait || ints[0].Ambiguities[0] != "интерпретатор недоступен" || ints[2].ActionType != model.ActionMove || ints[2].Target != "вниз" {
		t.Fatalf("fallback = %+v", ints)
	}
	// Заявка названием подхода (кнопка варианта, ADR-0015) разбирается в этот подход без модели.
	byTitle := cellar()
	loc := model.LocationID(2)
	byTitle.Characters[0].LocationID = &loc
	byTitle.Submissions[0].Text = " Отпереть "
	ints = Fallback(byTitle, "интерпретатор недоступен")
	if ints[0].ActionType != model.ActionInteract || ints[0].Target != "cellar_door" || ints[0].ApproachIdx == nil || *ints[0].ApproachIdx != 1 || len(ints[0].Ambiguities) != 0 {
		t.Fatalf("fallback by title = %+v", ints[0])
	}
}

// TestBuildInput_NoLeaks: скрытые выходы и объекты, выбывшие персонажи не попадают в контекст; знания обрезаны.
func TestBuildInput_NoLeaks(t *testing.T) {
	t.Parallel()
	in := BuildInput(cellar(), def())
	s := in.Encode()
	for _, leak := range []string{"Тайный ход", "hidden_lever", "Призрак", "ддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддддд"} {
		if strings.Contains(s, leak) {
			t.Errorf("leak %q in %s", leak, s)
		}
	}
	if len(in.Scenes) != 2 || len(in.Scenes[1].Objects) != 1 || len(in.Scenes[1].Objects[0].Approaches) != 2 || len(in.Scenes[0].Exits) != 1 {
		t.Fatalf("scenes = %+v", in.Scenes)
	}
	thief := in.Scenes[1].Characters[0]
	if len(thief.Knowledge) != 3 || thief.Knowledge[0] != "b" || !strings.HasSuffix(thief.Knowledge[2], "…") || len(thief.Skills) != 1 {
		t.Errorf("thief = %+v", thief)
	}
	if len(in.Submissions) != 3 || in.Submissions[0].Nickname != "Вор" || !strings.Contains(s, `"submission_id":71`) {
		t.Errorf("submissions = %+v", in.Submissions)
	}
}

// TestBuildInput_HiddenExit (ADR-0025): выход к скрытому соседу подписан видом издалека без имени; цель `move` по
// имени скрытого соседа не проходит проверку, по направлению — проходит.
func TestBuildInput_HiddenExit(t *testing.T) {
	t.Parallel()
	snap := cellar()
	snap.Locations = append(snap.Locations, model.SnapshotLocation{ID: 3, Name: "Деревня", Description: "Десяток дворов.", X: 1, Y: 0, Stage: model.StageCard, Hidden: true, DistantView: "дым над крышами", Objects: []model.LocationObject{}})
	in := BuildInput(snap, def())
	hall := in.Scenes[0]
	if len(hall.Exits) != 2 || hall.Exits[1].To != "east" || hall.Exits[1].Title != "на восток: неизведанная территория (дым над крышами)" {
		t.Fatalf("exits = %+v", hall.Exits)
	}
	if enc := in.Encode(); strings.Contains(enc, "Деревня") {
		t.Fatalf("hidden name leaked: %s", enc)
	}
	v := newValidator(snap, def())
	sub := model.SnapshotSubmission{SubmissionID: 73, CharacterID: 13, Text: "иду на дым"}
	if _, err := v.intent(sub, rawIntent{SubmissionID: 73, ActionType: "move", Target: "Деревня"}); err == nil || !strings.Contains(err.Error(), "неизведанная территория (дым над крышами)") {
		t.Fatalf("move by hidden name = %v", err)
	}
	if got, err := v.intent(sub, rawIntent{SubmissionID: 73, ActionType: "move", Target: "east"}); err != nil || got.Target != "east" {
		t.Fatalf("move by direction = %+v, %v", got, err)
	}
}
