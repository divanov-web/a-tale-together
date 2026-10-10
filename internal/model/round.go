package model

import "time"

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
	RoundFailed    RoundStatus = "failed"
)

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
	SkipReason string         // причина пропуска для /status, например «заявок 1 из 2»
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// RoundSnapshot — состояние на дедлайн: персонажи и заявки; воспроизведение движка по снимку и seed.
type RoundSnapshot struct {
	SchemaVersion int                  `json:"schema_version"`
	Characters    []SnapshotCharacter  `json:"characters"`
	Submissions   []SnapshotSubmission `json:"submissions"`
}

// SnapshotCharacter — персонаж в снимке раунда.
type SnapshotCharacter struct {
	ID           CharacterID     `json:"id"`
	Nickname     string          `json:"nickname"`
	Status       CharacterStatus `json:"status"`
	MissedRounds int             `json:"missed_rounds"`
	HP           int             `json:"hp"`
	FactionID    FactionID       `json:"faction_id"`
	PartyID      *PartyID        `json:"party_id,omitempty"`
	LocationID   *LocationID     `json:"location_id,omitempty"`
	Conditions   []string        `json:"conditions"`
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

// Виды сообщений outbox; хроника и приватные итоги появятся в пункте 09.
const (
	OutboxScene       OutboxKind = "scene"        // стартовая сцена
	OutboxRoundOpen   OutboxKind = "round_open"   // анонс открытия раунда
	OutboxRoundClosed OutboxKind = "round_closed" // раунд закрыт или пропущен
	OutboxSystem      OutboxKind = "system"       // пауза, возобновление, служебные уведомления игры
)

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
