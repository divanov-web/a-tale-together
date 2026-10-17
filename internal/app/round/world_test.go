package round

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// worldLLM — рассказчик, который достраивает мир по заданиям из входа (как сделала бы модель): каждой клетке из
// `generate.cells` — описание и объект с подходом без броска, каждому соседу — карточка; по заданиям квестов
// (пункт 28) — квест с минимальным числом этапов (первый на расстоянии 1, условие `fact`), размещение этапа объектом
// «зацепка» с подходом без броска и условием по виду из задания (`fact` — его факт, `visited`, `object_state` —
// «камень», который сдвигают), цель фракции; сцены по входу, все события упомянуты.
// Интерпретатор недоступен: заявки разбирает код (`идти north`). Завязка — стартовый пакет из opening.
type worldLLM struct {
	opening    string
	calls      map[model.LLMRole]int
	generated  int // клеток и соседей, выданных моделью
	quests     int // квестов, выданных моделью
	placed     int // этапов, размещённых моделью
	inputs     []string
	noHints    bool // не отмечать просьбы о подсказке в `hints_given` (ADR-0038): просьба переносится
	echoFlavor bool // интерпретатор отвечает: каждая заявка — `wait` с окраской, равной её тексту (ADR-0039); иначе сбой
}

// questBlock — задания по квестам во входе рассказчика.
type questBlock struct {
	Quests []narrator.QuestTask `json:"quests"`
	Stages []narrator.StageTask `json:"stages"`
	Goals  []narrator.GoalTask  `json:"goals"`
}

// answerQuests — ответ фейка на задания по квестам.
func (w *worldLLM) answerQuests(q *questBlock) (quests, stages, goals []map[string]any) {
	quests, stages, goals = []map[string]any{}, []map[string]any{}, []map[string]any{}
	if q == nil {
		return
	}
	for _, task := range q.Quests {
		w.quests++
		st := []map[string]any{}
		for i := 0; i < task.MinStages; i++ {
			cond := "fact"
			if i > 0 {
				cond = "visited"
			}
			st = append(st, map[string]any{"key": fmt.Sprintf("step_%d", i+1), "title": fmt.Sprintf("Шаг %d", i+1), "summary": "Сделать шаг.", "distance": 1, "condition": cond, "reward": "Награда за шаг."})
		}
		quests = append(quests, map[string]any{"faction": task.Faction, "title": "Дело " + string(task.Kind), "summary": "Описание дела.", "stages": st})
	}
	for _, task := range q.Stages {
		w.placed++
		fact := "Подсказка: " + task.StageTitle
		key := "clue_" + task.StageKey
		object := map[string]any{"key": key, "title": "зацепка", "description": "", "state": "", "hidden": false,
			"approaches": []map[string]any{{"action": "explore", "title": "изучить зацепку", "attribute": nil, "skill": nil, "difficulty": 0, "on_success": map[string]any{"state": nil, "reveal": nil, "fact": fact}, "on_fail": nil}}}
		condition := map[string]any{"kind": "fact", "object": nil, "state": nil, "fact": fact}
		switch task.ConditionKind {
		case model.ConditionVisited:
			condition = map[string]any{"kind": "visited", "object": nil, "state": nil, "fact": nil}
		case model.ConditionObjectState:
			object = map[string]any{"key": key, "title": "камень вожака", "description": "", "state": "in_place", "hidden": false,
				"approaches": []map[string]any{{"action": "interact", "title": "сдвинуть камень", "attribute": nil, "skill": nil, "difficulty": 0, "on_success": map[string]any{"state": "moved", "reveal": nil, "fact": nil}, "on_fail": nil}}}
			condition = map[string]any{"kind": "object_state", "object": key, "state": "moved", "fact": nil}
		}
		stages = append(stages, map[string]any{"quest_id": task.QuestID, "stage_key": task.StageKey, "condition": condition, "objects": []map[string]any{object}})
	}
	for _, task := range q.Goals {
		goals = append(goals, map[string]any{"faction": task.Faction, "text": "Цель фракции " + task.Faction + " после «" + task.Closed + "».", "stage": "дальше"})
	}
	return
}

func (w *worldLLM) Complete(_ context.Context, call applm.Call, req model.LLMRequest) (model.LLMResponse, error) {
	if w.calls == nil {
		w.calls = map[model.LLMRole]int{}
	}
	w.calls[call.Role]++
	w.inputs = append(w.inputs, req.Input)
	switch call.Role {
	case model.RoleOpening:
		if w.opening == "" {
			return model.LLMResponse{}, errors.New("opening down")
		}
		return model.LLMResponse{JSON: json.RawMessage(w.opening), Usage: model.LLMUsage{Input: 10, Output: 5}}, nil
	case model.RoleInterpreter:
		if !w.echoFlavor {
			return model.LLMResponse{}, errors.New("interpreter down")
		}
		var in struct {
			Submissions []struct {
				SubmissionID int64  `json:"submission_id"`
				CharacterID  int64  `json:"character_id"`
				Text         string `json:"text"`
			} `json:"submissions"`
		}
		if err := json.Unmarshal([]byte(req.Input), &in); err != nil {
			return model.LLMResponse{}, err
		}
		intents := []map[string]any{}
		for _, s := range in.Submissions {
			intents = append(intents, map[string]any{"submission_id": s.SubmissionID, "character_id": s.CharacterID, "action_type": "wait", "target": "", "approach_idx": nil, "attribute": nil, "skill": nil, "risk": nil, "on_success": nil, "on_fail": nil, "push": false, "flavor": s.Text, "ambiguities": []string{}, "fallback": nil})
		}
		b, _ := json.Marshal(map[string]any{"intents": intents})
		return model.LLMResponse{JSON: b, Usage: model.LLMUsage{Input: 10, Output: 5}}, nil
	}
	var in struct {
		Scenes []struct {
			SceneID int `json:"scene_id"`
		} `json:"scenes"`
		Events []struct {
			Seq int64 `json:"seq"`
		} `json:"events"`
		Generate *struct {
			Cells     []narrator.CellTask     `json:"cells"`
			Neighbors []narrator.NeighborTask `json:"neighbors"`
			Quests    *questBlock             `json:"quests"`
		} `json:"generate"`
		Hints []struct {
			Faction string `json:"faction"`
		} `json:"hints"`
	}
	if err := json.Unmarshal([]byte(req.Input), &in); err != nil {
		return model.LLMResponse{}, err
	}
	hintsGiven := []string{}
	if !w.noHints {
		for _, h := range in.Hints {
			hintsGiven = append(hintsGiven, h.Faction)
		}
	}
	seqs := []int64{}
	for _, e := range in.Events {
		seqs = append(seqs, e.Seq)
	}
	scenes := []map[string]any{}
	for i, sc := range in.Scenes {
		own := []int64{}
		if i == 0 {
			own = seqs
		}
		scenes = append(scenes, map[string]any{"scene_id": sc.SceneID, "text": "Группа идёт дальше.", "event_seqs": own})
	}
	cells, neighbors := []map[string]any{}, []map[string]any{}
	quests, stages, goals := []map[string]any{}, []map[string]any{}, []map[string]any{}
	if in.Generate != nil {
		quests, stages, goals = w.answerQuests(in.Generate.Quests)
		for _, c := range in.Generate.Cells {
			w.generated++
			cells = append(cells, map[string]any{"location_id": c.LocationID, "name": c.Name + " (подробно)", "description": "Описание клетки " + c.Name + ".", "difficulty": 0,
				"objects": []map[string]any{{"key": "sign_" + fmt.Sprint(c.LocationID), "title": "указатель", "description": "", "state": "", "hidden": false,
					"approaches": []map[string]any{{"action": "explore", "title": "прочитать указатель", "attribute": nil, "skill": nil, "difficulty": 0, "on_success": map[string]any{"state": nil, "reveal": nil, "fact": "На указателе стёртая надпись."}, "on_fail": nil}}}}})
		}
		for _, n := range in.Generate.Neighbors {
			w.generated++
			neighbors = append(neighbors, map[string]any{"x": n.X, "y": n.Y, "name": fmt.Sprintf("Клетка %d,%d", n.X, n.Y), "terrain": "forest", "card": "Лес шумит.", "visible": n.Direction != model.West, "distant_view": "шум листвы"})
		}
	}
	raw, _ := json.Marshal(map[string]any{"chronicle": "Ветер.", "scenes": scenes, "world": map[string]any{"cells": cells, "neighbors": neighbors, "quests": quests, "stages": stages, "goals": goals}, "hints_given": hintsGiven})
	return model.LLMResponse{JSON: raw, Usage: model.LLMUsage{Input: 100, Output: 50}}, nil
}

// startPackageJSON — ответ завязки: стартовая клетка с объектом, четыре карточки соседей, цель и сюжетный квест
// каждой фракции (пункт 28): у Ордена три этапа (первый — факт на расстоянии 1), у Гильдии — три этапа.
const startPackageJSON = `{"text":"Над площадью туман, Элис, Боб и Кэт ждут вестей. Ветер приносит запах дыма с севера, и никто не знает, откуда он. Торговцы закрывают лавки раньше времени. Кто-то должен пойти и посмотреть.",
 "factions":[
  {"code":"good","goal":{"text":"Узнать, откуда дым, и защитить город.","stage":"тревога"},
   "quest":{"faction":"good","title":"Дым с севера","summary":"Кто-то жжёт костры на севере; надо понять, кто и зачем.",
    "stages":[{"key":"smoke_source","title":"Найти источник дыма","summary":"Понять, откуда идёт дым.","distance":1,"condition":"fact","reward":"Дым идёт от лагеря на холме."},
              {"key":"camp","title":"Выйти к лагерю","summary":"Добраться до лагеря.","distance":2,"condition":"visited","reward":""},
              {"key":"chief","title":"Договориться с вожаком","summary":"Вожак должен согласиться уйти.","distance":1,"condition":"object_state","reward":"Лагерь снимается."}]}},
  {"code":"evil","goal":{"text":"Обернуть тревогу города себе на пользу.","stage":"выжидание"},
   "quest":{"faction":"evil","title":"Тёмный груз","summary":"Пока Орден смотрит на север, провезти груз.",
    "stages":[{"key":"contact","title":"Найти связного","summary":"Связной ждёт за воротами.","distance":1,"condition":"fact","reward":"Связной: груз в южной сторожке."},
              {"key":"lodge","title":"Добраться до сторожки","summary":"Дойти до сторожки.","distance":1,"condition":"visited","reward":""},
              {"key":"cargo","title":"Забрать груз","summary":"Сундук должен быть открыт.","distance":1,"condition":"object_state","reward":"Груз у Гильдии."}]}}],
 "start":{"location_id":%d,"name":"Площадь у колодца","description":"Сердце города; у колодца толпа.","difficulty":0,"objects":[{"key":"well","title":"колодец","description":"","state":"","hidden":false,"approaches":[{"action":"explore","title":"заглянуть в колодец","attribute":"wits","skill":"awareness","difficulty":5,"on_success":{"state":null,"reveal":null,"fact":"На дне блестит кольцо."},"on_fail":null}]}]},
 "neighbors":[{"direction":"north","name":"Опушка","terrain":"forest","card":"Кромка леса.","visible":true,"distant_view":""},{"direction":"south","name":"Южные ворота","terrain":"town","card":"Ворота на тракт.","visible":true,"distant_view":""},{"direction":"west","name":"Переулки","terrain":"town","card":"Тесные дворы.","visible":false,"distant_view":"гул голосов за углом"},{"direction":"east","name":"Рынок","terrain":"town","card":"Торговые ряды.","visible":true,"distant_view":""}],
 "lair":{"name":"Чёрная мельница","terrain":"ruins","card":"Мельница без крыльев на голом холме.","distant_view":"крыло мельницы над холмами"}}`

// startWorld — мир только со стартовой клеткой (как после `/create`): остальное достраивают завязка и рассказчик.
func startWorld(t *testing.T) *world {
	t.Helper()
	s := model.DefaultGameSettings()
	s.MinRoundAnswers = 1
	w := newWorld(t, s)
	ctx := context.Background()
	genre, _ := w.store.GetGenre(ctx, w.game.GenreID)
	lt := genre.Template.Locations[0]
	start, err := w.store.CreateLocation(ctx, model.Location{GameID: w.game.ID, Name: lt.Name, Description: lt.Description, Terrain: lt.Terrain, Objects: lt.Objects, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := range w.chars {
		w.chars[i].LocationID = &start.ID
		if err := w.store.SaveCharacterState(ctx, w.game.ID, w.chars[i]); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func (w *world) locations(t *testing.T) []model.Location {
	t.Helper()
	locs, err := w.store.ListLocations(context.Background(), w.game.ID)
	if err != nil {
		t.Fatal(err)
	}
	return locs
}

// lair даёт силам зла клетку логова (ADR-0031), как это делает `game.Create`.
func (w *world) lair(t *testing.T, at model.Coord) {
	t.Helper()
	ctx := context.Background()
	factions, err := w.store.ListFactions(ctx, w.game.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range factions {
		if f.Code == model.FactionEvil {
			f.Lair = &at
			if err := w.store.UpdateFaction(ctx, f); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// has — есть ли клетка с координатами.
func (w *world) has(t *testing.T, x, y int) bool {
	t.Helper()
	for _, l := range w.locations(t) {
		if l.X == x && l.Y == y {
			return true
		}
	}
	return false
}

func (w *world) at(t *testing.T, x, y int) model.Location {
	t.Helper()
	for _, l := range w.locations(t) {
		if l.X == x && l.Y == y {
			return l
		}
	}
	t.Fatalf("no cell at (%d, %d)", x, y)
	return model.Location{}
}

// TestBegin_StartPackage: `/begin` создаёт центр `detailed` (описание и объекты от модели заменяют шаблон), четырёх
// соседей `card` и завязку; при сбое модели центр остаётся шаблонным, соседи — «неизведанные земли».
func TestBegin_StartPackage(t *testing.T) {
	t.Parallel()
	w := startWorld(t)
	ctx := context.Background()
	start := w.locations(t)[0]
	llm := &worldLLM{opening: fmt.Sprintf(startPackageJSON, start.ID)}
	w.svc.LLM = llm
	for _, c := range w.chars {
		_ = w.store.MarkStarted(ctx, c.UserID)
	}
	g := w.begin(t)
	if !strings.HasPrefix(g.OpeningScene, "Над площадью туман") {
		t.Fatalf("opening = %q", g.OpeningScene)
	}
	locs := w.locations(t)
	if len(locs) != 5 || locs[0].Name != "Площадь у колодца" || locs[0].Stage != model.StageDetailed || len(locs[0].Objects) != 1 || locs[0].Objects[0].Key != "well" || locs[0].Terrain != "town" {
		t.Fatalf("start after package = %+v", locs[0])
	}
	// B49: стартовая клетка посещена с момента старта, карточки соседей — нет.
	if locs[0].VisitedAt == nil {
		t.Fatalf("start not visited after begin: %+v", locs[0])
	}
	if n := w.at(t, 0, 1); n.Name != "Опушка" || n.Stage != model.StageCard || n.Terrain != "forest" || n.Description != "Кромка леса." || n.VisitedAt != nil {
		t.Fatalf("north card = %+v", n)
	}
	if e := w.at(t, 1, 0); e.Name != "Рынок" || e.Hidden {
		t.Fatalf("east card = %+v", e)
	}
	// ADR-0025: скрытый сосед хранится со скрытостью и видом издалека; `/scout` подписывает его видом.
	if west := w.at(t, -1, 0); !west.Hidden || west.DistantView != "гул голосов за углом" || west.Glimpse() != "неизведанная территория (гул голосов за углом)" {
		t.Fatalf("west card = %+v", west)
	}
	if text, err := ScoutText(w.chars[0], w.locations(t)); err != nil || !strings.Contains(text.Text, "• на север: Опушка — Кромка леса.\n• на юг: Южные ворота — Ворота на тракт.\n• на запад: неизведанная территория (гул голосов за углом)\n• на восток: Рынок — Торговые ряды.") {
		t.Fatalf("scout = %q, %v", text.Text, err)
	}
	// S22 (ADR-0031): без логова у фракций карточка логова из ответа игнорируется и задания во входе нет.
	if strings.Contains(llm.inputs[0], `"lair"`) {
		t.Fatalf("lair task without faction lair: %s", llm.inputs[0])
	}
	// Личное «раунд открыт» предлагает колодец и четыре перехода.
	var open string
	for _, m := range w.outbox(t) {
		if m.Kind == model.OutboxPrivateOpen {
			open = m.Text
		}
	}
	if !strings.Contains(open, "• заглянуть в колодец (Разум/внимательность): параметр 6, сложность 5, шанс 60%") || strings.Contains(open, "идти на") {
		t.Fatalf("personal open = %q", open)
	}

	down := startWorld(t)
	down.svc.LLM = &worldLLM{}
	down.begin(t)
	locs = down.locations(t)
	if len(locs) != 5 || locs[0].Name != "Рыночная площадь" || len(locs[0].Objects) != 1 || locs[0].Objects[0].Key != "notice_board" {
		t.Fatalf("start after fallback = %+v", locs[0])
	}
	if n := down.at(t, 0, -1); n.Name != model.TemplateCardName || n.Stage != model.StageCard || n.Terrain != model.TemplateCardTerrain {
		t.Fatalf("template card = %+v", n)
	}
}

// TestBegin_Lair (S22, ADR-0031): у Гильдии логово `(3,-3)` — завязка получает задание `generate.lair` с направлением от
// площади, после `/begin` в игре шесть клеток: логово в стадии `card`, скрытое, с видом издалека от модели; при сбое
// модели — шаблон «Логово» без вида; повторный старт после сброса мира создаёт логово заново; рассказчик раунда
// получает логово как фон.
func TestBegin_Lair(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := startWorld(t)
	w.lair(t, model.Coord{X: 3, Y: -3})
	start := w.locations(t)[0]
	llm := &worldLLM{opening: fmt.Sprintf(startPackageJSON, start.ID)}
	w.svc.LLM = llm
	w.begin(t)
	if !strings.Contains(llm.inputs[0], `"lair":{"x":3,"y":-3,"bearing":"недалеко на юго-востоке","faction":"evil","title":"Гильдия"}`) {
		t.Fatalf("opening input without lair task: %s", llm.inputs[0])
	}
	if locs := w.locations(t); len(locs) != 6 {
		t.Fatalf("cells after begin = %d", len(locs))
	}
	lair := w.at(t, 3, -3)
	if lair.Name != "Чёрная мельница" || lair.Stage != model.StageCard || !lair.Hidden || lair.DistantView != "крыло мельницы над холмами" || lair.Terrain != "ruins" || lair.VisitedAt != nil || lair.Glimpse() != "неизведанная территория (крыло мельницы над холмами)" {
		t.Fatalf("lair = %+v", lair)
	}
	// Первый раунд: рассказчик получает логово фоном — фракция и вид издалека, без имени; направление у сцены в
	// первом раунде не даётся (B53: каждый третий раунд, `narrator.LairHintEvery`).
	for _, c := range w.chars {
		if _, _, err := w.svc.Submit(ctx, w.game, c, "осмотреться"); err != nil {
			t.Fatal(err)
		}
	}
	w.tick(t, 10*time.Minute)
	last := llm.inputs[len(llm.inputs)-1]
	if !strings.Contains(last, `"lair":{"faction":"Гильдия","distant_view":"крыло мельницы над холмами"}`) || strings.Contains(last, "Чёрная мельница") || strings.Contains(last, `"lair":"недалеко на юго-востоке"`) {
		t.Fatalf("narrator input lair: %s", last)
	}
	// Сброс мира удаляет логово, повторный старт создаёт его заново (шаблон при сбое модели).
	if _, err := w.svc.Reset(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	if w.has(t, 3, -3) {
		t.Fatal("lair survived reset")
	}
	w.svc.LLM = &worldLLM{}
	w.begin(t)
	lair = w.at(t, 3, -3)
	if lair.Name != model.TemplateLairName || !lair.Hidden || lair.DistantView != "" || lair.Terrain != model.TemplateLairTerrain || lair.Description != model.TemplateLairCard {
		t.Fatalf("template lair = %+v", lair)
	}
}

// TestSixSteps_North — критерий готовности пункта 27: после старта из пяти клеток и шести шагов по прямой на север в
// игре ровно 23 клетки; каждая пройденная клетка детализирована рассказчиком с объектом; возврат в известную клетку
// не даёт заданий на генерацию; каждая новая клетка — одно открытие фракции; `/replay` любого раунда не обращается к
// модели и совпадает.
func TestSixSteps_North(t *testing.T) {
	t.Parallel()
	w := startWorld(t)
	ctx := context.Background()
	start := w.locations(t)[0]
	llm := &worldLLM{opening: fmt.Sprintf(startPackageJSON, start.ID)}
	w.svc.LLM = llm
	w.begin(t)
	for step := 1; step <= 6; step++ {
		w.reload(t)
		for _, c := range w.chars {
			if _, _, err := w.svc.Submit(ctx, w.game, c, "идти на север"); err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
		}
		w.tick(t, 10*time.Minute)
		if got := len(w.locations(t)); got != 5+3*step {
			t.Fatalf("after step %d: %d cells, want %d", step, got, 5+3*step)
		}
		cell := w.at(t, 0, step)
		// Пункт 28: в клетке объект рассказчика, а если здесь разместился этап квеста — ещё «зацепка»: шаг 1 — первый
		// этап стартового сюжетного квеста (расстояние 1 от площади); шаг 2 — первый этап побочного квеста, выданного
		// на шаге 1 за одну открытую клетку; шаг 6 — первый этап второго сюжетного, выданного на шаге 5 после ещё
		// четырёх клеток (порог 4 при двух активных).
		objects := 1
		if step == 1 || step == 2 || step == 6 {
			objects = 2
		}
		if cell.Stage != model.StageDetailed || !strings.HasSuffix(cell.Name, "(подробно)") || len(cell.Objects) != objects || cell.VisitedAt == nil {
			t.Fatalf("step %d: entered cell = %+v", step, cell)
		}
		for _, d := range []model.Direction{model.North, model.West, model.East} {
			dx, dy := d.Offset()
			if n := w.at(t, dx, step+dy); n.Stage != model.StageCard || n.Description != "Лес шумит." {
				t.Fatalf("step %d: neighbor %s = %+v", step, d, n)
			}
		}
		info, _ := w.svc.Info(ctx, w.game)
		if m := info.Last.Metrics; m == nil || m.WorldCells != 4 || m.WorldTemplated != 0 || m.NarratorFallback {
			t.Fatalf("step %d: metrics = %+v", step, m)
		}
		if n, same, err := w.svc.Replay(ctx, w.game, 1, step); err != nil || !same || n == 0 {
			t.Fatalf("step %d: replay = %d, %v, %v", step, n, same, err)
		}
	}
	if llm.generated != 6*4 || llm.calls[model.RoleNarrator] != 6 {
		t.Fatalf("generated = %d, narrator calls = %d", llm.generated, llm.calls[model.RoleNarrator])
	}
	// Счётчик Ордена: 1 → выдача побочного (−1) → 0, затем 4 → выдача сюжетного (−4) → 0, шаг 6 → 1; Гильдия не ходила.
	factions, _ := w.store.ListFactions(ctx, w.game.ID)
	if factions[0].ExploredSinceReset != 1 || factions[1].ExploredSinceReset != 0 {
		t.Fatalf("explored = %+v", factions)
	}
	quests, _ := w.store.ListQuests(ctx, w.game.ID)
	kinds := []model.QuestKind{}
	for _, q := range quests {
		if q.FactionID == factions[0].ID {
			kinds = append(kinds, q.Kind)
		}
	}
	if fmt.Sprint(kinds) != "[story side story]" || len(quests) != 4 || llm.quests != 2 || llm.placed != 3 {
		t.Fatalf("quests = %v (all %d), model quests %d, placed %d", kinds, len(quests), llm.quests, llm.placed)
	}
	// Возврат на юг в известную клетку: заданий нет, карта не растёт, открытий нет.
	w.reload(t)
	for _, c := range w.chars {
		if _, _, err := w.svc.Submit(ctx, w.game, c, "идти на юг"); err != nil {
			t.Fatal(err)
		}
	}
	w.tick(t, 10*time.Minute)
	if got := len(w.locations(t)); got != 23 {
		t.Fatalf("after return: %d cells", got)
	}
	if last := llm.inputs[len(llm.inputs)-1]; strings.Contains(last, `"generate"`) {
		t.Fatalf("return generated tasks: %s", last)
	}
	factions, _ = w.store.ListFactions(ctx, w.game.ID)
	if factions[0].ExploredSinceReset != 1 {
		t.Fatalf("explored after return = %+v", factions)
	}
	if d, _ := w.store.ListDiscoveries(ctx, w.game.ID); len(d) != 6 || len(d[w.at(t, 0, 3).ID]) != 1 {
		t.Fatalf("discoveries = %+v", d)
	}
}

// crashJournal — журнал, который один раз паникует на записи «resolved»: сбой конвейера после вызова рассказчика.
type crashJournal struct {
	fakeJournal
	crashes int
}

func (j *crashJournal) Write(ctx context.Context, rec model.ChatLogRecord) {
	if rec.Event == "resolved" && j.crashes > 0 {
		j.crashes--
		panic("pipeline crashed after narrator")
	}
	j.fakeJournal.Write(ctx, rec)
}

// TestRound_RetryReusesGeneration: сбой после вызова рассказчика откатывает транзакцию, но ответ модели сохранён по
// ключу задания (ADR-0019): повтор раунда берёт его без второго вызова, клетка генерируется один раз, мир тот же.
func TestRound_RetryReusesGeneration(t *testing.T) {
	t.Parallel()
	w := startWorld(t)
	ctx := context.Background()
	start := w.locations(t)[0]
	llm := &worldLLM{opening: fmt.Sprintf(startPackageJSON, start.ID)}
	w.svc.LLM = llm
	j := &crashJournal{crashes: 1}
	w.svc.Journal = j
	w.begin(t)
	w.reload(t)
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "идти north"); err != nil {
		t.Fatal(err)
	}
	w.clock.t = w.clock.t.Add(10 * time.Minute)
	if err := w.svc.Tick(ctx, w.clock.Now()); err == nil {
		t.Fatal("tick with panic must fail")
	}
	if got := len(w.locations(t)); got != 5 {
		t.Fatalf("cells committed despite rollback: %d", got)
	}
	if llm.calls[model.RoleNarrator] != 1 {
		t.Fatalf("narrator calls before retry = %d", llm.calls[model.RoleNarrator])
	}
	w.tick(t, 10*time.Second)
	info, _ := w.svc.Info(ctx, w.game)
	if info.Last == nil || info.Last.Status != model.RoundPublished || info.Last.Attempts != 1 {
		t.Fatalf("round after retry = %+v", info.Last)
	}
	if llm.calls[model.RoleNarrator] != 1 || llm.generated != 4 {
		t.Fatalf("narrator called again: calls = %d, generated = %d", llm.calls[model.RoleNarrator], llm.generated)
	}
	if m := info.Last.Metrics; m == nil || m.LLMReused != 1 || m.Attempt != 2 || m.WorldCells != 4 {
		t.Fatalf("metrics = %+v", m)
	}
	if got := len(w.locations(t)); got != 8 {
		t.Fatalf("cells after retry = %d", got)
	}
	// Объект рассказчика и «зацепка» первого этапа сюжетного квеста (пункт 28): этап размещён один раз.
	if cell := w.at(t, 0, 1); cell.Name != "Опушка (подробно)" || len(cell.Objects) != 2 || cell.Objects[1].Key != "clue_smoke_source" {
		t.Fatalf("entered cell = %+v", cell)
	}
	if quests, _ := w.store.ListQuests(ctx, w.game.ID); len(quests) != 3 || quests[0].Stages[0].Status != model.StageBound {
		t.Fatalf("quests after retry = %+v", quests)
	}
}

// TestSaveState_ByID: клетки снимка сохраняются по id, а не по позиции: персонаж в клетке, которая в снимке стоит
// не на том месте, что в карте игры, меняет состояние объекта — обновляется именно эта клетка.
func TestSaveState_ByID(t *testing.T) {
	t.Parallel()
	w := startWorld(t)
	ctx := context.Background()
	for _, cell := range []model.Location{
		{GameID: w.game.ID, Name: "Опушка", X: 0, Y: 1, Stage: model.StageDetailed, Terrain: "forest"},
		{GameID: w.game.ID, Name: "Сторожка", X: 0, Y: 2, Stage: model.StageDetailed, Terrain: "forest",
			Objects: []model.LocationObject{{Key: "lever", Title: "рычаг", State: "up", Approaches: []model.Approach{{Action: model.ActionInteract, Title: "дёрнуть рычаг", Difficulty: 0, OnSuccess: json.RawMessage(`{"state":"down"}`)}}}}},
		{GameID: w.game.ID, Name: "Бор", X: 0, Y: 3, Stage: model.StageCard, Terrain: "forest"},
	} {
		if _, err := w.store.CreateLocation(ctx, cell); err != nil {
			t.Fatal(err)
		}
	}
	hut := w.at(t, 0, 2)
	for i := range w.chars {
		w.chars[i].LocationID = &hut.ID
		if err := w.store.SaveCharacterState(ctx, w.game.ID, w.chars[i]); err != nil {
			t.Fatal(err)
		}
	}
	w.svc.LLM = &worldLLM{}
	w.begin(t)
	w.reload(t)
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "дёрнуть рычаг"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	info, _ := w.svc.Info(ctx, w.game)
	if len(info.Last.Snapshot.Locations) >= len(w.locations(t)) || info.Last.Snapshot.Locations[0].ID == w.locations(t)[0].ID {
		t.Fatalf("snapshot is not local: %d of %d cells, first %d", len(info.Last.Snapshot.Locations), len(w.locations(t)), info.Last.Snapshot.Locations[0].ID)
	}
	if after := w.at(t, 0, 2); after.Objects[0].State != "down" {
		t.Fatalf("lever not saved by id: %+v", after.Objects)
	}
	if start := w.at(t, 0, 0); len(start.Objects) != 1 || start.Objects[0].Key != "notice_board" {
		t.Fatalf("start cell touched: %+v", start.Objects)
	}
}
