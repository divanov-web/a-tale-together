package model

import (
	"encoding/json"
	"time"
)

// ActionType — тип действия из каталога (docs/game/actions.md).
type ActionType string

// Типы действий каталога. В пункте 07 разрешаются move, explore, interact, wait, help; остальные — поздние пункты.
const (
	ActionMove     ActionType = "move"
	ActionExplore  ActionType = "explore"
	ActionInteract ActionType = "interact"
	ActionPersuade ActionType = "persuade"
	ActionAttack   ActionType = "attack"
	ActionHelp     ActionType = "help"
	ActionSneak    ActionType = "sneak"
	ActionWait     ActionType = "wait"
	ActionTrade    ActionType = "trade"
)

// Risk — оценка риска свободного действия; число успехов назначает таблица правил.
type Risk string

// Уровни риска.
const (
	RiskNormal  Risk = "normal"
	RiskHard    Risk = "hard"
	RiskExtreme Risk = "extreme"
)

// Intent — разобранное намерение персонажа на раунд: выход интерпретатора (пункт 08) или типовой команды.
type Intent struct {
	SubmissionID SubmissionID `json:"submission_id"`
	CharacterID  CharacterID  `json:"character_id"`
	ActionType   ActionType   `json:"action_type"`
	Target       string       `json:"target,omitempty"`       // ключ объекта, имя выхода или локации, никнейм персонажа
	ApproachIdx  *int         `json:"approach_idx,omitempty"` // выбранный подход объекта; nil — по типу действия
	Risk         Risk         `json:"risk,omitempty"`         // для свободного действия без подхода
	Push         bool         `json:"push,omitempty"`         // форсировать бросок при провале
	How          string       `json:"how,omitempty"`          // свободное описание способа
	Ambiguities  []string     `json:"ambiguities,omitempty"`  // непустой список → заявка ambiguous
	RawText      string       `json:"raw_text,omitempty"`     // исходный текст игрока (данные)
}

// Visibility — кому доступно событие.
type Visibility string

// Уровни видимости событий.
const (
	VisibilityPublic   Visibility = "public"   // всем игрокам мира
	VisibilityLocation Visibility = "location" // присутствующим в локации события
	VisibilityPrivate  Visibility = "private"  // только персонажам из VisibleTo
)

// EventType — тип события мира.
type EventType string

// Типы событий пункта 07.
const (
	EventCharacterMoved    EventType = "character_moved"
	EventCheckRolled       EventType = "check_rolled"
	EventObjectChanged     EventType = "object_changed"
	EventFactLearned       EventType = "fact_learned"
	EventConditionAdded    EventType = "condition_added"
	EventConditionRemoved  EventType = "condition_removed"
	EventHPChanged         EventType = "hp_changed"
	EventCharacterWaited   EventType = "character_waited"
	EventHelpGiven         EventType = "help_given"
	EventActionRejected    EventType = "action_rejected"
	EventCharacterDied     EventType = "character_died"
	EventCharacterReturned EventType = "character_returned"
)

// Event — событие мира: единственный способ изменить состояние (docs/architecture/data-model.md, `world_events`).
type Event struct {
	ID          int64           `json:"-"`
	GameID      GameID          `json:"-"`
	RoundID     RoundID         `json:"-"`
	Seq         int64           `json:"seq"` // сквозной номер в игре; движок нумерует с 1 внутри раунда, сценарий сдвигает
	Type        EventType       `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	Visibility  Visibility      `json:"visibility"`
	VisibleTo   []CharacterID   `json:"visible_to,omitempty"`
	LocationID  *LocationID     `json:"location_id,omitempty"` // для видимости location
	CharacterID *CharacterID    `json:"character_id,omitempty"`
	CreatedAt   time.Time       `json:"-"`
}

// CheckInfo — бросок проверки, записывается в событие check_rolled и итог заявки.
type CheckInfo struct {
	Attribute  string `json:"attribute"`
	Skill      string `json:"skill,omitempty"`
	Pool       int    `json:"pool"`
	Dice       []int  `json:"dice"`
	Successes  int    `json:"successes"`
	Difficulty int    `json:"difficulty"` // итоговая: подход + локация
	Pushed     bool   `json:"pushed"`
	PushDice   []int  `json:"push_dice,omitempty"`
	Outcome    string `json:"outcome"` // success | costly_success | failure
	ChancePct  int    `json:"chance_pct"`
}

// Исходы проверки.
const (
	OutcomeSuccess       = "success"
	OutcomeCostlySuccess = "costly_success"
	OutcomeFailure       = "failure"
)

// ResultStatus — статус заявки после разрешения; ровно один на заявку.
type ResultStatus string

// Статусы разрешения.
const (
	ResultResolved  ResultStatus = "resolved"
	ResultRejected  ResultStatus = "rejected"
	ResultDeferred  ResultStatus = "deferred"
	ResultAmbiguous ResultStatus = "ambiguous"
)

// ActionResult — итог заявки: статус, исход, причина, ссылки на события.
type ActionResult struct {
	SubmissionID SubmissionID `json:"submission_id"`
	CharacterID  CharacterID  `json:"character_id"`
	Intent       Intent       `json:"intent"`
	Status       ResultStatus `json:"status"`
	Outcome      string       `json:"outcome,omitempty"` // для resolved с проверкой
	Reason       string       `json:"reason,omitempty"`  // для rejected / ambiguous / deferred, текст игроку
	Summary      string       `json:"summary"`           // краткий итог словами (шаблон, без LLM)
	Check        *CheckInfo   `json:"check,omitempty"`
	EventSeqs    []int64      `json:"event_seqs"`
}

// LocationLink — выход из локации.
type LocationLink struct {
	To     string `json:"to"` // имя локации
	Title  string `json:"title"`
	Hidden bool   `json:"hidden,omitempty"`
}

// Approach — предлагаемый подход к объекту (docs/game/models/location.md).
type Approach struct {
	Action     ActionType      `json:"action"`
	Attribute  string          `json:"attribute,omitempty"`
	Skill      string          `json:"skill,omitempty"`
	Difficulty int             `json:"difficulty"`
	Title      string          `json:"title,omitempty"`
	OnSuccess  json.RawMessage `json:"on_success,omitempty"` // {"state": "open"} | {"object": "key", "state": "open"} | {"reveal": "key"} | {"fact": "…"}
	OnFail     json.RawMessage `json:"on_fail,omitempty"`    // {"condition": "exhausted"}
	Deadly     bool            `json:"deadly,omitempty"`     // провал не заменяется «успехом с ценой» в режиме повествования
}

// LocationObject — объект локации.
type LocationObject struct {
	Key         string     `json:"key"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	State       string     `json:"state,omitempty"`
	Revealed    bool       `json:"revealed,omitempty"`
	HiddenUntil bool       `json:"hidden_until_revealed,omitempty"` // объект не виден до события reveal
	Approaches  []Approach `json:"approaches,omitempty"`
}

// Location — место мира с выходами и объектами.
type Location struct {
	ID          LocationID
	GameID      GameID
	Name        string
	Description string
	Difficulty  int
	Links       []LocationLink
	Objects     []LocationObject
	Active      bool
	Start       bool // стартовая локация персонажей
	CreatedAt   time.Time
}

// LocationTemplate — локация в шаблоне жанра.
type LocationTemplate struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Difficulty  int              `json:"difficulty"`
	Links       []LocationLink   `json:"links"`
	Objects     []LocationObject `json:"objects"`
	Start       bool             `json:"start,omitempty"`
}

// SnapshotLocation — локация в снимке раунда.
type SnapshotLocation struct {
	ID          LocationID       `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Difficulty  int              `json:"difficulty"`
	Links       []LocationLink   `json:"links"`
	Objects     []LocationObject `json:"objects"`
}

// Object возвращает объект локации по ключу.
func (l SnapshotLocation) Object(key string) (LocationObject, int, bool) {
	for i, o := range l.Objects {
		if o.Key == key {
			return o, i, true
		}
	}
	return LocationObject{}, -1, false
}

// DeathMode — режим смерти персонажа (docs/game/death.md).
type DeathMode string

// Режимы смерти; реализуется только fake.
const (
	DeathNone DeathMode = "none"
	DeathFake DeathMode = "fake"
	DeathReal DeathMode = "real"
)

// DeathDaysDefault — срок выбытия при ненастоящей смерти, дней; настройка разработчика.
const DeathDaysDefault = 10

// Состояния персонажа (docs/game/character.md): штраф −1 кубик к проверкам.
const (
	ConditionWounded   = "wounded"
	ConditionExhausted = "exhausted"
	ConditionHidden    = "hidden"
	ConditionDetained  = "detained"
)
