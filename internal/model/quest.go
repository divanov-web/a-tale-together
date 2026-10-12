package model

import "time"

// Пункт 28 (docs/game/world.md «Квесты», ADR-0020): мир порождает цели фракций и квесты с проверяемым прогрессом.
// Квест и этапы создаёт рассказчик по заданию кода, завершение считает код по условиям из каталога.

// QuestKind — тип квеста: сюжетный связан с целью фракции, побочный короче.
type QuestKind string

// Типы квестов.
const (
	QuestStory QuestKind = "story"
	QuestSide  QuestKind = "side"
)

// QuestStatus — статус квеста.
type QuestStatus string

// Статусы квеста.
const (
	QuestActive QuestStatus = "active"
	QuestDone   QuestStatus = "done"
)

// StageStatus — статус этапа: без клетки, привязан к клетке, закрыт.
type StageStatus string

// Статусы этапа.
const (
	StagePending StageStatus = "pending"
	StageBound   StageStatus = "bound"
	StageDone    StageStatus = "done"
)

// ConditionKind — вид условия-результата этапа; проверяет код (`service/quest.Check`), выражений от модели нет.
type ConditionKind string

// Каталог условий этапа; `item_owned` и `npc_state` — пункт 30.
const (
	ConditionObjectState ConditionKind = "object_state" // объект клетки этапа в состоянии State
	ConditionFact        ConditionKind = "fact"         // факт Fact знает хотя бы один персонаж фракции
	ConditionVisited     ConditionKind = "visited"      // клетка этапа посещена
)

// ConditionKinds — каталог в порядке вывода модели.
var ConditionKinds = []ConditionKind{ConditionObjectState, ConditionFact, ConditionVisited}

// StageCondition — условие завершения этапа (`quest_stages.condition`). До привязки известен только вид.
type StageCondition struct {
	Kind   ConditionKind `json:"kind"`
	Object string        `json:"object,omitempty"` // ключ объекта клетки этапа (object_state)
	State  string        `json:"state,omitempty"`  // требуемое состояние (object_state)
	Fact   string        `json:"fact,omitempty"`   // требуемый факт (fact)
}

// StageReward — награда за этап (`quest_stages.reward`): факт персонажам фракции; предмет — пункт 30.
type StageReward struct {
	Fact string `json:"fact,omitempty"`
}

// Quest — квест фракции с этапами по порядку.
type Quest struct {
	ID             QuestID
	GameID         GameID
	FactionID      FactionID
	Kind           QuestKind
	Status         QuestStatus
	Title          string
	Summary        string
	CreatedRoundID *RoundID // nil — создан завязкой
	DoneRoundID    *RoundID
	Stages         []QuestStage
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Current — первый незакрытый этап квеста.
func (q Quest) Current() (*QuestStage, bool) {
	for i := range q.Stages {
		if q.Stages[i].Status != StageDone {
			return &q.Stages[i], true
		}
	}
	return nil, false
}

// Previous — последний закрытый этап перед первым незакрытым.
func (q Quest) Previous() (*QuestStage, bool) {
	var prev *QuestStage
	for i := range q.Stages {
		if q.Stages[i].Status != StageDone {
			break
		}
		prev = &q.Stages[i]
	}
	return prev, prev != nil
}

// QuestStage — этап квеста: смысл, расстояние от предыдущего, условие, награда, клетка после привязки.
type QuestStage struct {
	ID          QuestStageID
	GameID      GameID
	QuestID     QuestID
	StageNo     int
	Key         string
	Title       string
	Summary     string
	Distance    int // клеток от клетки предыдущего этапа (первого — от стартовой), по сетке
	Condition   StageCondition
	Reward      StageReward
	LocationID  *LocationID // клетка после привязки
	Status      StageStatus
	DoneRoundID *RoundID
}

// FactionGoal — цель фракции: формулировка и текущая стадия; каждая версия — своя строка.
type FactionGoal struct {
	ID        FactionGoalID
	GameID    GameID
	FactionID FactionID
	Version   int
	Text      string
	Stage     string
	QuestID   *QuestID // сюжетный квест, по закрытию которого цель пересмотрена; nil у первой версии
	RoundID   *RoundID
	CreatedAt time.Time
}

// QuestTemplate — сюжетный квест шаблона жанра на случай сбоя модели (`genres.template.story_quest`).
type QuestTemplate struct {
	Title   string          `json:"title"`
	Summary string          `json:"summary"`
	Stages  []StageTemplate `json:"stages"`
}

// StageTemplate — этап шаблонного квеста.
type StageTemplate struct {
	Key       string        `json:"key"`
	Title     string        `json:"title"`
	Summary   string        `json:"summary"`
	Distance  int           `json:"distance"`
	Condition ConditionKind `json:"condition"`
	Reward    string        `json:"reward,omitempty"`
}

// Лимиты квестов (docs/game/world.md «Квесты»).
const (
	MaxStoryQuestsDefault = 2 // активных сюжетных на фракцию (settings.max_story_quests)
	MaxSideQuestsDefault  = 1 // активных побочных на фракцию (settings.max_side_quests)
	MinStoryStages        = 3
	MaxStoryStages        = 6
	MinSideStages         = 2
	MaxSideStages         = 4
	MaxStageDistance      = 3
	MaxQuestTitleRunes    = 80
	MaxQuestSummaryRunes  = 300
	MaxStageSummaryRunes  = 200
	MaxGoalRunes          = 300
	MaxGoalStageRunes     = 200
)

// StageBounds — допустимое число этапов по типу квеста.
func StageBounds(kind QuestKind) (minStages, maxStages int) {
	if kind == QuestSide {
		return MinSideStages, MaxSideStages
	}
	return MinStoryStages, MaxStoryStages
}

// IssueThreshold — сколько новых клеток, открытых фракцией, нужно для следующего квеста при данном числе активных
// (docs/game/world.md «Выдача по исследованию»); 0 — выдача закрыта лимитом.
func IssueThreshold(active int) int {
	switch {
	case active <= 1:
		return 1
	case active == 2:
		return 4
	}
	return 0
}

// Шаблонный объект этапа при сбое модели: подход без броска даёт факт, условие — этот факт.
const (
	TemplateStageObjectPrefix = "trace_"
	TemplateStageTitlePrefix  = "след: "
	TemplateStageApproach     = "разобраться на месте"
)
