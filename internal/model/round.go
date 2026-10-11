package model

import (
	"strings"
	"time"
)

// RoundStatus — статус раунда (docs/game/round.md, docs/architecture/round-pipeline.md).
type RoundStatus string

// Статусы раунда. После пункта 05 раунд доходит до `frozen` или `skipped`; разрешение и публикация — пункты 07–09.
const (
	RoundOpen      RoundStatus = "open"      // приём заявок до дедлайна
	RoundFrozen    RoundStatus = "frozen"    // дедлайн прошёл, снимок и seed записаны
	RoundSkipped   RoundStatus = "skipped"   // заявок меньше кворума; заявки переходят в следующий раунд
	RoundResolving RoundStatus = "resolving" // интерпретация и разрешение
	RoundResolved  RoundStatus = "resolved"  // события зафиксированы
	RoundPublished RoundStatus = "published" // тексты в outbox
	RoundFailed    RoundStatus = "failed"    // разрешение не удалось MaxRoundAttempts раз подряд; заявки перенесены в следующий раунд
)

// MaxRoundAttempts — попыток закрыть раунд (заморозка и разрешение одной транзакцией) до статуса `failed` (ADR-0013).
const MaxRoundAttempts = 3

// Round — раунд игры: окно приёма заявок, снимок и seed на дедлайн.
type Round struct {
	ID         RoundID
	GameID     GameID
	SeasonID   SeasonID
	RoundNo    int
	Status     RoundStatus
	OpensAt    time.Time
	DeadlineAt time.Time
	ClosedAt   *time.Time
	Seed       int64
	Snapshot   *RoundSnapshot // nil до заморозки
	Narration  *Narration     // хроника рассказчика (пункт 09); nil до публикации или при шаблонной хронике
	SkipReason string         // причина пропуска или сбоя для /status, например «заявок 1 из 2»
	Attempts   int            // неудачных попыток закрыть раунд (транзакция откатилась); считается вне транзакции (ADR-0013)
	Metrics    *RoundMetrics  // метрики разрешения (пункт 10); nil до публикации
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// RoundMetrics — измерения разрешения раунда (docs/architecture/round-pipeline.md, «Измеряется»): длительность шагов,
// вызовы и токены модели, запасные пути, статусы заявок. Хранятся в `rounds.metrics`, пишутся в лог и журнал чата.
type RoundMetrics struct {
	Attempt             int   `json:"attempt"`      // номер успешной попытки (1 — с первого раза)
	FreezeMS            int64 `json:"freeze_ms"`    // снимок и статусы персонажей
	InterpretMS         int64 `json:"interpret_ms"` // вызов интерпретатора или разбор кодом
	EngineMS            int64 `json:"engine_ms"`    // движок, события, свёртка, запись состояния
	NarrateMS           int64 `json:"narrate_ms"`   // вызов рассказчика или шаблон
	PublishMS           int64 `json:"publish_ms"`   // постановка в outbox
	TotalMS             int64 `json:"total_ms"`     // от дедлайна до публикации в этой попытке
	LLMCalls            int   `json:"llm_calls"`    // вызовов модели (с дозапросами)
	TokensIn            int   `json:"tokens_in"`
	TokensOut           int   `json:"tokens_out"`
	InterpreterFallback bool  `json:"interpreter_fallback"` // заявки разобраны кодом
	NarratorFallback    bool  `json:"narrator_fallback"`    // хроника шаблонная
	Submissions         int   `json:"submissions"`
	Resolved            int   `json:"resolved"`
	Rejected            int   `json:"rejected"`
	Ambiguous           int   `json:"ambiguous"` // безопасных толкований
	Safe                int   `json:"safe"`      // заявок с безопасным толкованием интерпретатора
	Events              int   `json:"events"`
	Patched             int   `json:"patched"` // событий, дописанных кодом в хронику
	Issues              int   `json:"issues"`  // замечаний к ответам рассказчика
	ChronicleParts      int   `json:"chronicle_parts"`
	Notices             int   `json:"notices"`
}

// RoundSnapshot — состояние на дедлайн: персонажи, локации и заявки; воспроизведение движка по снимку и seed.
// SchemaVersion 2 (пункт 07): характеристики, инвентарь, знания, локации с объектами.
type RoundSnapshot struct {
	SchemaVersion int                  `json:"schema_version"`
	Mode          GameMode             `json:"mode,omitempty"`
	RulesVersion  int                  `json:"rules_version"`
	Characters    []SnapshotCharacter  `json:"characters"`
	Locations     []SnapshotLocation   `json:"locations,omitempty"`
	Submissions   []SnapshotSubmission `json:"submissions"`
}

// Character возвращает персонажа снимка по id.
func (s *RoundSnapshot) Character(id CharacterID) (*SnapshotCharacter, bool) {
	for i := range s.Characters {
		if s.Characters[i].ID == id {
			return &s.Characters[i], true
		}
	}
	return nil, false
}

// Location возвращает локацию снимка по id.
func (s *RoundSnapshot) Location(id LocationID) (*SnapshotLocation, bool) {
	for i := range s.Locations {
		if s.Locations[i].ID == id {
			return &s.Locations[i], true
		}
	}
	return nil, false
}

// LocationByName возвращает локацию снимка по имени без учёта регистра.
func (s *RoundSnapshot) LocationByName(name string) (*SnapshotLocation, bool) {
	for i := range s.Locations {
		if strings.EqualFold(s.Locations[i].Name, name) {
			return &s.Locations[i], true
		}
	}
	return nil, false
}

// SnapshotCharacter — персонаж в снимке раунда.
// Отметки входа персонажа в историю (B1): рассказчик вводит нового персонажа в сцену и возвращает отсутствовавшего.
const (
	EntryNew      = "new"      // первый замороженный раунд персонажа
	EntryReturned = "returned" // заявка после статуса `leaving` или `inactive`
)

// SnapshotCharacter — персонаж в снимке раунда.
type SnapshotCharacter struct {
	ID              CharacterID     `json:"id"`
	UserID          UserID          `json:"user_id"`
	Nickname        string          `json:"nickname"`
	Status          CharacterStatus `json:"status"`
	Entry           string          `json:"entry,omitempty"` // вход в историю в этом раунде: EntryNew, EntryReturned или пусто
	MissedRounds    int             `json:"missed_rounds"`
	DeathRoundsLeft int             `json:"death_rounds_left,omitempty"`
	HP              int             `json:"hp"`
	MaxHP           int             `json:"max_hp"`
	FactionID       FactionID       `json:"faction_id"`
	PartyID         *PartyID        `json:"party_id,omitempty"`
	LocationID      *LocationID     `json:"location_id,omitempty"`
	Conditions      []string        `json:"conditions"`
	Attributes      map[string]int  `json:"attributes,omitempty"`
	Skills          map[string]int  `json:"skills,omitempty"`
	Inventory       []Item          `json:"inventory,omitempty"`
	Knowledge       []string        `json:"knowledge,omitempty"`
}

// HasCondition сообщает, есть ли у персонажа состояние.
func (c SnapshotCharacter) HasCondition(name string) bool {
	for _, v := range c.Conditions {
		if v == name {
			return true
		}
	}
	return false
}

// Narration — ответ рассказчика по схеме (docs/llm/round-calls.md, вызов 2): общий текст раунда и блок на сцену.
// Хранится в `rounds.narration`; последние хроники идут в контекст следующего вызова как выжимка.
type Narration struct {
	PromptVersion string           `json:"prompt_version"`
	Chronicle     string           `json:"chronicle"`
	Scenes        []NarrationScene `json:"scenes"`
	Patched       []int64          `json:"patched,omitempty"` // события, дописанные кодом после дозапроса
}

// NarrationScene — блок хроники по сцене (локации; с группами — по группе).
type NarrationScene struct {
	LocationID LocationID `json:"location_id"`
	Party      string     `json:"party"`
	Text       string     `json:"text"`
	EventSeqs  []int64    `json:"event_seqs"`
}

// SnapshotSubmission — заявка в снимке раунда.
type SnapshotSubmission struct {
	SubmissionID SubmissionID `json:"submission_id"`
	CharacterID  CharacterID  `json:"character_id"`
	Text         string       `json:"text"`
	SubmittedAt  time.Time    `json:"submitted_at"`
}

// SubmissionStatus — статус заявки.
type SubmissionStatus string

// Статусы заявки до разрешения; статусы разрешения (resolved, rejected, deferred, ambiguous) — пункт 07.
const (
	SubmissionPending SubmissionStatus = "pending" // ждёт дедлайна; при пропуске раунда переходит в следующий
	SubmissionFrozen  SubmissionStatus = "frozen"  // попала в снимок раунда
)

// Лимиты заявки (docs/game/round.md, «Лимиты»).
const (
	SubmissionMaxLen        = 500
	SubmissionReplaceWindow = time.Minute // замена не чаще раза в минуту, если разрешена settings.allow_resubmit
)

// Submission — заявка персонажа на раунд: сырой текст игрока, данные, не инструкции.
type Submission struct {
	ID          SubmissionID
	GameID      GameID
	RoundID     RoundID
	CharacterID CharacterID
	RawText     string
	Status      SubmissionStatus
	SubmittedAt time.Time // первая подача
	UpdatedAt   time.Time // последняя замена
}

// OutboxKind — вид исходящего игрового сообщения.
type OutboxKind string

// Виды сообщений outbox.
const (
	OutboxScene       OutboxKind = "scene"        // стартовая сцена
	OutboxRoundOpen   OutboxKind = "round_open"   // анонс открытия раунда
	OutboxRoundClosed OutboxKind = "round_closed" // раунд закрыт или пропущен
	OutboxSystem      OutboxKind = "system"       // пауза, возобновление, служебные уведомления игры
	OutboxChronicle   OutboxKind = "chronicle"    // хроника раунда в группу; длинная делится на части (part 1..N)
	OutboxPrivate     OutboxKind = "private"      // уведомление о заявке игроку в личку
)

// TelegramTextMaxLen — лимит текста одного сообщения Telegram; игровые тексты длиннее делятся на части при постановке в outbox.
const TelegramTextMaxLen = 4096

// OutboxStatus — статус доставки сообщения.
type OutboxStatus string

// Статусы outbox.
const (
	OutboxPending OutboxStatus = "pending" // ждёт отправки
	OutboxSending OutboxStatus = "sending" // взято воркером
	OutboxSent    OutboxStatus = "sent"
	OutboxFailed  OutboxStatus = "failed"  // попытки исчерпаны
	OutboxUnknown OutboxStatus = "unknown" // исход неизвестен: воркер не вернулся; повторно не отправляется, чтобы не дублировать
)

// OutboxMessage — исходящее игровое сообщение (ADR-0006): пишется в транзакции сценария, отправляется воркером `app/delivery`.
// Дедупликация по (round_id, telegram_chat_id, kind, part), когда раунд задан.
type OutboxMessage struct {
	ID                OutboxID
	GameID            GameID
	RoundID           *RoundID
	TelegramChatID    TelegramChatID
	Kind              OutboxKind
	Part              int
	Text              string
	Status            OutboxStatus
	TelegramMessageID *int64
	Attempts          int
	SendAfter         time.Time
	ClaimedAt         *time.Time
	LastError         string
	CreatedAt         time.Time
	SentAt            *time.Time
}
