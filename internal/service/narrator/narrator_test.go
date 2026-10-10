package narrator

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

func loc(id model.LocationID) *model.LocationID    { return &id }
func char(id model.CharacterID) *model.CharacterID { return &id }

// tower — состояние после раунда «запертая дверь» (docs/llm/round-calls.md): дверь открыта Вором, Кузнец истощён;
// Маг в зале, Призрак выбыл.
func tower() model.RoundSnapshot {
	return model.RoundSnapshot{
		SchemaVersion: 2, Mode: model.ModeBalanced,
		Locations: []model.SnapshotLocation{
			{ID: 1, Name: "Зал башни", Description: "Пыльный зал.", Links: []model.LocationLink{{To: "Подвал башни", Title: "вниз в подвал"}, {To: "Тайный ход", Title: "тайный ход", Hidden: true}},
				Objects: []model.LocationObject{{Key: "statue", Title: "статуя", Approaches: []model.Approach{{Action: model.ActionExplore, Title: "осмотреть статую", Attribute: "wits", Skill: "awareness", Difficulty: 1}}}}},
			{ID: 2, Name: "Подвал башни", Description: "Сырой подвал.", Difficulty: 1, Links: []model.LocationLink{{To: "Зал башни", Title: "наверх в зал"}},
				Objects: []model.LocationObject{
					{Key: "cellar_door", Title: "дверь", State: "open", Approaches: []model.Approach{
						{Action: model.ActionInteract, Title: "выбить", Attribute: "strength", Difficulty: 2},
						{Action: model.ActionInteract, Title: "отпереть", Attribute: "agility", Skill: "thievery", Difficulty: 1},
					}},
					{Key: "hidden_lever", Title: "потайной рычаг", HiddenUntil: true, Approaches: []model.Approach{{Action: model.ActionInteract, Title: "дёрнуть"}}},
				}},
		},
		Characters: []model.SnapshotCharacter{
			{ID: 11, Nickname: "Вор", Status: model.CharacterActive, LocationID: loc(2), Attributes: map[string]int{"strength": 2, "agility": 4}, Skills: map[string]int{"thievery": 3}, Conditions: []string{}},
			{ID: 12, Nickname: "Кузнец", Status: model.CharacterWaiting, LocationID: loc(2), Attributes: map[string]int{"strength": 4, "agility": 2}, Conditions: []string{model.ConditionExhausted}},
			{ID: 13, Nickname: "Маг", Status: model.CharacterActive, LocationID: loc(1), Attributes: map[string]int{"wits": 4}, Skills: map[string]int{"awareness": 3}},
			{ID: 15, Nickname: "Призрак", Status: model.CharacterDead, LocationID: loc(1)},
		},
	}
}

func def() model.CharacterModelDef {
	return model.CharacterModelDef{
		Attributes: []model.AttributeDef{{Key: "strength", Title: "Сила"}, {Key: "agility", Title: "Ловкость"}, {Key: "wits", Title: "Разум"}},
		Skills:     []model.SkillDef{{Key: "thievery", Title: "Воровство", Attribute: "agility"}, {Key: "awareness", Title: "Внимательность", Attribute: "wits"}},
	}
}

func events() []model.Event {
	check := func(outcome string, pushed bool) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"character_id": 12, "object": "cellar_door", "title": "дверь", "free": false, "summary": "взаимодействует: дверь",
			"check": model.CheckInfo{Attribute: "strength", Pool: 4, Dice: []int{1, 2, 3, 4}, Successes: 0, Difficulty: 3, Outcome: outcome, Pushed: pushed, ChancePct: 11}})
		return b
	}
	return []model.Event{
		{Seq: 117, Type: model.EventObjectChanged, Visibility: model.VisibilityLocation, LocationID: loc(2), CharacterID: char(11), Payload: json.RawMessage(`{"character_id":11,"location_id":2,"object":"cellar_door","title":"дверь","from":"locked","to":"open"}`)},
		{Seq: 118, Type: model.EventCheckRolled, Visibility: model.VisibilityLocation, LocationID: loc(2), CharacterID: char(12), Payload: check(model.OutcomeFailure, true)},
		{Seq: 119, Type: model.EventCharacterMoved, Visibility: model.VisibilityLocation, LocationID: loc(1), CharacterID: char(13), Payload: json.RawMessage(`{"character_id":13,"from":1,"to":2,"from_name":"Зал башни","to_name":"Подвал башни"}`)},
		{Seq: 120, Type: model.EventCharacterMoved, Visibility: model.VisibilityLocation, LocationID: loc(2), CharacterID: char(13), Payload: json.RawMessage(`{"character_id":13,"from":1,"to":2,"from_name":"Зал башни","to_name":"Подвал башни"}`)},
		{Seq: 121, Type: model.EventFactLearned, Visibility: model.VisibilityPrivate, VisibleTo: []model.CharacterID{11}, LocationID: loc(2), CharacterID: char(11), Payload: json.RawMessage(`{"character_id":11,"fact":"За дверью коридор."}`)},
	}
}

func params(mode model.GameMode) Params {
	st := tower()
	st.Mode = mode
	return Params{
		Title: "Серая башня", Tone: "мрачная ирония", Mode: mode, RoundNo: 7,
		Factions: []Faction{{Code: "good", Title: "Орден", Relations: map[string]string{"evil": "hostile"}}},
		Previous: []model.Narration{{Chronicle: "Раунд шесть: " + strings.Repeat("х", 500)}, {Chronicle: "Раунд пять."}},
		State:    &st,
		Bios:     map[model.CharacterID]Bio{11: {Public: "ловкий вор", Secret: "на самом деле шпион Гильдии теней"}, 12: {Public: "кузнец из деревни"}},
		Events:   events(), Def: def(),
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
	return model.LLMResponse{JSON: json.RawMessage(out), TokensIn: 100, TokensOut: 30}, nil
}

func newService(f *fakeLLM) *Service {
	return New(f, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

const goodResponse = `{"chronicle":"Над башней седьмой вечер без луны.","scenes":[
 {"location_id":1,"party":"Зал башни","text":"Маг спускается в подвал.","event_seqs":[119]},
 {"location_id":2,"party":"Подвал","text":"Кузнец бил в дверь, пока не выдохся — камень не поддался. Вор дождался щелчка: дверь приоткрылась. Маг сошёл по ступеням.","event_seqs":[117,118]}
]}`

// TestBuildInput: сцены только с присутствующими (выбывший не входит), скрытое описание как данные, выходы и объекты
// видимые, приватные события отброшены, пара событий перехода передана целиком, выжимка прошлых хроник обрезана и
// идёт от старых к новым; числа броска только в режиме «механика», сложность подходов не передаётся в «повествовании».
func TestBuildInput(t *testing.T) {
	t.Parallel()
	in := BuildInput(params(model.ModeBalanced))
	if len(in.Scenes) != 2 || in.Scenes[0].LocationID != 1 || len(in.Scenes[0].Characters) != 1 || len(in.Scenes[1].Characters) != 2 {
		t.Fatalf("scenes = %+v", in.Scenes)
	}
	cellar := in.Scenes[1]
	if cellar.Characters[0].SecretBio != "на самом деле шпион Гильдии теней" || cellar.Characters[1].Status != "waiting" || cellar.Characters[1].Conditions[0] != "истощён" {
		t.Errorf("cellar characters = %+v", cellar.Characters)
	}
	if len(in.Scenes[0].Exits) != 1 || len(cellar.Objects) != 1 || cellar.Objects[0].State != "открыто" {
		t.Errorf("exits/objects = %+v / %+v", in.Scenes[0].Exits, cellar.Objects)
	}
	if len(in.Events) != 4 || in.Events[1].Outcome != "провал" || !in.Events[1].Pushed || in.Events[1].Numbers != nil || in.Events[3].Summary != "Маг приходит из «Зал башни» в «Подвал башни»" {
		t.Errorf("events = %+v", in.Events)
	}
	if len(in.World.Previous) != 2 || in.World.Previous[0] != "Раунд пять." || !strings.HasSuffix(in.World.Previous[1], "…") || len([]rune(in.World.Previous[1])) != previousMaxRune+1 {
		t.Errorf("previous = %q", in.World.Previous)
	}
	if len(in.Approaches) != 3 || in.Approaches[1].Difficulty != 3 || in.Approaches[1].Attribute != "Сила" {
		t.Errorf("approaches = %+v", in.Approaches)
	}
	mech := BuildInput(params(model.ModeMechanics))
	if mech.Events[1].Numbers == nil || mech.Events[1].Numbers.Pool != 4 || mech.Events[1].Numbers.Difficulty != 3 {
		t.Errorf("mechanics numbers = %+v", mech.Events[1].Numbers)
	}
	narr := BuildInput(params(model.ModeNarrative))
	if narr.Approaches[1].Difficulty != 0 || narr.Events[1].Numbers != nil {
		t.Errorf("narrative approaches = %+v", narr.Approaches)
	}
	if strings.Contains(narr.Encode(), "За дверью коридор") {
		t.Error("private fact leaked into input")
	}
}

// TestNarrate_Valid: один вызов, все события упомянуты (пара перехода — одним seq), ничего не дописано.
func TestNarrate_Valid(t *testing.T) {
	t.Parallel()
	f := &fakeLLM{responses: []string{goodResponse}}
	res, err := newService(f).Narrate(context.Background(), params(model.ModeBalanced))
	if err != nil {
		t.Fatal(err)
	}
	if res.Calls != 1 || res.Patched != 0 || len(res.Issues) != 0 || res.TokensIn != 100 || res.Narration.PromptVersion != PromptVersion {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Narration.Scenes) != 2 || res.Narration.Scenes[1].Party != "Подвал" || len(res.Narration.Scenes[1].EventSeqs) != 2 {
		t.Fatalf("narration = %+v", res.Narration)
	}
	req := f.requests[0]
	if req.Role != model.RoleNarrator || req.System == "" || len(req.JSONSchema) == 0 || !strings.Contains(req.Input, `"round_no":7`) {
		t.Errorf("request = %+v", req)
	}
}

// TestNarrate_RetryThenPatch: пропущенное событие и выдуманный seq → дозапрос с замечаниями; если модель снова
// пропустила событие, код дописывает строку в сцену его локации и помечает её в Patched.
func TestNarrate_RetryThenPatch(t *testing.T) {
	t.Parallel()
	first := `{"chronicle":"Тихо.","scenes":[{"location_id":2,"party":"Подвал","text":"Вор открыл дверь.","event_seqs":[117,999]}]}`
	second := `{"chronicle":"Тихо.","scenes":[{"location_id":1,"party":"Зал","text":"Маг ушёл вниз.","event_seqs":[119]},{"location_id":2,"party":"Подвал","text":"Вор открыл дверь.","event_seqs":[117]}]}`
	f := &fakeLLM{responses: []string{first, second}}
	res, err := newService(f).Narrate(context.Background(), params(model.ModeBalanced))
	if err != nil {
		t.Fatal(err)
	}
	if res.Calls != 2 || res.Patched != 1 || len(res.Narration.Patched) != 1 || res.Narration.Patched[0] != 118 {
		t.Fatalf("result = %+v", res)
	}
	retry := f.requests[1].Input
	if !strings.Contains(retry, "seq=999") || !strings.Contains(retry, "seq=118") || !strings.Contains(retry, "seq=119") || !strings.Contains(retry, "нет блока для сцены location_id=1") {
		t.Errorf("retry input = %s", retry[len(retry)-600:])
	}
	cellar := res.Narration.Scenes[1]
	if !strings.HasSuffix(cellar.Text, "Кузнец взаимодействует: дверь, форсирует — провал.") || len(cellar.EventSeqs) != 2 {
		t.Errorf("patched scene = %+v", cellar)
	}
}

// TestNarrate_LeakAndFailures: раскрытое скрытое описание после дозапроса → ErrLeak; сбой вызова и ответ не по схеме —
// ошибка; контекст больше бюджета — ErrBudget без вызова.
func TestNarrate_LeakAndFailures(t *testing.T) {
	t.Parallel()
	leak := `{"chronicle":"Вор, на самом деле шпион Гильдии теней, открыл дверь.","scenes":[{"location_id":1,"party":"Зал","text":"Маг ушёл.","event_seqs":[119]},{"location_id":2,"party":"Подвал","text":"Кузнец устал.","event_seqs":[117,118]}]}`
	f := &fakeLLM{responses: []string{leak, leak}}
	if _, err := newService(f).Narrate(context.Background(), params(model.ModeBalanced)); !errors.Is(err, ErrLeak) || len(f.requests) != 2 {
		t.Fatalf("leak: %v, calls %d", err, len(f.requests))
	}
	if _, err := newService(&fakeLLM{err: errors.New("down")}).Narrate(context.Background(), params(model.ModeBalanced)); err == nil {
		t.Fatal("provider error swallowed")
	}
	if _, err := newService(&fakeLLM{responses: []string{`{"oops":1`}}).Narrate(context.Background(), params(model.ModeBalanced)); err == nil {
		t.Fatal("bad json swallowed")
	}
	tiny := newService(&fakeLLM{responses: []string{goodResponse}})
	tiny.MaxInputBytes = 10
	if _, err := tiny.Narrate(context.Background(), params(model.ModeBalanced)); !errors.Is(err, ErrBudget) {
		t.Fatalf("budget: %v", err)
	}
	if _, err := (&Service{Retries: 1}).Narrate(context.Background(), params(model.ModeBalanced)); !errors.Is(err, ErrNoLLM) {
		t.Fatalf("no llm: %v", err)
	}
}

// TestNarrate_NarrativeDigits: числа в режиме «повествование» — замечание и дозапрос, но не отказ.
func TestNarrate_NarrativeDigits(t *testing.T) {
	t.Parallel()
	withDigits := strings.Replace(goodResponse, "седьмой вечер", "7 вечер", 1)
	f := &fakeLLM{responses: []string{withDigits, goodResponse}}
	res, err := newService(f).Narrate(context.Background(), params(model.ModeNarrative))
	if err != nil || res.Calls != 2 || strings.Contains(res.Narration.Chronicle, "7") {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

// TestOptions: подходы видимых объектов с итоговой сложностью и шансами присутствующих; скрытый объект и выбывший
// персонаж не участвуют; текст по режиму.
func TestOptions(t *testing.T) {
	t.Parallel()
	st := tower()
	opts := Options(&st, def(), model.ModeBalanced)
	if len(opts) != 3 || opts[0].LocationID != 1 || opts[0].Title != "осмотреть статую" || len(opts[0].Chances) != 1 || opts[0].Chances[0].Nickname != "Маг" {
		t.Fatalf("options = %+v", opts)
	}
	door, pick := opts[1], opts[2]
	if door.Difficulty != 3 || door.Attribute != "Сила" || len(door.Chances) != 2 || door.Chances[0].Nickname != "Вор" {
		t.Errorf("door option = %+v", door)
	}
	// Вор: пул 4 + 3 против сложности 2; Кузнец: пул 2 − 1 (истощён) → почти без шансов.
	if pick.Difficulty != 2 || pick.Skill != "Воровство" || pick.Chances[0].Pct < 30 || pick.Chances[1].Pct != 0 {
		t.Errorf("pick option = %+v", pick)
	}
	text := OptionsText(opts[1:], model.ModeBalanced)
	if text != "Варианты:\n• выбить (Сила, сложность 3)\n• отпереть (Ловкость/воровство, сложность 2)" {
		t.Errorf("text = %q", text)
	}
	words := OptionsText(opts[1:], model.ModeNarrative)
	if strings.ContainsAny(words, "0123456789") || !strings.Contains(words, "• выбить (Сила, очень трудно)\n• отпереть (Ловкость/воровство, трудно)") {
		t.Errorf("narrative text = %q", words)
	}
	st.Locations[1].Objects[1].Revealed = true
	if o := Options(&st, def(), model.ModeBalanced); len(o) != 4 || o[3].Attribute != "" || !strings.Contains(OptionsText(o[3:], model.ModeBalanced), "• дёрнуть (без броска)") {
		t.Errorf("revealed options = %+v", o)
	}
	if OptionsText(nil, model.ModeBalanced) != "" {
		t.Error("empty options text")
	}
}
