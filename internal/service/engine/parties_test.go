package engine

import (
	"encoding/json"
	"reflect"
	"testing"

	"taletogether/internal/model"
)

// square — снимок сценария S14: группа «Ночной дозор» (Вор, Кузнец, Паладин) на площади с выходами в коридор и на рынок.
func square(parties int) model.RoundSnapshot {
	loc := func(id model.LocationID) *model.LocationID { return &id }
	watch := model.PartyID(1)
	snap := model.RoundSnapshot{
		SchemaVersion: model.SnapshotSchemaVersion, Mode: model.ModeBalanced, MaxParties: 3, NewPartyIDs: []model.PartyID{7, 8}, PartyNames: []string{"Ночной дозор", "Вольные клинки", "Странники"},
		Locations: []model.SnapshotLocation{
			{ID: 1, Name: "Площадь", Links: []model.LocationLink{{To: "Коридор", Title: "в коридор"}, {To: "Рынок", Title: "на рынок"}}},
			{ID: 2, Name: "Коридор", Links: []model.LocationLink{{To: "Площадь", Title: "на площадь"}}},
			{ID: 3, Name: "Рынок", Links: []model.LocationLink{{To: "Площадь", Title: "на площадь"}}},
		},
		Characters: []model.SnapshotCharacter{
			{ID: 11, Nickname: "Вор", Status: model.CharacterActive, LocationID: loc(1), PartyID: &watch, Conditions: []string{}, Attributes: map[string]int{"will": 3}},
			{ID: 12, Nickname: "Кузнец", Status: model.CharacterActive, LocationID: loc(1), PartyID: &watch, Conditions: []string{}, Attributes: map[string]int{"will": 5}},
			{ID: 14, Nickname: "Паладин", Status: model.CharacterActive, LocationID: loc(1), PartyID: &watch, Conditions: []string{}, Attributes: map[string]int{"will": 4}},
		},
		Parties: []model.SnapshotParty{{ID: watch, Name: "Ночной дозор", LeaderID: ptrChar(11)}},
	}
	for i := 2; i <= parties; i++ {
		snap.Parties = append(snap.Parties, model.SnapshotParty{ID: model.PartyID(i), Name: snap.PartyNames[i-1]})
	}
	return snap
}

func ptrChar(id model.CharacterID) *model.CharacterID { return &id }

func s14Intents() []model.Intent {
	return []model.Intent{
		{SubmissionID: 1, CharacterID: 11, ActionType: model.ActionMove, Target: "коридор"},
		{SubmissionID: 2, CharacterID: 12, ActionType: model.ActionMove, Target: "коридор"},
		{SubmissionID: 3, CharacterID: 14, ActionType: model.ActionMove, Target: "рынок"},
	}
}

func eventsOf(res Result, t model.EventType) []model.Event {
	var out []model.Event
	for _, ev := range res.Events {
		if ev.Type == t {
			out = append(out, ev)
		}
	}
	return out
}

func payload(ev model.Event) map[string]any {
	var p map[string]any
	_ = json.Unmarshal(ev.Payload, &p)
	return p
}

// TestScenario_S14_PartySplit: при двух активных группах расхождение даёт новую группу из отделившегося Паладина
// с зарезервированным id и именем из шаблона; оба перемещения выполнены; свёртка воспроизводит состояние.
func TestScenario_S14_PartySplit(t *testing.T) {
	t.Parallel()
	snap := square(2)
	res := Resolve(snap, s14Intents(), 1)
	for _, r := range res.Results {
		if r.Status != model.ResultResolved || r.Outcome == model.OutcomeHeld {
			t.Fatalf("result = %+v", r)
		}
	}
	splits := eventsOf(res, model.EventPartySplit)
	if len(splits) != 1 || splits[0].Visibility != model.VisibilityLocation || *splits[0].LocationID != 1 {
		t.Fatalf("split events = %+v", splits)
	}
	p := payload(splits[0])
	if p["new_party_id"] != float64(7) || p["new_party"] != "Странники" || p["leader_id"] != float64(14) || p["to_name"] != "Рынок" || p["party"] != "Ночной дозор" {
		t.Fatalf("split payload = %v", p)
	}
	if len(eventsOf(res, model.EventPartyHeld)) != 0 {
		t.Fatal("unexpected party_held")
	}
	after := square(2)
	if err := Apply(&after, res.Events); err != nil {
		t.Fatal(err)
	}
	pal, _ := after.Character(14)
	thief, _ := after.Character(11)
	if pal.PartyID == nil || *pal.PartyID != 7 || *pal.LocationID != 3 || *thief.PartyID != 1 || *thief.LocationID != 2 {
		t.Fatalf("after apply: paladin %+v, thief %+v", pal, thief)
	}
	if len(after.Parties) != 3 || after.Parties[2].Name != "Странники" || *after.Parties[2].LeaderID != 14 {
		t.Fatalf("parties after apply = %+v", after.Parties)
	}
	// Повтор с тем же seed даёт те же события.
	again := Resolve(square(2), s14Intents(), 1)
	if !reflect.DeepEqual(again.Events, res.Events) {
		t.Fatal("replay differs")
	}
}

// TestScenario_S14_PartyHeld: при трёх активных группах меньшинство удерживается: группа целиком идёт за лидером
// в коридор, заявка Паладина получает исход «удержан» с объяснением, событие `party_held` с причиной `party_limit`.
func TestScenario_S14_PartyHeld(t *testing.T) {
	t.Parallel()
	res := Resolve(square(3), s14Intents(), 1)
	by := map[model.SubmissionID]model.ActionResult{}
	for _, r := range res.Results {
		by[r.SubmissionID] = r
	}
	if by[3].Status != model.ResultResolved || by[3].Outcome != model.OutcomeHeld || by[3].Summary != "остаётся с группой «Ночной дозор»: группа идёт в «Коридор», а не в «Рынок»" {
		t.Fatalf("paladin result = %+v", by[3])
	}
	if by[1].Outcome == model.OutcomeHeld || by[2].Outcome == model.OutcomeHeld {
		t.Fatalf("majority held: %+v", res.Results)
	}
	held := eventsOf(res, model.EventPartyHeld)
	if len(held) != 1 || *held[0].LocationID != 1 {
		t.Fatalf("held events = %+v", held)
	}
	p := payload(held[0])
	if p["reason"] != model.HeldPartyLimit || p["to_name"] != "Коридор" || p["party"] != "Ночной дозор" {
		t.Fatalf("held payload = %v", p)
	}
	if len(eventsOf(res, model.EventPartySplit)) != 0 {
		t.Fatal("split under limit")
	}
	after := square(3)
	if err := Apply(&after, res.Events); err != nil {
		t.Fatal(err)
	}
	for _, id := range []model.CharacterID{11, 12, 14} {
		c, _ := after.Character(id)
		if *c.LocationID != 2 || *c.PartyID != 1 {
			t.Fatalf("character %d after hold = %+v", id, c)
		}
	}
	if len(after.Parties) != 3 {
		t.Fatalf("parties = %+v", after.Parties)
	}
}

// TestScenario_S14_LeaderDirection: лидер уходит один, лимит достигнут — вся группа идёт за ним, включая участника
// без заявки на переход; заявка на другое направление удержана. Лидер без заявки — группа остаётся на месте.
func TestScenario_S14_LeaderDirection(t *testing.T) {
	t.Parallel()
	res := Resolve(square(3), []model.Intent{
		{SubmissionID: 1, CharacterID: 11, ActionType: model.ActionMove, Target: "рынок"},
		{SubmissionID: 2, CharacterID: 12, ActionType: model.ActionMove, Target: "коридор"},
		{SubmissionID: 4, CharacterID: 14, ActionType: model.ActionExplore},
	}, 2)
	by := map[model.SubmissionID]model.ActionResult{}
	for _, r := range res.Results {
		by[r.SubmissionID] = r
	}
	if by[1].Outcome == model.OutcomeHeld || by[2].Outcome != model.OutcomeHeld {
		t.Fatalf("results = %+v", res.Results)
	}
	after := square(3)
	if err := Apply(&after, res.Events); err != nil {
		t.Fatal(err)
	}
	for _, id := range []model.CharacterID{11, 12, 14} {
		c, _ := after.Character(id)
		if *c.LocationID != 3 {
			t.Fatalf("character %d not with leader: %+v", id, c)
		}
	}
	// Осмотр Паладина разрешён уже на рынке: группа ушла до фазы взаимодействия.
	if by[4].Status != model.ResultResolved || by[4].Summary != "осматривается в локации «Рынок»" {
		t.Fatalf("paladin explore = %+v", by[4])
	}
	held := payload(eventsOf(res, model.EventPartyHeld)[0])
	if list, _ := held["held"].([]any); len(list) != 2 || held["to_name"] != "Рынок" {
		t.Fatalf("held payload = %v", held)
	}

	// Лидер не двигался: группа остаётся, заявки на переход удержаны на месте.
	stay := Resolve(square(3), []model.Intent{
		{SubmissionID: 2, CharacterID: 12, ActionType: model.ActionMove, Target: "коридор"},
		{SubmissionID: 3, CharacterID: 14, ActionType: model.ActionMove, Target: "рынок"},
	}, 2)
	if len(eventsOf(stay, model.EventCharacterMoved)) != 0 || stay.Results[0].Summary != "остаётся с группой «Ночной дозор» в «Площадь»: в «Коридор» не ушёл" {
		t.Fatalf("stay = %+v", stay.Results)
	}
	if p := payload(eventsOf(stay, model.EventPartyHeld)[0]); p["to_name"] != nil {
		t.Fatalf("stay payload = %v", p)
	}
}

// TestScenario_S14_LocationLimit: лимит занятых локаций удерживает разделение с причиной `location_limit`
// (одиночка остаётся на площади, группа заняла бы третью локацию); персонаж без группы перемещается свободно.
func TestScenario_S14_LocationLimit(t *testing.T) {
	t.Parallel()
	snap := square(1)
	snap.MaxActiveLocations = 2
	snap.Characters = append(snap.Characters, model.SnapshotCharacter{ID: 15, Nickname: "Бродяга", Status: model.CharacterActive, LocationID: &snap.Locations[0].ID, Conditions: []string{}})
	res := Resolve(snap, s14Intents(), 3)
	held := eventsOf(res, model.EventPartyHeld)
	if len(held) != 1 || payload(held[0])["reason"] != model.HeldLocationLimit {
		t.Fatalf("held = %+v", held)
	}
	after := Clone(snap)
	if err := Apply(&after, res.Events); err != nil {
		t.Fatal(err)
	}
	loner, _ := after.Character(15)
	pal, _ := after.Character(14)
	if *loner.LocationID != 1 || *pal.LocationID != 2 || pal.PartyID == nil || len(after.Parties) != 1 {
		t.Fatalf("loner %+v, paladin %+v", loner, pal)
	}
	// Одиночка идёт на рынок без событий групп; без лимита локаций разделение проходит.
	solo := Resolve(snap, []model.Intent{{SubmissionID: 5, CharacterID: 15, ActionType: model.ActionMove, Target: "рынок"}}, 3)
	if len(solo.Events) != 2 || solo.Events[0].Type != model.EventCharacterMoved || len(eventsOf(solo, model.EventPartyHeld)) != 0 {
		t.Fatalf("solo move = %+v", solo.Events)
	}
	snap.MaxActiveLocations = 0
	if free := Resolve(snap, s14Intents(), 3); len(eventsOf(free, model.EventPartySplit)) != 1 {
		t.Fatalf("no split without location limit: %+v", free.Events)
	}
}
