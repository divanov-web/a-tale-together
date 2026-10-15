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
			{ID: 1, Name: "Зал башни", Description: "Пыльный зал.", X: 0, Y: 0, Stage: model.StageDetailed, Start: true,
				Objects: []model.LocationObject{{Key: "statue", Title: "статуя", Approaches: []model.Approach{{Action: model.ActionExplore, Title: "осмотреть статую", Attribute: "wits", Skill: "awareness", Difficulty: 5}}}}},
			{ID: 2, Name: "Подвал башни", Description: "Сырой подвал.", Difficulty: 1, X: 0, Y: -1, Stage: model.StageDetailed,
				Objects: []model.LocationObject{
					{Key: "cellar_door", Title: "дверь", State: "open", Approaches: []model.Approach{
						{Action: model.ActionInteract, Title: "выбить", Attribute: "strength", Skill: "athletics", Difficulty: 4},
						{Action: model.ActionInteract, Title: "отпереть", Attribute: "agility", Skill: "thievery", Difficulty: 4},
					}},
					{Key: "hidden_lever", Title: "потайной рычаг", HiddenUntil: true, Approaches: []model.Approach{{Action: model.ActionInteract, Title: "дёрнуть"}}},
				}},
		},
		Characters: []model.SnapshotCharacter{
			{ID: 11, Nickname: "Вор", Status: model.CharacterActive, LocationID: loc(2), Attributes: map[string]int{"strength": 2, "agility": 5}, Skills: map[string]int{"thievery": 3}, Conditions: []string{}},
			{ID: 12, Nickname: "Кузнец", Status: model.CharacterWaiting, LocationID: loc(2), Attributes: map[string]int{"strength": 5, "agility": 2}, Skills: map[string]int{"athletics": 3}, Conditions: []string{model.ConditionExhausted}},
			{ID: 13, Nickname: "Маг", Status: model.CharacterActive, LocationID: loc(1), Attributes: map[string]int{"wits": 5}, Skills: map[string]int{"awareness": 3}},
			{ID: 15, Nickname: "Призрак", Status: model.CharacterDead, LocationID: loc(1)},
		},
	}
}

func def() model.CharacterModelDef {
	return model.CharacterModelDef{
		Attributes: []model.AttributeDef{{Key: "strength", Title: "Сила"}, {Key: "agility", Title: "Ловкость"}, {Key: "wits", Title: "Разум"}},
		Skills:     []model.SkillDef{{Key: "athletics", Title: "Атлетика", Attribute: "strength"}, {Key: "thievery", Title: "Воровство", Attribute: "agility"}, {Key: "awareness", Title: "Внимательность", Attribute: "wits"}},
	}
}

func events() []model.Event {
	check := func(outcome string, pushed bool) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"character_id": 12, "object": "cellar_door", "title": "дверь", "free": false, "summary": "взаимодействует: дверь",
			"check": model.CheckInfo{Attribute: "strength", Skill: "athletics", Param: 7, Difficulty: 5, ChancePct: 70, Roll: 84, Outcome: outcome, Pushed: pushed, PushRoll: 91}})
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
 {"scene_id":1,"text":"Маг спускается в подвал.","event_seqs":[119]},
 {"scene_id":2,"text":"Кузнец бил в дверь, пока не выдохся — камень не поддался. Вор дождался щелчка: дверь приоткрылась. Маг сошёл по ступеням.","event_seqs":[117,118]}
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
	st := tower()
	st.Characters[0].Entry = model.EntryReturned
	if entry := BuildInput(Params{State: &st, Def: def()}); entry.Scenes[1].Characters[0].Entry != "returned" || !strings.Contains(entry.Encode(), `"entry":"returned"`) {
		t.Errorf("entry mark missing: %+v", entry.Scenes[0].Characters)
	}
	// B13: `leaving` в сцене остаётся с этим статусом, `inactive` исключается как `dead`.
	st = tower()
	st.Characters[1].Status = model.CharacterLeaving
	st.Characters[2].Status = model.CharacterInactive
	absent := BuildInput(Params{State: &st, Def: def()})
	if len(absent.Scenes) != 1 || absent.Scenes[0].LocationID != 2 || len(absent.Scenes[0].Characters) != 2 || absent.Scenes[0].Characters[1].Status != "leaving" || strings.Contains(absent.Encode(), "Маг") {
		t.Errorf("leaving/inactive scenes = %+v", absent.Scenes)
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
	if len(in.Approaches) != 3 || in.Approaches[1].Difficulty != 5 || in.Approaches[1].Attribute != "Сила" {
		t.Errorf("approaches = %+v", in.Approaches)
	}
	mech := BuildInput(params(model.ModeMechanics))
	if mech.Events[1].Numbers == nil || mech.Events[1].Numbers.Param != 7 || mech.Events[1].Numbers.Difficulty != 5 || mech.Events[1].Numbers.Roll != 84 || mech.Events[1].Numbers.ChancePct != 70 {
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

// TestBuildInput_PartyScenes (пункт 19): сцена на группу внутри локации, персонажи без группы одной локации — отдельная
// сцена без имени группы; группы раньше одиночек; лидер в контекст не попадает; события групп описаны словами
// без лидера; хроника с группами разложена по scene_id.
func TestBuildInput_PartyScenes(t *testing.T) {
	t.Parallel()
	st := tower()
	watch, free := model.PartyID(1), model.PartyID(2)
	st.Parties = []model.SnapshotParty{{ID: watch, Name: "Ночной дозор", LeaderID: char(11)}, {ID: free, Name: "Вольные клинки", LeaderID: char(13)}}
	st.Characters[0].PartyID, st.Characters[2].PartyID = &watch, &free
	st.Characters = append(st.Characters, model.SnapshotCharacter{ID: 16, Nickname: "Бродяга", Status: model.CharacterActive, LocationID: loc(2)})
	p := params(model.ModeBalanced)
	p.State = &st
	held := json.RawMessage(`{"party_id":1,"party":"Ночной дозор","reason":"party_limit","from":2,"from_name":"Подвал башни","to":1,"to_name":"Зал башни","held":[{"character_id":12,"nickname":"Кузнец","wanted":"Тайный ход"}]}`)
	split := json.RawMessage(`{"party_id":2,"party":"Вольные клинки","new_party_id":9,"new_party":"Странники","leader_id":16,"members":[16],"nicknames":["Бродяга"],"from":1,"from_name":"Зал башни","to":2,"to_name":"Подвал башни"}`)
	p.Events = append(p.Events,
		model.Event{Seq: 122, Type: model.EventPartyHeld, Visibility: model.VisibilityLocation, LocationID: loc(2), Payload: held},
		model.Event{Seq: 123, Type: model.EventPartySplit, Visibility: model.VisibilityLocation, LocationID: loc(1), Payload: split})
	in := BuildInput(p)
	if len(in.Scenes) != 3 || in.Scenes[0].SceneID != 1 || in.Scenes[0].Party != "Вольные клинки" || in.Scenes[0].LocationID != 1 ||
		in.Scenes[1].Party != "Ночной дозор" || len(in.Scenes[1].Characters) != 1 || in.Scenes[2].Party != "" || len(in.Scenes[2].Characters) != 2 {
		t.Fatalf("scenes = %+v", in.Scenes)
	}
	if in.Scenes[1].Label() != "Ночной дозор (Подвал башни)" || in.Scenes[2].Label() != "Подвал башни" {
		t.Errorf("labels = %q / %q", in.Scenes[1].Label(), in.Scenes[2].Label())
	}
	// Лидер и названия групп в контекст не попадают (решения руководителя 2026-10-11 и 2026-10-13).
	if enc := in.Encode(); strings.Contains(enc, "leader") || strings.Contains(enc, "Ночной дозор") || strings.Contains(enc, "Вольные клинки") || strings.Contains(enc, "Странники") {
		t.Error("leader or party name leaked into narrator input")
	}
	ev := in.Events[len(in.Events)-2]
	if ev.Actor != "группа" || ev.Summary != "группа держится вместе и уходит из «Подвал башни» в «Зал башни»; Кузнец хотел в «Тайный ход»" {
		t.Errorf("held event = %+v", ev)
	}
	if sp := in.Events[len(in.Events)-1]; sp.Summary != "Бродяга отделяется от группы и уходит своей дорогой из «Зал башни» в «Подвал башни»" {
		t.Errorf("split event = %+v", sp)
	}
	resp := `{"chronicle":"Ночь.","scenes":[{"scene_id":1,"text":"Маг ждёт.","event_seqs":[119,123]},{"scene_id":2,"text":"Вор открыл дверь, дозор ушёл наверх.","event_seqs":[117,122]},{"scene_id":3,"text":"Кузнец бил в дверь.","event_seqs":[118]}]}`
	res, err := newService(&fakeLLM{responses: []string{resp}}).Narrate(context.Background(), p)
	if err != nil || res.Calls != 1 || len(res.Narration.Scenes) != 3 || res.Narration.Scenes[1].Party != "Ночной дозор (Подвал башни)" || res.Narration.Scenes[1].LocationID != 2 {
		t.Fatalf("narration = %+v, %v", res.Narration, err)
	}
	// Пропущенное событие дописывается в первую сцену его локации; блок без ответа добавляется с подписью сцены.
	short := `{"chronicle":"Ночь.","scenes":[{"scene_id":1,"text":"Маг ждёт.","event_seqs":[119,123]},{"scene_id":2,"text":"Вор открыл дверь.","event_seqs":[117,122]}]}`
	res, err = newService(&fakeLLM{responses: []string{short, short}}).Narrate(context.Background(), p)
	if err != nil || res.Patched != 1 || len(res.Narration.Scenes) != 3 || res.Narration.Scenes[2].Party != "Подвал башни" || !strings.Contains(res.Narration.Scenes[1].Text, "Кузнец взаимодействует") {
		t.Fatalf("patched narration = %+v, %v", res.Narration, err)
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
	if len(res.Narration.Scenes) != 2 || res.Narration.Scenes[1].Party != "Подвал башни" || res.Narration.Scenes[1].LocationID != 2 || len(res.Narration.Scenes[1].EventSeqs) != 2 {
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
	first := `{"chronicle":"Тихо.","scenes":[{"scene_id":2,"text":"Вор открыл дверь.","event_seqs":[117,999]}]}`
	second := `{"chronicle":"Тихо.","scenes":[{"scene_id":1,"text":"Маг ушёл вниз.","event_seqs":[119]},{"scene_id":2,"text":"Вор открыл дверь.","event_seqs":[117]}]}`
	f := &fakeLLM{responses: []string{first, second}}
	res, err := newService(f).Narrate(context.Background(), params(model.ModeBalanced))
	if err != nil {
		t.Fatal(err)
	}
	if res.Calls != 2 || res.Patched != 1 || len(res.Narration.Patched) != 1 || res.Narration.Patched[0] != 118 {
		t.Fatalf("result = %+v", res)
	}
	retry := f.requests[1].Input
	if !strings.Contains(retry, "seq=999") || !strings.Contains(retry, "seq=118") || !strings.Contains(retry, "seq=119") || !strings.Contains(retry, "нет блока для сцены scene_id=1") {
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
	leak := `{"chronicle":"Вор, на самом деле шпион Гильдии теней, открыл дверь.","scenes":[{"scene_id":1,"text":"Маг ушёл.","event_seqs":[119]},{"scene_id":2,"text":"Кузнец устал.","event_seqs":[117,118]}]}`
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
	// Только подходы объектов: переходов по направлениям среди вариантов нет (B36, ADR-0025).
	if len(opts) != 3 || opts[0].LocationID != 1 || opts[0].Title != "осмотреть статую" || len(opts[0].Chances) != 1 || opts[0].Chances[0].Nickname != "Маг" {
		t.Fatalf("options = %+v", opts)
	}
	for _, o := range opts {
		if strings.HasPrefix(o.Title, "идти") || o.Object == "move" {
			t.Fatalf("move option = %+v", o)
		}
	}
	door, pick := opts[1], opts[2]
	if door.Difficulty != 5 || door.Attribute != "Сила" || door.Skill != "Атлетика" || len(door.Chances) != 2 || door.Chances[0].Nickname != "Вор" || door.Chances[0].Pct != 20 || door.Chances[1].Pct != 70 {
		t.Errorf("door option = %+v", door)
	}
	// Вор: параметр 5 + 3 против сложности 5 → 80 %; Кузнец: 2 − 1 (истощён) → 10 %.
	if pick.Difficulty != 5 || pick.Skill != "Воровство" || pick.Chances[0].Pct != 80 || pick.Chances[1].Pct != 10 {
		t.Errorf("pick option = %+v", pick)
	}
	text := OptionsText(opts[1:], model.ModeBalanced)
	if text != "Варианты:\n• выбить (Сила/атлетика, сложность 5)\n• отпереть (Ловкость/воровство, сложность 5)" {
		t.Errorf("text = %q", text)
	}
	words := OptionsText(opts[1:], model.ModeNarrative)
	if strings.ContainsAny(words, "0123456789") || !strings.Contains(words, "• выбить (Сила/атлетика, обычно)\n• отпереть (Ловкость/воровство, обычно)") {
		t.Errorf("narrative text = %q", words)
	}
	st.Locations[1].Objects[1].Revealed = true
	if o := Options(&st, def(), model.ModeBalanced); len(o) != 4 || o[3].Attribute != "" || !strings.Contains(OptionsText(o[3:], model.ModeBalanced), "• дёрнуть (без броска)") {
		t.Errorf("revealed options = %+v", o)
	}
	if OptionsText(nil, model.ModeBalanced) != "" {
		t.Error("empty options text")
	}
	// B8: исчерпанные подходы не предлагаются — «отпереть» ведёт к состоянию open, дверь уже открыта;
	// «осмотреть статую» даёт факт, который Маг уже знает.
	st.Locations[1].Objects[0].Approaches[1].OnSuccess = json.RawMessage(`{"state":"open"}`)
	st.Characters[2].Knowledge = []string{"статуя: ничего примечательного"}
	o := Options(&st, def(), model.ModeBalanced)
	if len(o) != 2 || o[0].Title != "выбить" || o[1].Title != "дёрнуть" {
		t.Errorf("exhausted options = %+v", o)
	}
}

// TestBuildInput_HiddenExit (ADR-0025): скрытый сосед уходит модели как `hidden` с видом издалека, без имени и
// карточки; видимая карточка — с именем и карточкой.
func TestBuildInput_HiddenExit(t *testing.T) {
	t.Parallel()
	st := tower()
	st.Locations = append(st.Locations,
		model.SnapshotLocation{ID: 3, Name: "Деревня", Description: "Десяток дворов.", X: 1, Y: 0, Stage: model.StageCard, Hidden: true, DistantView: "дым над крышами", Objects: []model.LocationObject{}},
		model.SnapshotLocation{ID: 4, Name: "Опушка", Description: "Кромка леса.", X: -1, Y: 0, Stage: model.StageCard, Objects: []model.LocationObject{}},
	)
	in := BuildInput(Params{State: &st, Def: def()})
	hall := in.Scenes[0]
	if len(hall.Exits) != 3 {
		t.Fatalf("exits = %+v", hall.Exits)
	}
	var east, west sceneExit
	for _, e := range hall.Exits {
		switch e.Direction {
		case model.East:
			east = e
		case model.West:
			west = e
		}
	}
	if !east.Hidden || east.DistantView != "дым над крышами" || east.Name != "" || east.Card != "" {
		t.Fatalf("hidden exit = %+v", east)
	}
	if west.Hidden || west.Name != "Опушка" || west.Card != "Кромка леса." {
		t.Fatalf("visible card exit = %+v", west)
	}
	if enc := in.Encode(); strings.Contains(enc, "Деревня") || strings.Contains(enc, "Десяток дворов") {
		t.Fatalf("hidden neighbor leaked: %s", enc)
	}
}

// TestBuildInput_Speeches — S21 (пункт 32, ADR-0027): реплика попадает только в сцену присутствия персонажа после
// свёртки; реплики выбывших (`dead`, `inactive`) и пустые отбрасываются; у сцены без реплик пустой список.
func TestBuildInput_Speeches(t *testing.T) {
	t.Parallel()
	p := params(model.ModeBalanced)
	p.Speeches = []model.SnapshotSpeech{
		{SpeechID: 1, CharacterID: 11, Text: "Тише, свои! Не стреляйте."},
		{SpeechID: 2, CharacterID: 15, Text: "Бу!"},
		{SpeechID: 3, CharacterID: 13, Text: "  "},
		{SpeechID: 4, CharacterID: 12, Text: "Дверь не поддаётся."},
	}
	in := BuildInput(p)
	hall, cellar := in.Scenes[0], in.Scenes[1]
	if len(hall.Speeches) != 0 || hall.Speeches == nil {
		t.Fatalf("hall speeches = %#v; want empty list", hall.Speeches)
	}
	if len(cellar.Speeches) != 2 || cellar.Speeches[0].Nickname != "Вор" || cellar.Speeches[0].Text != "Тише, свои! Не стреляйте." || cellar.Speeches[1].Nickname != "Кузнец" {
		t.Fatalf("cellar speeches = %+v", cellar.Speeches)
	}
	enc := in.Encode()
	if strings.Contains(enc, "Бу!") || strings.Count(enc, "Не стреляйте") != 1 || !strings.Contains(enc, `"speeches":[]`) {
		t.Errorf("encoded input = %s", enc)
	}
	// Без реплик во входе у каждой сцены пустой список, не null.
	if plain := BuildInput(params(model.ModeBalanced)).Encode(); strings.Contains(plain, `"speeches":null`) {
		t.Errorf("null speeches: %s", plain)
	}
}

// TestBuildInput_Lair (S22, ADR-0031): логово во входе — фракция и вид издалека без имени, пока клетка скрыта; у каждой
// сцены направление и дальность логова словами; открытая клетка передаёт имя; без логова полей нет.
func TestBuildInput_Lair(t *testing.T) {
	t.Parallel()
	p := params(model.ModeBalanced)
	p.Lair = &LairInfo{Faction: "Гильдия", At: model.Coord{X: 3, Y: -3}, Hidden: true, Name: "Чёрная мельница", DistantView: "крыло мельницы над холмами"}
	in := BuildInput(p)
	if in.World.Lair == nil || in.World.Lair.Faction != "Гильдия" || in.World.Lair.DistantView != "крыло мельницы над холмами" || in.World.Lair.Name != "" {
		t.Fatalf("world lair = %+v", in.World.Lair)
	}
	if hall, cellar := in.Scenes[0], in.Scenes[1]; hall.Lair != "недалеко на юго-востоке" || cellar.Lair != "недалеко на юго-востоке" {
		t.Fatalf("scene lair = %q, %q", hall.Lair, cellar.Lair)
	}
	if enc := in.Encode(); strings.Contains(enc, "Чёрная мельница") || !strings.Contains(enc, `"lair":{"faction":"Гильдия","distant_view":"крыло мельницы над холмами"}`) {
		t.Fatalf("encoded = %s", enc)
	}
	p.Lair.Hidden = false
	if in = BuildInput(p); in.World.Lair.Name != "Чёрная мельница" {
		t.Fatalf("open lair = %+v", in.World.Lair)
	}
	if enc := BuildInput(params(model.ModeBalanced)).Encode(); strings.Contains(enc, `"lair"`) {
		t.Fatalf("lair without faction lair: %s", enc)
	}
}
