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
// Свободное испытание (docs/llm/round-calls.md): без подхода объекта модель назначает характеристику, навык, риск
// и эффекты обеих ветвей; сложность и бросок — движок. Пустой ActionType с Ambiguities → заявка ambiguous;
// непустой тип с Ambiguities — безопасное толкование, разрешается как обычное действие.
type Intent struct {
	SubmissionID SubmissionID    `json:"submission_id"`
	CharacterID  CharacterID     `json:"character_id"`
	ActionType   ActionType      `json:"action_type"`
	Target       string          `json:"target,omitempty"`       // ключ объекта, имя выхода или локации, никнейм персонажа
	ApproachIdx  *int            `json:"approach_idx,omitempty"` // выбранный подход объекта; nil — по типу действия
	Attribute    string          `json:"attribute,omitempty"`    // свободное испытание: характеристика из модели персонажа
	Skill        string          `json:"skill,omitempty"`        // свободное испытание: навык из модели персонажа
	Risk         Risk            `json:"risk,omitempty"`         // свободное испытание: оценка риска → число успехов по таблице
	OnSuccess    json.RawMessage `json:"on_success,omitempty"`   // свободное испытание: {"fact": …} | {"reveal": key} | {"move": выход}
	OnFail       json.RawMessage `json:"on_fail,omitempty"`      // свободное испытание: {"condition": …} | {"damage": N} | {"fact": …}
	Push         bool            `json:"push,omitempty"`         // форсировать бросок при провале
	How          string          `json:"how,omitempty"`          // свободное описание способа
	Ambiguities  []string        `json:"ambiguities,omitempty"`  // что непонятно в заявке; при пустом ActionType → ambiguous
	RawText      string          `json:"raw_text,omitempty"`     // исходный текст игрока (данные)
}

// FreeCheck — у намерения задано свободное испытание (характеристика без подхода объекта).
func (in Intent) FreeCheck() bool { return in.ApproachIdx == nil && in.Attribute != "" }

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
	EventPartySplit        EventType = "party_split"        // часть группы ушла в другую локацию новой группой (пункт 19)
	EventPartyHeld         EventType = "party_held"         // лимит не дал разделиться: группа идёт за лидером (пункт 19)
	EventCellDiscovered    EventType = "cell_discovered"    // первый вход персонажа фракции в клетку (пункт 27, приватно)
	EventCellGenerated     EventType = "cell_generated"     // клетка создана или детализирована пакетом рассказчика или шаблоном (пункт 27)
	EventObjectCreated     EventType = "object_created"     // объект клетки создан пакетом рассказчика (пункт 27)
	EventQuestIssued       EventType = "quest_issued"       // фракция получила квест (пункт 28, private персонажам фракции)
	EventQuestStagePlaced  EventType = "quest_stage_placed" // этап квеста привязан к клетке (private)
	EventQuestStageDone    EventType = "quest_stage_done"   // условие этапа выполнено; награда `reward.fact` → знания адресатов (private)
	EventQuestDone         EventType = "quest_done"         // последний этап закрыт (private)
	EventGoalRevised       EventType = "goal_revised"       // новая версия цели фракции (private)
)

// GenerationEvents — события сценария раунда вне движка: пакет мира после вызова рассказчика и квесты (пункт 28);
// воспроизведение раунда (`/replay`) их не сравнивает.
var GenerationEvents = map[EventType]bool{EventCellGenerated: true, EventObjectCreated: true,
	EventQuestIssued: true, EventQuestStagePlaced: true, EventQuestStageDone: true, EventQuestDone: true, EventGoalRevised: true}

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

// CheckInfo — проверка «Прямой шанс» (ADR-0014): параметр, сложность, шанс и бросок d100; записывается в событие
// check_rolled и итог заявки.
type CheckInfo struct {
	Attribute  string `json:"attribute"`
	Skill      string `json:"skill,omitempty"`
	Param      int    `json:"param"`
	Difficulty int    `json:"difficulty"` // итоговая: подход + локация
	ChancePct  int    `json:"chance_pct"`
	Roll       int    `json:"roll"` // 1–100; успех при roll ≤ chance_pct
	Pushed     bool   `json:"pushed"`
	PushRoll   int    `json:"push_roll,omitempty"` // повторный бросок при форсировании
	Outcome    string `json:"outcome"`             // success | costly_success | failure
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
	Searched    bool       `json:"searched,omitempty"`              // «ничего полезного»: повторный осмотр без изменения состояния находку не даёт
	Approaches  []Approach `json:"approaches,omitempty"`
}

// Location — клетка сетки мира (docs/game/world.md): координаты от стартовой `(0,0)`, стадия, тип местности, имя
// как подпись (не ключ), объекты из `world_objects`. Выходы по четырём направлениям считает код по соседям.
type Location struct {
	ID          LocationID
	GameID      GameID
	Name        string
	Description string // у `card` — одно предложение карточки соседа
	Difficulty  int
	X, Y        int
	Stage       LocationStage
	Terrain     string
	VisitedAt   *time.Time // первый вход персонажа; nil у непосещённой
	Objects     []LocationObject
	Start       bool // стартовая клетка персонажей, `(0,0)`
	CreatedAt   time.Time
}

// LocationTemplate — стартовая клетка в шаблоне жанра (единственная локация шаблона с пункта 27).
type LocationTemplate struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Difficulty  int              `json:"difficulty"`
	Terrain     string           `json:"terrain,omitempty"`
	Objects     []LocationObject `json:"objects"`
	Start       bool             `json:"start,omitempty"`
}

// SnapshotLocation — клетка в снимке раунда (v4): координаты, стадия, посещение, открывшие фракции, объекты.
type SnapshotLocation struct {
	ID           LocationID       `json:"id"`
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Difficulty   int              `json:"difficulty"`
	X            int              `json:"x"`
	Y            int              `json:"y"`
	Stage        LocationStage    `json:"stage"`
	Terrain      string           `json:"terrain,omitempty"`
	Visited      bool             `json:"visited,omitempty"`
	Start        bool             `json:"start,omitempty"`
	DiscoveredBy []FactionID      `json:"discovered_by,omitempty"` // фракции, чьи персонажи уже входили в клетку
	Objects      []LocationObject `json:"objects"`
}

// Discovered сообщает, открыла ли фракция клетку.
func (l SnapshotLocation) Discovered(f FactionID) bool {
	for _, id := range l.DiscoveredBy {
		if id == f {
			return true
		}
	}
	return false
}

// Exit — выход из клетки по направлению к существующему соседу.
type Exit struct {
	Direction Direction
	To        *SnapshotLocation
}

// Title — подпись выхода: «на север: Лес».
func (e Exit) Title() string {
	return e.Direction.Toward() + ": " + e.To.Name
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
