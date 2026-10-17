package narrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"taletogether/internal/model"
)

// gridState — состояние после хода на север: группа «Дозор» вошла в карточку «Лес» `(0,1)`, сосед `(0,0)` известен.
func gridState() model.RoundSnapshot {
	watch := model.PartyID(1)
	return model.RoundSnapshot{
		SchemaVersion: model.SnapshotSchemaVersion, Mode: model.ModeBalanced,
		Locations: []model.SnapshotLocation{
			{ID: 1, Name: "Площадь", Description: "Сердце города.", X: 0, Y: 0, Stage: model.StageDetailed, Start: true, Objects: []model.LocationObject{}},
			{ID: 2, Name: "Лес", Description: "Тёмная кромка леса.", X: 0, Y: 1, Stage: model.StageCard, Terrain: "forest", Objects: []model.LocationObject{}},
		},
		Characters: []model.SnapshotCharacter{{ID: 11, Nickname: "Вор", Status: model.CharacterActive, PartyID: &watch, LocationID: loc(2), Conditions: []string{}, Attributes: map[string]int{"wits": 4}}},
		Parties:    []model.SnapshotParty{{ID: watch, Name: "Дозор"}},
	}
}

// TestPlanTasks: занятая карточка — задание на клетку, недостающие соседи занятых клеток — задания на карточки
// (известные в мире не заказываются); сверх лимита клеток — Overflow; без персонажей заданий нет.
func TestPlanTasks(t *testing.T) {
	t.Parallel()
	st := gridState()
	known := map[[2]int]bool{{0, 0}: true, {0, 1}: true, {1, 1}: true} // восточный сосед леса уже есть в мире вне снимка
	tasks := PlanTasks(&st, func(x, y int) bool { return known[[2]int{x, y}] })
	if len(tasks.Cells) != 1 || tasks.Cells[0].LocationID != 2 || tasks.Cells[0].Card != "Тёмная кромка леса." || tasks.Cells[0].Parties[0] != "Вор" {
		t.Fatalf("cells = %+v", tasks.Cells)
	}
	if len(tasks.Neighbors) != 2 || tasks.Neighbors[0].Direction != model.North || tasks.Neighbors[0].Y != 2 || tasks.Neighbors[1].Direction != model.West || tasks.Neighbors[1].OfName != "Лес" {
		t.Fatalf("neighbors = %+v", tasks.Neighbors)
	}
	if PlanTasks(&model.RoundSnapshot{}, func(int, int) bool { return false }).Empty() != true {
		t.Error("empty state must give no tasks")
	}
	many := gridState()
	for i := 0; i < model.MaxCellsPerRound+1; i++ {
		id := model.LocationID(100 + i)
		many.Locations = append(many.Locations, model.SnapshotLocation{ID: id, Name: "Клетка", X: 10 + i, Y: 10, Stage: model.StageCard})
		many.Characters = append(many.Characters, model.SnapshotCharacter{ID: model.CharacterID(200 + i), Nickname: "N", Status: model.CharacterActive, LocationID: loc(id)})
	}
	over := PlanTasks(&many, func(int, int) bool { return true })
	if len(over.Cells) != model.MaxCellsPerRound || len(over.Overflow) != 2 {
		t.Fatalf("overflow = %d cells, %d overflow", len(over.Cells), len(over.Overflow))
	}
}

const worldResponse = `{"chronicle":"Лес встречает тишиной.","scenes":[{"scene_id":1,"text":"Вор входит под ели.","event_seqs":[]}],
 "world":{"cells":[
  {"location_id":2,"name":"Еловый бор","description":"Густой ельник, тропа теряется в хвое.","difficulty":1,"objects":[
    {"key":"Old Hut","title":"заброшенная хижина","description":"Дверь перекошена.","state":"closed","hidden":false,"approaches":[
      {"action":"interact","title":"открыть дверь","attribute":"strength","skill":"athletics","difficulty":5,"on_success":{"state":"open","reveal":null,"fact":null},"on_fail":{"condition":"exhausted","damage":null,"fact":null}},
      {"action":"explore","title":"осмотреть следы","attribute":"wits","skill":"awareness","difficulty":5,"on_success":{"state":null,"reveal":"cache","fact":"Следы ведут к тайнику."},"on_fail":null},
      {"action":"interact","title":"взлететь","attribute":"magic","skill":null,"difficulty":5,"on_success":{"state":null,"reveal":null,"fact":"x"},"on_fail":null}]},
    {"key":"cache","title":"тайник под корнями","description":"","state":"","hidden":true,"approaches":[{"action":"interact","title":"вскрыть","attribute":null,"skill":null,"difficulty":0,"on_success":{"state":"open","reveal":null,"fact":"Внутри печать Ордена."},"on_fail":null}]},
    {"key":"extra","title":"лишний объект","description":"","state":"","hidden":false,"approaches":[]}]},
  {"location_id":77,"name":"Чужая клетка","description":"Не из задания.","difficulty":0,"objects":[]}],
  "neighbors":[{"x":0,"y":2,"name":"Болото","terrain":"swamp","card":"Туман над осокой.","visible":false,"distant_view":"Туман стелется низко."},{"x":5,"y":5,"name":"Лишний","terrain":"road","card":"Не из задания.","visible":true,"distant_view":""}]}}`

// TestNarrate_WorldPackage: пакет мира проверяется по заданиям: клетка из задания принята (ключи в snake_case, объекты
// сверх лимита и подход с неизвестной характеристикой отброшены, reveal указывает на скрытый объект), чужие клетка и
// сосед отброшены, пропущенные соседи после одного дозапроса (B45: невыполненное задание — замечание модели, клетка
// сверх лимита заданием не была) и клетка сверх лимита закрыты шаблоном; замечания к качеству пакета дозапроса не
// вызывают; вход содержит задания, лимиты и ключи модели персонажа.
func TestNarrate_WorldPackage(t *testing.T) {
	t.Parallel()
	st := gridState()
	tasks := PlanTasks(&st, func(x, y int) bool { return x == 0 && y == 0 })
	tasks.Overflow = []CellTask{{LocationID: 3, X: 1, Y: 0, Name: "Дорога", Card: "Пыльный тракт."}}
	f := &fakeLLM{responses: []string{worldResponse}}
	svc := newService(f)
	res, err := svc.Narrate(context.Background(), Params{Title: "Мир", Mode: model.ModeBalanced, RoundNo: 2, State: &st, Def: def(), Tasks: tasks})
	if err != nil {
		t.Fatal(err)
	}
	if res.Calls != 2 || res.Patched != 0 || res.Narration.Chronicle != "Лес встречает тишиной." {
		t.Fatalf("result = %+v", res)
	}
	if retry := f.requests[1].Input; !strings.Contains(retry, "сосед (-1, 1) на запад от «Лес» без карточки") || strings.Contains(retry, "location_id=3") || strings.Contains(retry, "объектов больше 2") {
		t.Fatalf("retry input = %s", retry[len(retry)-500:])
	}
	in := f.requests[0].Input
	for _, want := range []string{`"generate":{"cells":[{"location_id":2`, `"neighbors":[{"x":0,"y":2,"direction":"north"`, `"limits":{"objects_per_cell":2`, `"character_model":{"attributes":[{"key":"strength"`, `"stage":"card"`} {
		if !strings.Contains(in, want) {
			t.Fatalf("input without %s: %s", want, in)
		}
	}
	w := res.World
	if len(w.Cells) != 2 || w.Cells[0].LocationID != 2 || w.Cells[0].Name != "Еловый бор" || w.Cells[0].Difficulty != 1 || w.Cells[0].Template || w.Cells[0].Terrain != "forest" {
		t.Fatalf("cells = %+v", w.Cells)
	}
	if road := w.Cells[1]; !road.Template || road.LocationID != 3 || road.Name != "Дорога" || road.Description != model.TemplateCellDesc {
		t.Fatalf("overflow cell = %+v", road)
	}
	objs := w.Cells[0].Objects
	if len(objs) != 2 || objs[0].Key != "old_hut" || objs[0].State != "closed" || !objs[1].HiddenUntil || objs[1].Key != "cache" {
		t.Fatalf("objects = %+v", objs)
	}
	if ap := objs[0].Approaches; len(ap) != 2 || ap[0].Attribute != "strength" || ap[0].Skill != "athletics" || string(ap[0].OnSuccess) != `{"state":"open"}` || string(ap[0].OnFail) != `{"condition":"exhausted"}` ||
		string(ap[1].OnSuccess) != `{"fact":"Следы ведут к тайнику.","reveal":"cache"}` || len(ap[1].OnFail) != 0 {
		t.Fatalf("approaches = %+v", ap)
	}
	if ap := objs[1].Approaches; len(ap) != 1 || ap[0].Attribute != "" || ap[0].Difficulty != 0 || string(ap[0].OnSuccess) != `{"fact":"Внутри печать Ордена.","state":"open"}` {
		t.Fatalf("hidden approaches = %+v", ap)
	}
	if len(w.Neighbors) != 3 || w.Neighbors[0].Name != "Болото" || w.Neighbors[0].Terrain != "swamp" || w.Neighbors[0].Template || !w.Neighbors[1].Template || w.Neighbors[1].X != -1 || !w.Neighbors[2].Template {
		t.Fatalf("neighbors = %+v", w.Neighbors)
	}
	// ADR-0025: скрытость и вид издалека из карточки; шаблонный сосед виден.
	if !w.Neighbors[0].Hidden || w.Neighbors[0].DistantView != "Туман стелется низко." || w.Neighbors[1].Hidden || w.Neighbors[1].DistantView != "" {
		t.Fatalf("visibility = %+v", w.Neighbors)
	}
	if w.Templated() != 3 {
		t.Fatalf("templated = %d", w.Templated())
	}
	joined := strings.Join(res.Issues, "\n")
	for _, want := range []string{"характеристики \"magic\" нет", "объектов больше 2", "location_id=77 не из задания", "(5, 5) не из задания", "(-1, 1) не описан: шаблон"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("issues without %q: %s", want, joined)
		}
	}
	// Без заданий блок `generate` не передаётся, пустой `world` принимается без замечаний.
	plain := &fakeLLM{responses: []string{`{"chronicle":"Тихо.","scenes":[{"scene_id":1,"text":"Вор ждёт.","event_seqs":[]}],"world":{"cells":[],"neighbors":[]}}`}}
	res, err = newService(plain).Narrate(context.Background(), Params{Title: "Мир", State: &st, Def: def()})
	if err != nil || len(res.Issues) != 0 || len(res.World.Cells) != 0 || strings.Contains(plain.requests[0].Input, `"generate"`) {
		t.Fatalf("plain = %+v, %v, input %s", res, err, plain.requests[0].Input)
	}
	// Схема ответа содержит пакет мира обязательным полем.
	var schema map[string]any
	if err := json.Unmarshal(Schema, &schema); err != nil || !strings.Contains(string(Schema), `"required": ["chronicle", "scenes", "world"]`) {
		t.Fatalf("schema = %v", err)
	}
}

// TestTemplateWorld: сбой модели закрывает все задания шаблоном: клетка «дорога» с именем карточки, соседи
// «неизведанные земли».
func TestTemplateWorld(t *testing.T) {
	t.Parallel()
	st := gridState()
	tasks := PlanTasks(&st, func(x, y int) bool { return x == 0 && y == 0 })
	w := TemplateWorld(tasks)
	if len(w.Cells) != 1 || w.Cells[0].Name != "Лес" || w.Cells[0].Description != model.TemplateCellDesc || !w.Cells[0].Template || len(w.Cells[0].Objects) != 0 {
		t.Fatalf("cells = %+v", w.Cells)
	}
	if len(w.Neighbors) != 3 || w.Neighbors[0].Name != model.TemplateCardName || w.Neighbors[2].X != 1 || w.Neighbors[2].Y != 1 {
		t.Fatalf("neighbors = %+v", w.Neighbors)
	}
	if cell := templateCell(CellTask{LocationID: 5, Name: model.TemplateCardName}); cell.Name != model.TemplateCellName {
		t.Fatalf("template cell from template card = %+v", cell)
	}
}
