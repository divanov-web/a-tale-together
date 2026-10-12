// Package quest — чистые правила квестов и целей фракций (пункт 28, docs/game/world.md «Квесты», ADR-0020):
// планирование заданий на генерацию (новый квест по счётчику открытых клеток, привязка этапа к клетке по
// расстоянию, пересмотр цели), проверка условий этапов по состоянию после свёртки событий и шаблоны при сбое
// модели. Без I/O: вход — снимок, квесты, фракции; выход — задания и закрытия. Строки и события пишет `app/round`.
package quest

import (
	"fmt"
	"sort"
	"strings"

	"taletogether/internal/model"
)

// Input — данные для планирования: фракции со счётчиками после открытий раунда, все квесты игры, текущие цели,
// состояние после свёртки, все клетки игры (клетка прошлого этапа может быть вне снимка), лимиты и шаблон жанра.
type Input struct {
	Factions   []model.Faction
	Quests     []model.Quest
	Goals      []model.FactionGoal
	State      *model.RoundSnapshot
	Locations  []model.Location
	StoryLimit int
	SideLimit  int
	Template   *model.QuestTemplate
	GoalTexts  map[model.FactionID]string // цель по умолчанию из шаблона жанра; пусто — описание фракции
}

// IssueTask — задание «новый квест фракции»: тип, порог, который спишется со счётчика после принятия, и
// обязательность (сюжетный при нуле активных сюжетных, вне счётчика).
type IssueTask struct {
	FactionID model.FactionID
	Kind      model.QuestKind
	Threshold int
	Mandatory bool
}

// BindTask — задание «разместить этап в клетке»: первый незакрытый этап квеста без клетки и клетка, где стоит
// персонаж фракции на нужном расстоянии от клетки предыдущего этапа.
type BindTask struct {
	Quest      model.Quest
	Stage      model.QuestStage
	LocationID model.LocationID
}

// GoalTask — задание «пересмотреть цель»: текущая версия (пустая у фракции без цели) и закрытый сюжетный квест.
type GoalTask struct {
	FactionID model.FactionID
	Current   model.FactionGoal
	Closed    *model.Quest
}

// Tasks — задания раунда по квестам.
type Tasks struct {
	Issue []IssueTask
	Bind  []BindTask
	Goals []GoalTask
}

// Empty — заданий нет.
func (t Tasks) Empty() bool { return len(t.Issue) == 0 && len(t.Bind) == 0 && len(t.Goals) == 0 }

// Plan планирует задания по фракциям: нет активного сюжетного → сюжетный сразу; иначе по порогу
// `model.IssueThreshold` от числа активных и счётчику `ExploredSinceReset`, тип по свободным слотам с чередованием
// сюжетный → побочный → сюжетный; не больше одного задания на фракцию. Привязка: первый незакрытый этап без
// клетки получает клетку присутствующего персонажа фракции на расстоянии ≥ `Distance` от клетки предыдущего этапа
// (у первого — от стартовой); клетка одна на этап, первая по порядку снимка. Цель: фракция без цели или сюжетный
// квест закрыт после текущей версии цели.
func Plan(in Input) Tasks {
	var t Tasks
	coords := map[model.LocationID][2]int{}
	start := [2]int{}
	for _, l := range in.Locations {
		coords[l.ID] = [2]int{l.X, l.Y}
		if l.Start {
			start = [2]int{l.X, l.Y}
		}
	}
	if in.State != nil {
		for _, l := range in.State.Locations {
			coords[l.ID] = [2]int{l.X, l.Y}
			if l.Start {
				start = [2]int{l.X, l.Y}
			}
		}
	}
	for _, f := range in.Factions {
		active, story, side, last := factionQuests(in.Quests, f.ID)
		if task, ok := issue(f, active, story, side, last, in.StoryLimit, in.SideLimit); ok {
			t.Issue = append(t.Issue, task)
		}
		for _, q := range in.Quests {
			if q.FactionID != f.ID || q.Status != model.QuestActive {
				continue
			}
			cur, ok := q.Current()
			if !ok || cur.Status != model.StagePending {
				continue
			}
			anchor := start
			if prev, ok := q.Previous(); ok && prev.LocationID != nil {
				if xy, ok := coords[*prev.LocationID]; ok {
					anchor = xy
				}
			}
			if loc, ok := candidate(in.State, f.ID, anchor, cur.Distance); ok {
				t.Bind = append(t.Bind, BindTask{Quest: q, Stage: *cur, LocationID: loc})
			}
		}
		if task, ok := goal(in.Goals, in.Quests, f.ID); ok {
			t.Goals = append(t.Goals, task)
		}
	}
	return t
}

// factionQuests — активные квесты фракции по типам и тип последнего выданного.
func factionQuests(quests []model.Quest, f model.FactionID) (active, story, side int, last model.QuestKind) {
	var lastID model.QuestID
	for _, q := range quests {
		if q.FactionID != f {
			continue
		}
		if q.ID > lastID {
			lastID, last = q.ID, q.Kind
		}
		if q.Status != model.QuestActive {
			continue
		}
		active++
		if q.Kind == model.QuestStory {
			story++
		} else {
			side++
		}
	}
	return active, story, side, last
}

func issue(f model.Faction, active, story, side int, last model.QuestKind, storyLimit, sideLimit int) (IssueTask, bool) {
	if story == 0 {
		return IssueTask{FactionID: f.ID, Kind: model.QuestStory, Mandatory: true}, true
	}
	if active >= storyLimit+sideLimit {
		return IssueTask{}, false
	}
	th := model.IssueThreshold(active)
	if th <= 0 || f.ExploredSinceReset < th {
		return IssueTask{}, false
	}
	kind := model.QuestStory
	switch {
	case side < sideLimit && (last == model.QuestStory || story >= storyLimit):
		kind = model.QuestSide
	case story < storyLimit:
		kind = model.QuestStory
	case side < sideLimit:
		kind = model.QuestSide
	default:
		return IssueTask{}, false
	}
	return IssueTask{FactionID: f.ID, Kind: kind, Threshold: th}, true
}

// candidate — первая по порядку снимка клетка с персонажем фракции на расстоянии ≥ distance от anchor.
func candidate(state *model.RoundSnapshot, f model.FactionID, anchor [2]int, distance int) (model.LocationID, bool) {
	if state == nil {
		return 0, false
	}
	for _, l := range state.Locations {
		if Distance(anchor, [2]int{l.X, l.Y}) < distance {
			continue
		}
		for _, c := range state.Characters {
			if c.FactionID == f && c.LocationID != nil && *c.LocationID == l.ID && !c.Status.Absent() {
				return l.ID, true
			}
		}
	}
	return 0, false
}

// Distance — расстояние между клетками по сетке (сумма модулей разностей координат).
func Distance(a, b [2]int) int {
	dx, dy := a[0]-b[0], a[1]-b[1]
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

func goal(goals []model.FactionGoal, quests []model.Quest, f model.FactionID) (GoalTask, bool) {
	var cur *model.FactionGoal
	for i := range goals {
		if goals[i].FactionID == f {
			cur = &goals[i]
		}
	}
	var closed *model.Quest
	for i := range quests {
		q := &quests[i]
		if q.FactionID != f || q.Kind != model.QuestStory || q.Status != model.QuestDone {
			continue
		}
		if closed == nil || q.ID > closed.ID {
			closed = q
		}
	}
	switch {
	case cur == nil:
		return GoalTask{FactionID: f, Closed: closed}, true
	case closed != nil && (cur.QuestID == nil || *cur.QuestID != closed.ID):
		return GoalTask{FactionID: f, Current: *cur, Closed: closed}, true
	}
	return GoalTask{}, false
}

// Done — закрытие этапа по условию; QuestDone — это был последний этап.
type Done struct {
	Quest     model.Quest
	Stage     model.QuestStage
	QuestDone bool
}

// Check проверяет условия привязанных этапов по состоянию после свёртки: `object_state` — объект клетки этапа в
// состоянии (клетка должна быть в снимке: состояние меняется только в занятой клетке); `fact` — факт знает хотя бы
// один персонаж фракции; `visited` — клетка этапа посещена. Закрывается только первый незакрытый этап квеста.
func Check(state *model.RoundSnapshot, quests []model.Quest) []Done {
	var out []Done
	if state == nil {
		return out
	}
	for _, q := range quests {
		if q.Status != model.QuestActive {
			continue
		}
		cur, ok := q.Current()
		if !ok || cur.Status != model.StageBound || cur.LocationID == nil {
			continue
		}
		if !satisfied(state, q.FactionID, *cur) {
			continue
		}
		out = append(out, Done{Quest: q, Stage: *cur, QuestDone: cur.StageNo == len(q.Stages)})
	}
	return out
}

func satisfied(state *model.RoundSnapshot, f model.FactionID, st model.QuestStage) bool {
	switch st.Condition.Kind {
	case model.ConditionVisited:
		l, ok := state.Location(*st.LocationID)
		return ok && l.Visited
	case model.ConditionObjectState:
		l, ok := state.Location(*st.LocationID)
		if !ok {
			return false
		}
		o, _, ok := l.Object(st.Condition.Object)
		return ok && strings.EqualFold(o.State, st.Condition.State)
	case model.ConditionFact:
		for _, c := range state.Characters {
			if c.FactionID != f {
				continue
			}
			for _, k := range c.Knowledge {
				if k == st.Condition.Fact {
					return true
				}
			}
		}
	}
	return false
}

// TemplateQuest — сюжетный квест из шаблона жанра для фракции (сбой модели или отсутствие провайдера).
func TemplateQuest(t *model.QuestTemplate, f model.Faction) model.Quest {
	q := model.Quest{GameID: f.GameID, FactionID: f.ID, Kind: model.QuestStory, Status: model.QuestActive}
	if t == nil || len(t.Stages) == 0 {
		q.Title, q.Summary = "Первые шаги", "Осмотреться в окрестностях и понять, что здесь происходит."
		q.Stages = []model.QuestStage{
			{Key: "lookout", Title: "Осмотреться", Summary: "Найти место, откуда видно окрестности.", Distance: 1, Condition: model.StageCondition{Kind: model.ConditionVisited}},
			{Key: "clue", Title: "Найти зацепку", Summary: "Узнать, что тревожит здешних.", Distance: 1, Condition: model.StageCondition{Kind: model.ConditionFact}},
			{Key: "answer", Title: "Разобраться", Summary: "Дойти до источника тревоги.", Distance: 2, Condition: model.StageCondition{Kind: model.ConditionObjectState}},
		}
		return q
	}
	q.Title, q.Summary = t.Title, t.Summary
	for i, st := range t.Stages {
		kind := st.Condition
		if kind == "" {
			kind = model.ConditionVisited
		}
		key := st.Key
		if key == "" {
			key = fmt.Sprintf("stage_%d", i+1)
		}
		q.Stages = append(q.Stages, model.QuestStage{Key: key, Title: st.Title, Summary: st.Summary, Distance: max(1, min(st.Distance, model.MaxStageDistance)),
			Condition: model.StageCondition{Kind: kind}, Reward: model.StageReward{Fact: st.Reward}})
	}
	return q
}

// TemplateGoal — цель фракции по умолчанию: текст шаблона жанра, иначе описание фракции.
func TemplateGoal(f model.Faction, text string) model.FactionGoal {
	if strings.TrimSpace(text) == "" {
		text = f.Description
	}
	if strings.TrimSpace(text) == "" {
		text = "Укрепить положение своих в этих краях."
	}
	return model.FactionGoal{GameID: f.GameID, FactionID: f.ID, Text: text, Stage: "начало"}
}

// TemplateStage — размещение этапа без модели: объект «след» с подходом без броска, который даёт факт этапа;
// условие — этот факт. Ключ объекта не повторяет ключи клетки.
func TemplateStage(st model.QuestStage, existing []model.LocationObject) ([]model.LocationObject, model.StageCondition) {
	key := model.TemplateStageObjectPrefix + st.Key
	taken := map[string]bool{}
	for _, o := range existing {
		taken[o.Key] = true
	}
	for i := 2; taken[key]; i++ {
		key = fmt.Sprintf("%s%s_%d", model.TemplateStageObjectPrefix, st.Key, i)
	}
	fact := strings.TrimSpace(st.Summary)
	if fact == "" {
		fact = st.Title
	}
	fact = "Этап «" + st.Title + "»: " + fact
	obj := model.LocationObject{Key: key, Title: model.TemplateStageTitlePrefix + strings.ToLower(st.Title), Description: st.Summary,
		Approaches: []model.Approach{{Action: model.ActionExplore, Title: model.TemplateStageApproach, OnSuccess: []byte(`{"fact":` + quote(fact) + `}`)}}}
	return []model.LocationObject{obj}, model.StageCondition{Kind: model.ConditionFact, Fact: fact}
}

func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// Sort упорядочивает квесты по идентификатору для стабильного вывода.
func Sort(quests []model.Quest) {
	sort.Slice(quests, func(i, j int) bool { return quests[i].ID < quests[j].ID })
}
