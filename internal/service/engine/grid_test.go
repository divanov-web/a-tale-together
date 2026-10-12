package engine

import (
	"strings"
	"testing"

	"taletogether/internal/model"
)

// grid — снимок сценария S16 (docs/game/scenarios.md): стартовая площадь `(0,0)`, карточки соседей «Лес» на севере
// и «Дорога» на востоке; группа «Дозор» (Вор и Кузнец, фракция 1) и группа «Тени» (Маг и Паладин, фракция 2) на
// площади; резерв идентификаторов для разделения групп.
func grid() model.RoundSnapshot {
	loc := func(id model.LocationID) *model.LocationID { return &id }
	watch, shadows := model.PartyID(1), model.PartyID(2)
	return model.RoundSnapshot{
		SchemaVersion: model.SnapshotSchemaVersion, Mode: model.ModeBalanced, MaxParties: 4, NewPartyIDs: []model.PartyID{7, 8}, PartyNames: []string{"Дозор", "Тени", "Странники", "Искатели"},
		Locations: []model.SnapshotLocation{
			{ID: 1, Name: "Площадь", Description: "Сердце города.", X: 0, Y: 0, Stage: model.StageDetailed, Start: true, Visited: true, Objects: []model.LocationObject{}},
			{ID: 2, Name: "Лес", Description: "Тёмная кромка леса.", X: 0, Y: 1, Stage: model.StageCard, Objects: []model.LocationObject{}},
			{ID: 3, Name: "Дорога", Description: "Пыльный тракт.", X: 1, Y: 0, Stage: model.StageCard, Objects: []model.LocationObject{}},
		},
		Characters: []model.SnapshotCharacter{
			{ID: 11, Nickname: "Вор", Status: model.CharacterActive, FactionID: 1, PartyID: &watch, LocationID: loc(1), Conditions: []string{}},
			{ID: 12, Nickname: "Кузнец", Status: model.CharacterActive, FactionID: 1, PartyID: &watch, LocationID: loc(1), Conditions: []string{}},
			{ID: 13, Nickname: "Маг", Status: model.CharacterActive, FactionID: 2, PartyID: &shadows, LocationID: loc(1), Conditions: []string{}},
			{ID: 14, Nickname: "Паладин", Status: model.CharacterActive, FactionID: 2, PartyID: &shadows, LocationID: loc(1), Conditions: []string{}},
		},
		Parties: []model.SnapshotParty{{ID: watch, Name: "Дозор"}, {ID: shadows, Name: "Тени"}},
	}
}

// TestScenario_S16_GridMove: переход по направлению словом, по имени соседа и по подписи кнопки; несуществующее
// направление отклоняется с перечнем выходов по сторонам света; событие перехода несёт координаты.
func TestScenario_S16_GridMove(t *testing.T) {
	t.Parallel()
	res := Resolve(grid(), []model.Intent{
		{SubmissionID: 1, CharacterID: 11, ActionType: model.ActionMove, Target: "north"},
		{SubmissionID: 2, CharacterID: 12, ActionType: model.ActionMove, Target: "идти на север: Лес"},
		{SubmissionID: 3, CharacterID: 13, ActionType: model.ActionMove, Target: "лес"},
		{SubmissionID: 4, CharacterID: 14, ActionType: model.ActionMove, Target: "вверх"},
	}, 3)
	for i := 0; i < 3; i++ {
		if r := res.Results[i]; r.Status != model.ResultResolved || r.Summary != "переходит: Площадь → Лес" {
			t.Fatalf("result %d = %+v", i, r)
		}
	}
	if r := res.Results[3]; r.Status != model.ResultRejected || r.Reason != "из «Площадь» нет выхода «вверх»; выходы: на север: Лес, на восток: Дорога" {
		t.Fatalf("rejected = %+v", r)
	}
	moved := eventsOf(res, model.EventCharacterMoved)
	if len(moved) != 6 || payload(moved[0])["x"] != 0.0 || payload(moved[0])["y"] != 1.0 || payload(moved[0])["to_name"] != "Лес" {
		t.Fatalf("moved = %+v", moved)
	}
}

// TestScenario_S16_Discovery: первый вход персонажа фракции в клетку — приватное `cell_discovered`; две группы
// одной фракции в одной клетке дают одно открытие, другая фракция — своё; стартовая клетка открытием не считается;
// свёртка событий даёт те же открывшие фракции, возврат в известную клетку открытия не добавляет.
func TestScenario_S16_Discovery(t *testing.T) {
	t.Parallel()
	snap := grid()
	res := Resolve(snap, []model.Intent{
		{SubmissionID: 1, CharacterID: 11, ActionType: model.ActionMove, Target: "north"},
		{SubmissionID: 2, CharacterID: 12, ActionType: model.ActionMove, Target: "north"},
		{SubmissionID: 3, CharacterID: 13, ActionType: model.ActionMove, Target: "north"},
	}, 3)
	found := eventsOf(res, model.EventCellDiscovered)
	if len(found) != 2 || found[0].Visibility != model.VisibilityPrivate || len(found[0].VisibleTo) != 1 || found[0].VisibleTo[0] != 11 {
		t.Fatalf("discoveries = %+v", found)
	}
	if payload(found[0])["faction_id"] != 1.0 || payload(found[1])["faction_id"] != 2.0 || payload(found[1])["location_id"] != 2.0 || payload(found[1])["name"] != "Лес" {
		t.Fatalf("discovery payloads = %v / %v", payload(found[0]), payload(found[1]))
	}
	after := grid()
	if err := Apply(&after, res.Events); err != nil {
		t.Fatal(err)
	}
	forest, _ := after.Location(2)
	if !forest.Visited || len(forest.DiscoveredBy) != 2 || forest.DiscoveredBy[0] != 1 || forest.DiscoveredBy[1] != 2 {
		t.Fatalf("forest after apply = %+v", forest)
	}
	// Возврат на стартовую площадь и повторный вход в лес: открытий больше нет.
	back := Resolve(after, []model.Intent{
		{SubmissionID: 4, CharacterID: 11, ActionType: model.ActionMove, Target: "south"},
		{SubmissionID: 5, CharacterID: 14, ActionType: model.ActionMove, Target: "север"},
	}, 4)
	if n := len(eventsOf(back, model.EventCellDiscovered)); n != 0 {
		t.Fatalf("repeat discoveries = %d: %+v", n, back.Events)
	}
	if r := back.Results[1]; r.Status != model.ResultResolved || !strings.Contains(r.Summary, "→ Лес") {
		t.Fatalf("paladin = %+v", r)
	}
}

// TestApply_WorldPackage: `cell_generated` детализирует известную клетку и добавляет новую карточку, `object_created`
// добавляет объект один раз; состояние после пакета мира воспроизводимо свёрткой.
func TestApply_WorldPackage(t *testing.T) {
	t.Parallel()
	snap := grid()
	events := []model.Event{
		{Seq: 1, Type: model.EventCellGenerated, Payload: []byte(`{"location_id":2,"x":0,"y":1,"name":"Еловый бор","description":"Густой ельник.","difficulty":1,"terrain":"forest","stage":"detailed"}`)},
		{Seq: 2, Type: model.EventObjectCreated, Payload: []byte(`{"location_id":2,"object":{"key":"hut","title":"хижина","state":"closed","approaches":[{"action":"interact","title":"открыть","attribute":"strength","difficulty":5,"on_success":{"state":"open"}}]}}`)},
		{Seq: 3, Type: model.EventObjectCreated, Payload: []byte(`{"location_id":2,"object":{"key":"hut","title":"хижина"}}`)},
		{Seq: 4, Type: model.EventCellGenerated, Payload: []byte(`{"location_id":9,"x":0,"y":2,"name":"Болото","description":"Туман над осокой.","terrain":"swamp","stage":"card"}`)},
	}
	if err := Apply(&snap, events); err != nil {
		t.Fatal(err)
	}
	forest, _ := snap.Location(2)
	if forest.Stage != model.StageDetailed || forest.Name != "Еловый бор" || forest.Difficulty != 1 || len(forest.Objects) != 1 || forest.Objects[0].Approaches[0].Difficulty != 5 {
		t.Fatalf("forest = %+v", forest)
	}
	swamp, ok := snap.LocationAt(0, 2)
	if !ok || swamp.ID != 9 || swamp.Stage != model.StageCard || swamp.Description != "Туман над осокой." {
		t.Fatalf("swamp = %+v, %v", swamp, ok)
	}
	if exits := snap.Exits(forest); len(exits) != 2 || exits[0].Direction != model.North || exits[0].To.ID != 9 || exits[1].Direction != model.South {
		t.Fatalf("forest exits = %+v", exits)
	}
}
