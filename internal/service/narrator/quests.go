package narrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"taletogether/internal/model"
	"taletogether/internal/service/quest"
)

// Пункт 28 (docs/game/world.md «Квесты», ADR-0020): рассказчик в вызове раунда (и завязка при `/begin`) создаёт
// квесты и цели фракций по заданиям кода и размещает этапы в клетках; код проверяет пакет по заданиям, каталогу
// условий и достижимости, что не прошло — шаблон или перенос на следующий раунд.

// QuestTask — задание «новый квест фракции».
type QuestTask struct {
	FactionID model.FactionID `json:"-"`
	Faction   string          `json:"faction"` // код фракции
	Kind      model.QuestKind `json:"kind"`
	Goal      string          `json:"goal"` // текущая цель фракции
	MinStages int             `json:"min_stages"`
	MaxStages int             `json:"max_stages"`
	Threshold int             `json:"-"` // порог, списываемый со счётчика после принятия
	Mandatory bool            `json:"-"` // сюжетный при нуле сюжетных: при сбое модели — шаблон
}

// StageTask — задание «разместить этап в клетке»: что за этап и что в клетке уже есть.
type StageTask struct {
	QuestID       model.QuestID          `json:"quest_id"`
	QuestTitle    string                 `json:"quest_title"`
	StageKey      string                 `json:"stage_key"`
	StageTitle    string                 `json:"stage_title"`
	StageSummary  string                 `json:"stage_summary"`
	ConditionKind model.ConditionKind    `json:"condition_kind"` // вид условия, заданный при создании квеста
	LocationID    model.LocationID       `json:"location_id"`
	CellName      string                 `json:"cell_name"`
	CellDesc      string                 `json:"cell_description"`
	Objects       []string               `json:"objects"` // ключи объектов клетки (их можно использовать в условии)
	Existing      []model.LocationObject `json:"-"`
	Stage         model.QuestStage       `json:"-"`
}

// GoalTask — задание «цель фракции»: первая формулировка или пересмотр после закрытого сюжетного квеста.
type GoalTask struct {
	FactionID model.FactionID `json:"-"`
	Faction   string          `json:"faction"`
	Current   string          `json:"current"` // текущая формулировка; пусто у новой игры
	Stage     string          `json:"current_stage"`
	Closed    string          `json:"closed_quest"` // название закрытого сюжетного квеста; пусто у первой версии
	QuestID   *model.QuestID  `json:"-"`
	Fallback  string          `json:"-"` // текст шаблона при сбое модели
}

// QuestInfo — активный квест фракции в контексте рассказчика: одной строкой, ближайший этап подробно, закрытые
// этапы фразой; рассказчик намекает игрокам без кнопок.
type QuestInfo struct {
	Faction string           `json:"faction"`
	Kind    model.QuestKind  `json:"kind"`
	Title   string           `json:"title"`
	Summary string           `json:"summary"`
	Next    *QuestStageInfo  `json:"next"`   // первый незакрытый этап
	Closed  []string         `json:"closed"` // названия закрытых этапов
	Goal    string           `json:"goal"`   // текущая цель фракции
	Objects []string         `json:"-"`
	ID      model.QuestID    `json:"-"`
	Stages  []QuestStageInfo `json:"-"`
}

// QuestStageInfo — этап в контексте: привязанный несёт клетку и объекты.
type QuestStageInfo struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Placed  bool   `json:"placed"`         // этап уже в клетке
	Cell    string `json:"cell,omitempty"` // имя клетки этапа
	Hint    string `json:"hint,omitempty"` // что нужно сделать (условие словами)
}

// generateQuests — блок заданий по квестам внутри `generate`.
type generateQuests struct {
	Quests []QuestTask `json:"quests"`
	Stages []StageTask `json:"stages"`
	Goals  []GoalTask  `json:"goals"`
	Limits questLimits `json:"quest_limits"`
}

type questLimits struct {
	StoryStages    string   `json:"story_stages"`
	SideStages     string   `json:"side_stages"`
	MaxDistance    int      `json:"max_distance"`
	ConditionKinds []string `json:"condition_kinds"`
	TitleRunes     int      `json:"title_max_chars"`
	SummaryRunes   int      `json:"summary_max_chars"`
}

func questLimitsInfo() questLimits {
	kinds := make([]string, 0, len(model.ConditionKinds))
	for _, k := range model.ConditionKinds {
		kinds = append(kinds, string(k))
	}
	return questLimits{StoryStages: fmt.Sprintf("%d–%d", model.MinStoryStages, model.MaxStoryStages), SideStages: fmt.Sprintf("%d–%d", model.MinSideStages, model.MaxSideStages),
		MaxDistance: model.MaxStageDistance, ConditionKinds: kinds, TitleRunes: model.MaxQuestTitleRunes, SummaryRunes: model.MaxQuestSummaryRunes}
}

// GeneratedQuest — принятый квест: этапы без клеток, условие только видом.
type GeneratedQuest struct {
	FactionID model.FactionID
	Kind      model.QuestKind
	Title     string
	Summary   string
	Stages    []model.QuestStage
	Threshold int
	Template  bool
}

// GeneratedStage — принятое размещение этапа: объекты клетки и достижимое условие.
type GeneratedStage struct {
	QuestID    model.QuestID
	Stage      model.QuestStage
	LocationID model.LocationID
	Objects    []model.LocationObject
	Condition  model.StageCondition
	Template   bool
}

// GeneratedGoal — принятая версия цели.
type GeneratedGoal struct {
	FactionID model.FactionID
	Text      string
	Stage     string
	QuestID   *model.QuestID
	Template  bool
}

// rawQuest, rawStagePlacement, rawGoal — блоки ответа модели.
type rawQuest struct {
	Faction string          `json:"faction"`
	Title   string          `json:"title"`
	Summary string          `json:"summary"`
	Stages  []rawQuestStage `json:"stages"`
}

type rawQuestStage struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	Distance  int    `json:"distance"`
	Condition string `json:"condition"`
	Reward    string `json:"reward"`
}

type rawStagePlacement struct {
	QuestID   int64        `json:"quest_id"`
	StageKey  string       `json:"stage_key"`
	Objects   []rawObject  `json:"objects"`
	Condition rawCondition `json:"condition"`
}

type rawCondition struct {
	Kind   string  `json:"kind"`
	Object *string `json:"object"`
	State  *string `json:"state"`
	Fact   *string `json:"fact"`
}

type rawGoal struct {
	Faction string `json:"faction"`
	Text    string `json:"text"`
	Stage   string `json:"stage"`
}

// Схемы блоков квестов для ответа рассказчика и завязки.
const questSchema = `{
  "type": "object",
  "properties": {
    "faction": {"type": "string"},
    "title": {"type": "string"},
    "summary": {"type": "string"},
    "stages": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "key": {"type": "string"},
          "title": {"type": "string"},
          "summary": {"type": "string"},
          "distance": {"type": "integer"},
          "condition": {"type": "string", "enum": ["object_state", "fact", "visited"]},
          "reward": {"type": "string"}
        },
        "required": ["key", "title", "summary", "distance", "condition", "reward"],
        "additionalProperties": false
      }
    }
  },
  "required": ["faction", "title", "summary", "stages"],
  "additionalProperties": false
}`

const stagePlacementSchema = `{
  "type": "object",
  "properties": {
    "quest_id": {"type": "integer"},
    "stage_key": {"type": "string"},
    "objects": ` + objectsSchema + `,
    "condition": {
      "type": "object",
      "properties": {
        "kind": {"type": "string", "enum": ["object_state", "fact", "visited"]},
        "object": {"type": ["string", "null"]},
        "state": {"type": ["string", "null"]},
        "fact": {"type": ["string", "null"]}
      },
      "required": ["kind", "object", "state", "fact"],
      "additionalProperties": false
    }
  },
  "required": ["quest_id", "stage_key", "objects", "condition"],
  "additionalProperties": false
}`

const goalSchema = `{
  "type": "object",
  "properties": {
    "faction": {"type": "string"},
    "text": {"type": "string"},
    "stage": {"type": "string"}
  },
  "required": ["faction", "text", "stage"],
  "additionalProperties": false
}`

// validateQuests принимает квесты по заданиям: фракция из задания (одно задание — один квест), число этапов по
// типу (лишние отброшены, меньше минимума — принято с замечанием, ноль — шаблон для обязательного сюжетного,
// иначе перенос), ключи этапов в snake_case и уникальны, расстояние 1–MaxStageDistance, вид условия из каталога.
func validateQuests(raw []rawQuest, tasks []QuestTask, tpl *model.QuestTemplate) ([]GeneratedQuest, []string) {
	var out []GeneratedQuest
	var issues []string
	byFaction := map[string]rawQuest{}
	for _, q := range raw {
		code := strings.ToLower(strings.TrimSpace(q.Faction))
		if _, dup := byFaction[code]; dup {
			issues = append(issues, fmt.Sprintf("квест фракции «%s» повторяется: взят первый", code))
			continue
		}
		byFaction[code] = q
	}
	wanted := map[string]bool{}
	for _, task := range tasks {
		wanted[strings.ToLower(task.Faction)] = true
		q, ok := byFaction[strings.ToLower(task.Faction)]
		if !ok {
			issues = append(issues, fmt.Sprintf("квест для фракции «%s» не предложен", task.Faction))
			if g, ok := templateQuest(task, tpl); ok {
				out = append(out, g)
			}
			continue
		}
		g, more := validateQuest(q, task)
		issues = append(issues, more...)
		if len(g.Stages) == 0 {
			if t, ok := templateQuest(task, tpl); ok {
				out = append(out, t)
			}
			continue
		}
		out = append(out, g)
	}
	for code := range byFaction {
		if !wanted[code] {
			issues = append(issues, fmt.Sprintf("квест фракции «%s» не из задания: отброшен", code))
		}
	}
	return out, issues
}

func templateQuest(task QuestTask, tpl *model.QuestTemplate) (GeneratedQuest, bool) {
	if !task.Mandatory {
		return GeneratedQuest{}, false
	}
	q := quest.TemplateQuest(tpl, model.Faction{ID: task.FactionID})
	return GeneratedQuest{FactionID: task.FactionID, Kind: model.QuestStory, Title: q.Title, Summary: q.Summary, Stages: q.Stages, Threshold: task.Threshold, Template: true}, true
}

func validateQuest(q rawQuest, task QuestTask) (GeneratedQuest, []string) {
	var issues []string
	g := GeneratedQuest{FactionID: task.FactionID, Kind: task.Kind, Threshold: task.Threshold}
	g.Title = clip(q.Title, model.MaxQuestTitleRunes)
	if g.Title == "" {
		issues = append(issues, fmt.Sprintf("квест фракции «%s» без названия: взято «Поручение»", task.Faction))
		g.Title = "Поручение"
	}
	g.Summary = clip(q.Summary, model.MaxQuestSummaryRunes)
	minStages, maxStages := model.StageBounds(task.Kind)
	keys := map[string]bool{}
	for i, st := range q.Stages {
		if len(g.Stages) >= maxStages {
			issues = append(issues, fmt.Sprintf("квест «%s»: этапов больше %d, лишние отброшены", g.Title, maxStages))
			break
		}
		key := normalizeKey(st.Key)
		if key == "" {
			key = fmt.Sprintf("stage_%d", i+1)
		}
		for n := 2; keys[key]; n++ {
			key = fmt.Sprintf("%s_%d", normalizeKey(st.Key), n)
		}
		keys[key] = true
		title := clip(st.Title, model.MaxApproachTitleRunes)
		if title == "" {
			issues = append(issues, fmt.Sprintf("квест «%s»: этап %d без названия, отброшен", g.Title, i+1))
			continue
		}
		kind := model.ConditionKind(strings.TrimSpace(st.Condition))
		if !knownCondition(kind) {
			issues = append(issues, fmt.Sprintf("квест «%s»: условие %q не из каталога, взято visited", g.Title, st.Condition))
			kind = model.ConditionVisited
		}
		// Расстояние 0 — этап там, где группа стоит сейчас (B54): для того, что уже лежит перед персонажами.
		distance := st.Distance
		if distance < 0 || distance > model.MaxStageDistance {
			clamped := max(0, min(distance, model.MaxStageDistance))
			issues = append(issues, fmt.Sprintf("квест «%s»: расстояние %d вне 0–%d, взято %d", g.Title, distance, model.MaxStageDistance, clamped))
			distance = clamped
		}
		g.Stages = append(g.Stages, model.QuestStage{Key: key, Title: title, Summary: clip(st.Summary, model.MaxStageSummaryRunes), Distance: distance,
			Condition: model.StageCondition{Kind: kind}, Reward: model.StageReward{Fact: clip(st.Reward, model.MaxFactRunes)}})
	}
	if n := len(g.Stages); n > 0 && n < minStages {
		issues = append(issues, fmt.Sprintf("квест «%s»: этапов %d, меньше %d", g.Title, n, minStages))
	}
	if len(g.Stages) == 0 {
		issues = append(issues, fmt.Sprintf("квест «%s» без этапов: отброшен", g.Title))
	}
	return g, issues
}

func knownCondition(k model.ConditionKind) bool {
	for _, c := range model.ConditionKinds {
		if c == k {
			return true
		}
	}
	return false
}

// validateStages принимает размещения этапов по заданиям: объекты по правилам пакета мира, ключи не повторяют
// объекты клетки; условие принимается, только если достижимо подходом клетки (`object_state` — подход ставит
// объекту это состояние; `fact` — подход даёт этот факт), иначе первое достижимое. Если достижимого условия нет, а
// этап по заданию не «дойти до» (`visited`), размещение закрывает шаблонный «след» с подходом и фактом (B54): иначе
// этап закрывался бы простым входом в клетку. Пропущенное задание — тот же шаблон.
func validateStages(raw []rawStagePlacement, tasks []StageTask, def model.CharacterModelDef) ([]GeneratedStage, []string) {
	var out []GeneratedStage
	var issues []string
	type key struct {
		quest model.QuestID
		stage string
	}
	byKey := map[key]rawStagePlacement{}
	for _, p := range raw {
		k := key{model.QuestID(p.QuestID), normalizeKey(p.StageKey)}
		if _, dup := byKey[k]; !dup {
			byKey[k] = p
		}
	}
	wanted := map[key]bool{}
	for _, task := range tasks {
		k := key{task.QuestID, normalizeKey(task.StageKey)}
		wanted[k] = true
		p, ok := byKey[k]
		if !ok {
			issues = append(issues, fmt.Sprintf("этап «%s» квеста %d не размещён: шаблон", task.StageTitle, task.QuestID))
			out = append(out, templateStage(task))
			continue
		}
		objects, more := validateObjects(p.Objects, task.CellName, def)
		issues = append(issues, more...)
		objects = dropTaken(objects, task.Existing, &issues)
		all := append(append([]model.LocationObject{}, task.Existing...), objects...)
		cond, condIssues := validateCondition(p.Condition, all, task)
		issues = append(issues, condIssues...)
		template := false
		if cond.Kind == model.ConditionVisited && task.ConditionKind != model.ConditionVisited {
			issues = append(issues, fmt.Sprintf("этап «%s»: вместо visited добавлен шаблонный след с фактом", task.StageTitle))
			var more []model.LocationObject
			more, cond = quest.TemplateStage(task.Stage, all)
			objects = append(objects, more...)
			template = true
		}
		out = append(out, GeneratedStage{QuestID: task.QuestID, Stage: task.Stage, LocationID: task.LocationID, Objects: objects, Condition: cond, Template: template})
	}
	for k := range byKey {
		if !wanted[k] {
			issues = append(issues, fmt.Sprintf("размещение этапа «%s» квеста %d не из задания: отброшено", k.stage, k.quest))
		}
	}
	return out, issues
}

// dropTaken отбрасывает новые объекты с ключами, которые уже есть в клетке.
func dropTaken(objects, existing []model.LocationObject, issues *[]string) []model.LocationObject {
	taken := map[string]bool{}
	for _, o := range existing {
		taken[o.Key] = true
	}
	out := objects[:0:0]
	for _, o := range objects {
		if taken[o.Key] {
			*issues = append(*issues, fmt.Sprintf("объект «%s» уже есть в клетке: отброшен", o.Key))
			continue
		}
		out = append(out, o)
	}
	return out
}

func templateStage(task StageTask) GeneratedStage {
	objects, cond := quest.TemplateStage(task.Stage, task.Existing)
	return GeneratedStage{QuestID: task.QuestID, Stage: task.Stage, LocationID: task.LocationID, Objects: objects, Condition: cond, Template: true}
}

// validateCondition — условие размещённого этапа по достижимости подходами клетки.
func validateCondition(c rawCondition, objects []model.LocationObject, task StageTask) (model.StageCondition, []string) {
	var issues []string
	kind := model.ConditionKind(strings.TrimSpace(c.Kind))
	switch kind {
	case model.ConditionObjectState:
		if c.Object != nil && c.State != nil {
			key, state := normalizeKey(*c.Object), clip(*c.State, 20)
			if reachableState(objects, key, state) {
				return model.StageCondition{Kind: kind, Object: key, State: state}, nil
			}
		}
		issues = append(issues, fmt.Sprintf("этап «%s»: состояние объекта недостижимо подходами клетки", task.StageTitle))
	case model.ConditionFact:
		if c.Fact != nil {
			fact := clip(*c.Fact, model.MaxFactRunes)
			if fact != "" && reachableFact(objects, fact) {
				return model.StageCondition{Kind: kind, Fact: fact}, nil
			}
		}
		issues = append(issues, fmt.Sprintf("этап «%s»: факт не даёт ни один подход клетки", task.StageTitle))
	case model.ConditionVisited:
		return model.StageCondition{Kind: kind}, nil
	default:
		issues = append(issues, fmt.Sprintf("этап «%s»: условие %q не из каталога", task.StageTitle, c.Kind))
	}
	if cond, ok := firstReachable(objects); ok {
		issues = append(issues, fmt.Sprintf("этап «%s»: взято первое достижимое условие", task.StageTitle))
		return cond, issues
	}
	issues = append(issues, fmt.Sprintf("этап «%s»: достижимых условий нет, взято visited", task.StageTitle))
	return model.StageCondition{Kind: model.ConditionVisited}, issues
}

// effects — эффекты успеха подхода: объект (пусто — сам), состояние, факт.
func effects(ap model.Approach) (object, state, fact string) {
	if len(ap.OnSuccess) == 0 {
		return "", "", ""
	}
	var e struct {
		Object string `json:"object"`
		State  string `json:"state"`
		Fact   string `json:"fact"`
	}
	_ = json.Unmarshal(ap.OnSuccess, &e)
	return e.Object, e.State, e.Fact
}

func reachableState(objects []model.LocationObject, key, state string) bool {
	for _, o := range objects {
		for _, ap := range o.Approaches {
			obj, st, _ := effects(ap)
			if obj == "" {
				obj = o.Key
			}
			if obj == key && st != "" && strings.EqualFold(st, state) {
				return true
			}
		}
	}
	return false
}

func reachableFact(objects []model.LocationObject, fact string) bool {
	for _, o := range objects {
		for _, ap := range o.Approaches {
			if _, _, f := effects(ap); f == fact {
				return true
			}
		}
	}
	return false
}

// firstReachable — первое достижимое условие по подходам клетки: состояние объекта, иначе факт.
func firstReachable(objects []model.LocationObject) (model.StageCondition, bool) {
	for _, o := range objects {
		for _, ap := range o.Approaches {
			obj, st, _ := effects(ap)
			if obj == "" {
				obj = o.Key
			}
			if st != "" {
				return model.StageCondition{Kind: model.ConditionObjectState, Object: obj, State: st}, true
			}
		}
	}
	for _, o := range objects {
		for _, ap := range o.Approaches {
			if _, _, f := effects(ap); f != "" {
				return model.StageCondition{Kind: model.ConditionFact, Fact: f}, true
			}
		}
	}
	return model.StageCondition{}, false
}

// validateGoals принимает цели по заданиям: текст обязателен (иначе шаблон), стадия необязательна.
func validateGoals(raw []rawGoal, tasks []GoalTask) ([]GeneratedGoal, []string) {
	var out []GeneratedGoal
	var issues []string
	byFaction := map[string]rawGoal{}
	for _, g := range raw {
		code := strings.ToLower(strings.TrimSpace(g.Faction))
		if _, dup := byFaction[code]; !dup {
			byFaction[code] = g
		}
	}
	for _, task := range tasks {
		g, ok := byFaction[strings.ToLower(task.Faction)]
		text := clip(g.Text, model.MaxGoalRunes)
		if !ok || text == "" {
			issues = append(issues, fmt.Sprintf("цель фракции «%s» не сформулирована: шаблон", task.Faction))
			out = append(out, templateGoal(task))
			continue
		}
		out = append(out, GeneratedGoal{FactionID: task.FactionID, Text: text, Stage: clip(g.Stage, model.MaxGoalStageRunes), QuestID: task.QuestID})
	}
	return out, issues
}

func templateGoal(task GoalTask) GeneratedGoal {
	g := quest.TemplateGoal(model.Faction{ID: task.FactionID, Description: task.Current}, task.Fallback)
	stage := "начало"
	if task.Closed != "" {
		stage = "после «" + task.Closed + "»"
	}
	return GeneratedGoal{FactionID: task.FactionID, Text: g.Text, Stage: stage, QuestID: task.QuestID, Template: true}
}

// TemplateQuests закрывает задания квестов без модели: обязательные сюжетные — шаблон жанра, побочные ждут,
// этапы — объект «след», цели — шаблон.
func TemplateQuests(tasks Tasks) ([]GeneratedQuest, []GeneratedStage, []GeneratedGoal) {
	var quests []GeneratedQuest
	for _, task := range tasks.Quests {
		if q, ok := templateQuest(task, tasks.StoryTemplate); ok {
			quests = append(quests, q)
		}
	}
	var stages []GeneratedStage
	for _, task := range tasks.Stages {
		stages = append(stages, templateStage(task))
	}
	var goals []GeneratedGoal
	for _, task := range tasks.Goals {
		goals = append(goals, templateGoal(task))
	}
	return quests, stages, goals
}

// ConditionWordsFor — условие этапа словами для контекста рассказчика и `/quests`.
func ConditionWordsFor(c model.StageCondition, objects []model.LocationObject) string {
	switch c.Kind {
	case model.ConditionObjectState:
		title := c.Object
		for _, o := range objects {
			if o.Key == c.Object {
				title = o.Title
			}
		}
		return fmt.Sprintf("«%s» должен оказаться в состоянии «%s»", title, StateWords(c.State))
	case model.ConditionFact:
		return "узнать: " + c.Fact
	case model.ConditionVisited:
		return "добраться до места"
	}
	return ""
}
