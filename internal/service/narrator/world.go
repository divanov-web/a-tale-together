package narrator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"taletogether/internal/model"
)

// Пункт 27 (docs/game/world.md, ADR-0019): рассказчик в вызове раунда достраивает мир. Код планирует задания
// (клетки для детализации и недостающие соседи), модель возвращает пакет `world`, код проверяет его по заданиям,
// модели персонажа и лимитам; что не прошло — шаблон. Новые сущности рождаются только здесь: интерпретатор и
// текст игрока их не создают.

// CellTask — клетка стадии `card`, в которую вошла группа: рассказчик поднимает её до `detailed`.
type CellTask struct {
	LocationID model.LocationID `json:"location_id"`
	X          int              `json:"x"`
	Y          int              `json:"y"`
	Name       string           `json:"name"`
	Terrain    string           `json:"terrain"`
	Card       string           `json:"card"`
	Parties    []string         `json:"parties"` // кто вошёл: никнеймы присутствующих
}

// NeighborTask — недостающий сосед занятой клетки: нужна карточка.
type NeighborTask struct {
	X         int              `json:"x"`
	Y         int              `json:"y"`
	Direction model.Direction  `json:"direction"` // куда от клетки `of`
	Of        model.LocationID `json:"of_location_id"`
	OfName    string           `json:"of_name"`
}

// Tasks — задания на генерацию в этом раунде: клетки и соседи (пункт 27), квесты, этапы и цели фракций (пункт 28).
type Tasks struct {
	Cells         []CellTask           `json:"cells"`
	Neighbors     []NeighborTask       `json:"neighbors"`
	Overflow      []CellTask           `json:"-"` // клетки сверх лимита: шаблон без участия модели
	Quests        []QuestTask          `json:"quests,omitempty"`
	Stages        []StageTask          `json:"stages,omitempty"`
	Goals         []GoalTask           `json:"goals,omitempty"`
	StoryTemplate *model.QuestTemplate `json:"-"` // сюжетный квест шаблона жанра при сбое модели
}

// Empty — заданий нет: пакет мира от модели не нужен.
func (t Tasks) Empty() bool {
	return len(t.Cells) == 0 && len(t.Neighbors) == 0 && len(t.Quests) == 0 && len(t.Stages) == 0 && len(t.Goals) == 0
}

// PlanTasks планирует задания по состоянию после свёртки: занятые присутствующими персонажами клетки стадии
// `card` детализируются (до model.MaxCellsPerRound, остальные в Overflow), у каждой занятой клетки недостающие
// соседи получают задание на карточку. exists отвечает, есть ли клетка с координатами в мире (не только в снимке).
func PlanTasks(state *model.RoundSnapshot, exists func(x, y int) bool) Tasks {
	var t Tasks
	if state == nil {
		return t
	}
	occupied := map[model.LocationID][]string{}
	var order []model.LocationID
	for _, c := range state.Characters {
		if c.LocationID == nil || c.Status.Absent() {
			continue
		}
		label := c.Nickname // названия групп модели не передаются (решение руководителя 2026-10-13)
		if _, seen := occupied[*c.LocationID]; !seen {
			order = append(order, *c.LocationID)
		}
		if !contains(occupied[*c.LocationID], label) {
			occupied[*c.LocationID] = append(occupied[*c.LocationID], label)
		}
	}
	planned := map[[2]int]bool{}
	for _, id := range order {
		l, ok := state.Location(id)
		if !ok {
			continue
		}
		if l.Stage == model.StageCard {
			task := CellTask{LocationID: l.ID, X: l.X, Y: l.Y, Name: l.Name, Terrain: l.Terrain, Card: l.Description, Parties: occupied[id]}
			if len(t.Cells) < model.MaxCellsPerRound {
				t.Cells = append(t.Cells, task)
			} else {
				t.Overflow = append(t.Overflow, task)
			}
		}
		for _, d := range model.Directions {
			dx, dy := d.Offset()
			x, y := l.X+dx, l.Y+dy
			if planned[[2]int{x, y}] || exists(x, y) {
				continue
			}
			planned[[2]int{x, y}] = true
			t.Neighbors = append(t.Neighbors, NeighborTask{X: x, Y: y, Direction: d, Of: l.ID, OfName: l.Name})
		}
	}
	return t
}

func contains(list []string, v string) bool {
	for _, it := range list {
		if it == v {
			return true
		}
	}
	return false
}

// WorldPackage — принятый пакет мира: клетки с объектами и карточки соседей; что модель не вернула или вернула
// невалидно, закрыто шаблоном (`Template`).
type WorldPackage struct {
	Cells     []GeneratedCell
	Neighbors []GeneratedNeighbor
	Quests    []GeneratedQuest // новые квесты фракций по заданиям (пункт 28)
	Stages    []GeneratedStage // размещённые этапы
	Goals     []GeneratedGoal  // версии целей фракций
	Issues    []string         // замечания к пакету (для лога и eval); дозапроса по ним нет
}

// Templated — сколько заданий закрыто шаблоном.
func (w WorldPackage) Templated() int {
	n := 0
	for _, c := range w.Cells {
		if c.Template {
			n++
		}
	}
	for _, c := range w.Neighbors {
		if c.Template {
			n++
		}
	}
	for _, q := range w.Quests {
		if q.Template {
			n++
		}
	}
	for _, st := range w.Stages {
		if st.Template {
			n++
		}
	}
	for _, g := range w.Goals {
		if g.Template {
			n++
		}
	}
	return n
}

// GeneratedCell — детализированная клетка.
type GeneratedCell struct {
	LocationID  model.LocationID
	Name        string
	Description string
	Difficulty  int
	Terrain     string
	Objects     []model.LocationObject
	Template    bool
}

// GeneratedNeighbor — карточка соседа; скрытый (ADR-0025) известен игрокам только видом издалека до первого входа.
type GeneratedNeighbor struct {
	X, Y        int
	Name        string
	Terrain     string
	Card        string
	Hidden      bool   // модель решила, что сосед из занятой клетки не виден
	DistantView string // что видно издалека; пусто — «неизведанная территория»
	Template    bool
}

// rawWorld — блок `world` ответа модели по Schema.
type rawWorld struct {
	Cells     []rawCell           `json:"cells"`
	Neighbors []rawNeighbor       `json:"neighbors"`
	Quests    []rawQuest          `json:"quests"`
	Stages    []rawStagePlacement `json:"stages"`
	Goals     []rawGoal           `json:"goals"`
}

type rawCell struct {
	LocationID  int64       `json:"location_id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Difficulty  int         `json:"difficulty"`
	Objects     []rawObject `json:"objects"`
}

type rawObject struct {
	Key         string        `json:"key"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	State       string        `json:"state"`
	Hidden      bool          `json:"hidden"`
	Approaches  []rawApproach `json:"approaches"`
}

type rawApproach struct {
	Action     string   `json:"action"`
	Title      string   `json:"title"`
	Attribute  *string  `json:"attribute"`
	Skill      *string  `json:"skill"`
	Difficulty int      `json:"difficulty"`
	OnSuccess  *rawGood `json:"on_success"`
	OnFail     *rawBad  `json:"on_fail"`
}

type rawGood struct {
	State  *string `json:"state"`
	Reveal *string `json:"reveal"`
	Fact   *string `json:"fact"`
}

type rawBad struct {
	Condition *string `json:"condition"`
	Damage    *int    `json:"damage"`
	Fact      *string `json:"fact"`
}

type rawNeighbor struct {
	X           int    `json:"x"`
	Y           int    `json:"y"`
	Name        string `json:"name"`
	Terrain     string `json:"terrain"`
	Card        string `json:"card"`
	Visible     bool   `json:"visible"`      // виден ли сосед из клетки задания (ADR-0025)
	DistantView string `json:"distant_view"` // вид скрытого соседа издалека
}

// objectsSchema — массив объектов клетки (пакет мира, стартовый пакет, размещение этапа).
const objectsSchema = `{
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "key": {"type": "string"},
          "title": {"type": "string"},
          "description": {"type": "string"},
          "state": {"type": "string"},
          "hidden": {"type": "boolean"},
          "approaches": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "action": {"type": "string", "enum": ["explore", "interact"]},
                "title": {"type": "string"},
                "attribute": {"type": ["string", "null"]},
                "skill": {"type": ["string", "null"]},
                "difficulty": {"type": "integer"},
                "on_success": {
                  "type": ["object", "null"],
                  "properties": {"state": {"type": ["string", "null"]}, "reveal": {"type": ["string", "null"]}, "fact": {"type": ["string", "null"]}},
                  "required": ["state", "reveal", "fact"],
                  "additionalProperties": false
                },
                "on_fail": {
                  "type": ["object", "null"],
                  "properties": {"condition": {"type": ["string", "null"]}, "damage": {"type": ["integer", "null"]}, "fact": {"type": ["string", "null"]}},
                  "required": ["condition", "damage", "fact"],
                  "additionalProperties": false
                }
              },
              "required": ["action", "title", "attribute", "skill", "difficulty", "on_success", "on_fail"],
              "additionalProperties": false
            }
          }
        },
        "required": ["key", "title", "description", "state", "hidden", "approaches"],
        "additionalProperties": false
      }
    }`

// cellSchema — фрагмент JSON-схемы для блока `world` (хроника) и стартового пакета (завязка).
const cellSchema = `{
  "type": "object",
  "properties": {
    "location_id": {"type": "integer"},
    "name": {"type": "string"},
    "description": {"type": "string"},
    "difficulty": {"type": "integer"},
    "objects": ` + objectsSchema + `
  },
  "required": ["location_id", "name", "description", "difficulty", "objects"],
  "additionalProperties": false
}`

const neighborSchema = `{
  "type": "object",
  "properties": {
    "x": {"type": "integer"},
    "y": {"type": "integer"},
    "name": {"type": "string"},
    "terrain": {"type": "string"},
    "card": {"type": "string"},
    "visible": {"type": "boolean"},
    "distant_view": {"type": "string"}
  },
  "required": ["x", "y", "name", "terrain", "card", "visible", "distant_view"],
  "additionalProperties": false
}`

var keyChars = regexp.MustCompile(`[^a-z0-9]+`)

// normalizeKey — ключ объекта: латиница в snake_case; пусто, если латинских букв нет.
func normalizeKey(key string) string {
	k := strings.Trim(keyChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(key)), "_"), "_")
	if len(k) > 40 {
		k = k[:40]
	}
	return k
}

func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		return strings.TrimSpace(string(r[:max]))
	}
	return s
}

// validateWorld сверяет пакет модели с заданиями: клетки только из заданий (лишние отброшены), координаты соседей
// только из заданий, лимиты числа и длины, подходы по модели персонажа; пропущенное задание закрывает шаблон.
func validateWorld(raw rawWorld, tasks Tasks, def model.CharacterModelDef) WorldPackage {
	var out WorldPackage
	byID := map[model.LocationID]rawCell{}
	for _, c := range raw.Cells {
		id := model.LocationID(c.LocationID)
		if _, dup := byID[id]; dup {
			out.Issues = append(out.Issues, fmt.Sprintf("клетка location_id=%d повторяется: взята первая", id))
			continue
		}
		byID[id] = c
	}
	known := map[model.LocationID]bool{}
	for _, task := range tasks.Cells {
		known[task.LocationID] = true
		c, ok := byID[task.LocationID]
		if !ok {
			out.Issues = append(out.Issues, fmt.Sprintf("клетка location_id=%d («%s») не описана: шаблон", task.LocationID, task.Name))
			out.Cells = append(out.Cells, templateCell(task))
			continue
		}
		cell, issues := validateCell(c, task, def)
		out.Issues = append(out.Issues, issues...)
		out.Cells = append(out.Cells, cell)
	}
	for id := range byID {
		if !known[id] {
			out.Issues = append(out.Issues, fmt.Sprintf("клетка location_id=%d не из задания: отброшена", id))
		}
	}
	for _, task := range tasks.Overflow {
		out.Cells = append(out.Cells, templateCell(task))
	}
	byXY := map[[2]int]rawNeighbor{}
	for _, n := range raw.Neighbors {
		if _, dup := byXY[[2]int{n.X, n.Y}]; !dup {
			byXY[[2]int{n.X, n.Y}] = n
		}
	}
	wanted := map[[2]int]bool{}
	for _, task := range tasks.Neighbors {
		wanted[[2]int{task.X, task.Y}] = true
		n, ok := byXY[[2]int{task.X, task.Y}]
		card, issues := validateNeighbor(n, ok, task)
		out.Issues = append(out.Issues, issues...)
		out.Neighbors = append(out.Neighbors, card)
	}
	for xy := range byXY {
		if !wanted[xy] {
			out.Issues = append(out.Issues, fmt.Sprintf("сосед (%d, %d) не из задания: отброшен", xy[0], xy[1]))
		}
	}
	var issues []string
	out.Quests, issues = validateQuests(raw.Quests, tasks.Quests, tasks.StoryTemplate)
	out.Issues = append(out.Issues, issues...)
	out.Stages, issues = validateStages(raw.Stages, tasks.Stages, def)
	out.Issues = append(out.Issues, issues...)
	out.Goals, issues = validateGoals(raw.Goals, tasks.Goals)
	out.Issues = append(out.Issues, issues...)
	return out
}

// validateCell — клетка из ответа: имя (пустое — из карточки), описание, сложность 0–2, объекты по лимитам.
func validateCell(c rawCell, task CellTask, def model.CharacterModelDef) (GeneratedCell, []string) {
	var issues []string
	cell := GeneratedCell{LocationID: task.LocationID, Terrain: task.Terrain, Difficulty: c.Difficulty}
	cell.Name = clip(c.Name, model.MaxCellNameRunes)
	if cell.Name == "" {
		cell.Name = task.Name
	}
	cell.Description = clip(c.Description, model.MaxCellDescRunes)
	if cell.Description == "" {
		issues = append(issues, fmt.Sprintf("клетка «%s» без описания: взята карточка", cell.Name))
		cell.Description = task.Card
	}
	if cell.Difficulty < 0 || cell.Difficulty > 2 {
		issues = append(issues, fmt.Sprintf("клетка «%s»: сложность %d вне 0–2, взят 0", cell.Name, cell.Difficulty))
		cell.Difficulty = 0
	}
	objects, objIssues := validateObjects(c.Objects, cell.Name, def)
	cell.Objects = objects
	issues = append(issues, objIssues...)
	return cell, issues
}

// validateObjects — объекты клетки: уникальные ключи в snake_case, лимит числа, подходы по модели персонажа.
func validateObjects(raw []rawObject, cellName string, def model.CharacterModelDef) ([]model.LocationObject, []string) {
	var issues []string
	objects := []model.LocationObject{}
	keys := map[string]bool{}
	for _, o := range raw {
		if len(objects) >= model.MaxObjectsPerCell {
			issues = append(issues, fmt.Sprintf("клетка «%s»: объектов больше %d, лишние отброшены", cellName, model.MaxObjectsPerCell))
			break
		}
		key := normalizeKey(o.Key)
		if key == "" {
			key = normalizeKey(o.Title)
		}
		if key == "" {
			key = fmt.Sprintf("object_%d", len(objects)+1)
		}
		if keys[key] {
			issues = append(issues, fmt.Sprintf("клетка «%s»: ключ объекта «%s» повторяется, объект отброшен", cellName, key))
			continue
		}
		title := clip(o.Title, model.MaxObjectTitleRunes)
		if title == "" {
			issues = append(issues, fmt.Sprintf("клетка «%s»: объект «%s» без названия, отброшен", cellName, key))
			continue
		}
		keys[key] = true
		objects = append(objects, model.LocationObject{Key: key, Title: title, Description: clip(o.Description, model.MaxCardRunes), State: clip(o.State, 20), HiddenUntil: o.Hidden, Approaches: []model.Approach{}})
	}
	for i := range objects {
		for _, a := range raw[indexOf(raw, objects[i].Key)].Approaches {
			if len(objects[i].Approaches) >= model.MaxApproachesPerObj {
				issues = append(issues, fmt.Sprintf("объект «%s»: подходов больше %d, лишние отброшены", objects[i].Key, model.MaxApproachesPerObj))
				break
			}
			ap, err := validateApproach(a, objects, i, def)
			if err != nil {
				issues = append(issues, fmt.Sprintf("объект «%s»: %v", objects[i].Key, err))
				continue
			}
			objects[i].Approaches = append(objects[i].Approaches, ap)
		}
	}
	return objects, issues
}

// indexOf — индекс сырого объекта по нормализованному ключу (или названию).
func indexOf(raw []rawObject, key string) int {
	for i, o := range raw {
		k := normalizeKey(o.Key)
		if k == "" {
			k = normalizeKey(o.Title)
		}
		if k == key {
			return i
		}
	}
	for i := range raw {
		if fmt.Sprintf("object_%d", i+1) == key {
			return i
		}
	}
	return 0
}

// validateApproach — подход по правилам модели локации: действие из каталога, характеристика и навык из модели
// персонажа (без характеристики — без броска), сложность в границах, эффекты из списка.
func validateApproach(a rawApproach, objects []model.LocationObject, self int, def model.CharacterModelDef) (model.Approach, error) {
	ap := model.Approach{Action: model.ActionType(a.Action), Title: clip(a.Title, model.MaxApproachTitleRunes)}
	if ap.Action != model.ActionExplore && ap.Action != model.ActionInteract {
		return ap, fmt.Errorf("подход «%s»: действие %q не поддерживается", ap.Title, a.Action)
	}
	if ap.Title == "" {
		return ap, fmt.Errorf("подход без названия")
	}
	if a.Attribute != nil && *a.Attribute != "" {
		if _, ok := def.Attribute(*a.Attribute); !ok {
			return ap, fmt.Errorf("подход «%s»: характеристики %q нет в модели", ap.Title, *a.Attribute)
		}
		ap.Attribute = *a.Attribute
		if a.Skill != nil && *a.Skill != "" {
			if _, ok := def.Skill(*a.Skill); !ok {
				return ap, fmt.Errorf("подход «%s»: навыка %q нет в модели", ap.Title, *a.Skill)
			}
			ap.Skill = *a.Skill
		}
		ap.Difficulty = a.Difficulty
		if ap.Difficulty < 1 || ap.Difficulty > 9 {
			return ap, fmt.Errorf("подход «%s»: сложность %d вне 1–9", ap.Title, a.Difficulty)
		}
	}
	good := map[string]string{}
	if a.OnSuccess != nil {
		if a.OnSuccess.State != nil && strings.TrimSpace(*a.OnSuccess.State) != "" {
			good["state"] = clip(*a.OnSuccess.State, 20)
		}
		if a.OnSuccess.Reveal != nil && *a.OnSuccess.Reveal != "" {
			key := normalizeKey(*a.OnSuccess.Reveal)
			found := false
			for j, o := range objects {
				if o.Key == key && j != self && o.HiddenUntil {
					found = true
				}
			}
			if !found {
				return ap, fmt.Errorf("подход «%s»: reveal %q не указывает на скрытый объект клетки", ap.Title, *a.OnSuccess.Reveal)
			}
			good["reveal"] = key
		}
		if a.OnSuccess.Fact != nil && strings.TrimSpace(*a.OnSuccess.Fact) != "" {
			good["fact"] = clip(*a.OnSuccess.Fact, model.MaxFactRunes)
		}
	}
	if len(good) > 0 {
		ap.OnSuccess, _ = json.Marshal(good)
	}
	bad := map[string]any{}
	if a.OnFail != nil {
		if a.OnFail.Condition != nil && *a.OnFail.Condition != "" {
			switch *a.OnFail.Condition {
			case model.ConditionWounded, model.ConditionExhausted, model.ConditionHidden, model.ConditionDetained:
				bad["condition"] = *a.OnFail.Condition
			default:
				return ap, fmt.Errorf("подход «%s»: состояние %q не из списка", ap.Title, *a.OnFail.Condition)
			}
		}
		if a.OnFail.Damage != nil && *a.OnFail.Damage > 0 {
			bad["damage"] = min(*a.OnFail.Damage, 2)
		}
		if a.OnFail.Fact != nil && strings.TrimSpace(*a.OnFail.Fact) != "" {
			bad["fact"] = clip(*a.OnFail.Fact, model.MaxFactRunes)
		}
	}
	if len(bad) > 0 {
		ap.OnFail, _ = json.Marshal(bad)
	}
	return ap, nil
}

// validateNeighbor — карточка соседа: имя, тип местности и одно предложение; пусто — шаблон. Скрытость и вид
// издалека (ADR-0025) берутся у модели как есть: вид только у скрытого соседа, обрезан до лимита карточки.
func validateNeighbor(n rawNeighbor, ok bool, task NeighborTask) (GeneratedNeighbor, []string) {
	if !ok {
		return templateNeighbor(task), []string{fmt.Sprintf("сосед (%d, %d) не описан: шаблон", task.X, task.Y)}
	}
	card := GeneratedNeighbor{X: task.X, Y: task.Y, Name: clip(n.Name, model.MaxCellNameRunes), Terrain: normalizeKey(n.Terrain), Card: clip(n.Card, model.MaxCardRunes), Hidden: !n.Visible}
	if card.Hidden {
		card.DistantView = clip(n.DistantView, model.MaxCardRunes)
	}
	var issues []string
	if card.Name == "" {
		return templateNeighbor(task), []string{fmt.Sprintf("сосед (%d, %d) без имени: шаблон", task.X, task.Y)}
	}
	if card.Terrain == "" {
		card.Terrain = model.TemplateCardTerrain
	}
	if card.Card == "" {
		issues = append(issues, fmt.Sprintf("сосед «%s» без карточки: взято имя", card.Name))
		card.Card = card.Name + "."
	}
	return card, issues
}

// TemplateWorld закрывает все задания шаблоном: клетка «дорога», карточки «неизведанные земли» (видимые), сюжетные квесты
// и цели из шаблона жанра, этапы объектом «след» (сбой модели, отсутствие провайдера или превышение бюджета).
func TemplateWorld(tasks Tasks) WorldPackage {
	var out WorldPackage
	for _, task := range tasks.Cells {
		out.Cells = append(out.Cells, templateCell(task))
	}
	for _, task := range tasks.Overflow {
		out.Cells = append(out.Cells, templateCell(task))
	}
	for _, task := range tasks.Neighbors {
		out.Neighbors = append(out.Neighbors, templateNeighbor(task))
	}
	out.Quests, out.Stages, out.Goals = TemplateQuests(tasks)
	return out
}

func templateCell(task CellTask) GeneratedCell {
	name := task.Name
	if name == "" || name == model.TemplateCardName {
		name = model.TemplateCellName
	}
	return GeneratedCell{LocationID: task.LocationID, Name: name, Description: model.TemplateCellDesc, Terrain: model.TemplateCellTerrain, Objects: []model.LocationObject{}, Template: true}
}

func templateNeighbor(task NeighborTask) GeneratedNeighbor {
	return GeneratedNeighbor{X: task.X, Y: task.Y, Name: model.TemplateCardName, Terrain: model.TemplateCardTerrain, Card: model.TemplateCardDesc, Template: true}
}

// modelInfo — ключи характеристик и навыков для подходов в пакете мира.
type modelInfo struct {
	Attributes []keyTitle `json:"attributes"`
	Skills     []skillRef `json:"skills"`
}

type keyTitle struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type skillRef struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Attribute string `json:"attribute"`
}

func modelKeys(def model.CharacterModelDef) modelInfo {
	info := modelInfo{Attributes: []keyTitle{}, Skills: []skillRef{}}
	for _, a := range def.Attributes {
		info.Attributes = append(info.Attributes, keyTitle{Key: a.Key, Title: a.Title})
	}
	for _, s := range def.Skills {
		info.Skills = append(info.Skills, skillRef{Key: s.Key, Title: s.Title, Attribute: s.Attribute})
	}
	return info
}
