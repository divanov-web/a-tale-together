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
	Attempt             int     `json:"attempt"`      // номер успешной попытки (1 — с первого раза)
	FreezeMS            int64   `json:"freeze_ms"`    // снимок и статусы персонажей
	InterpretMS         int64   `json:"interpret_ms"` // вызов интерпретатора или разбор кодом
	EngineMS            int64   `json:"engine_ms"`    // движок, события, свёртка, запись состояния
	NarrateMS           int64   `json:"narrate_ms"`   // вызов рассказчика или шаблон
	PublishMS           int64   `json:"publish_ms"`   // постановка в outbox
	TotalMS             int64   `json:"total_ms"`     // от дедлайна до публикации в этой попытке
	LLMCalls            int     `json:"llm_calls"`    // вызовов модели (с дозапросами)
	TokensIn            int     `json:"tokens_in"`
	TokensOut           int     `json:"tokens_out"`
	CostUSD             float64 `json:"cost_usd"`             // цена вызовов раунда по `llm_prices` (ADR-0032)
	InterpreterFallback bool    `json:"interpreter_fallback"` // заявки разобраны кодом
	NarratorFallback    bool    `json:"narrator_fallback"`    // хроника шаблонная
	Submissions         int     `json:"submissions"`
	Resolved            int     `json:"resolved"`
	Rejected            int     `json:"rejected"`
	Ambiguous           int     `json:"ambiguous"` // безопасных толкований
	Safe                int     `json:"safe"`      // заявок с безопасным толкованием интерпретатора
	Events              int     `json:"events"`
	Patched             int     `json:"patched"` // событий, дописанных кодом в хронику
	Issues              int     `json:"issues"`  // замечаний к ответам рассказчика
	ChronicleParts      int     `json:"chronicle_parts"`
	Notices             int     `json:"notices"`
	LLMReused           int     `json:"llm_reused,omitempty"`      // ответов модели взято из сохранённых по ключу задания (ADR-0019)
	WorldCells          int     `json:"world_cells,omitempty"`     // клеток и карточек в пакете мира (пункт 27)
	WorldTemplated      int     `json:"world_templated,omitempty"` // из них закрыто шаблоном
	QuestsIssued        int     `json:"quests_issued,omitempty"`   // квестов выдано фракциям (пункт 28)
	StagesPlaced        int     `json:"stages_placed,omitempty"`   // этапов привязано к клеткам
	StagesDone          int     `json:"stages_done,omitempty"`     // этапов закрыто
}

// RoundSnapshot — состояние на дедлайн: персонажи, локации и заявки; воспроизведение движка по снимку и seed.
// SchemaVersion 2 (пункт 07): характеристики, инвентарь, знания, локации с объектами. SchemaVersion 3 (пункт 19):
// группы, лимит групп, зарезервированные идентификаторы и имена для новых групп (`party_split` детерминирован и
// воспроизводим по снимку). SchemaVersion 4 (пункт 27): клетки сетки с координатами, стадией и открывшими
// фракциями; в снимок входят только занятые клетки и их соседи, выходы по направлениям считает код. SchemaVersion 5
// (пункт 32): реплики персонажей `speeches` — слова для рассказчика, движок и интерпретатор их не читают.
type RoundSnapshot struct {
	SchemaVersion int                  `json:"schema_version"`
	Mode          GameMode             `json:"mode,omitempty"`
	RulesVersion  int                  `json:"rules_version"`
	Penalties     bool                 `json:"penalties,omitempty"` // состояния-штрафы: «истощён» за форсирование и состояние из on_fail (B27)
	Characters    []SnapshotCharacter  `json:"characters"`
	Locations     []SnapshotLocation   `json:"locations,omitempty"`
	Parties       []SnapshotParty      `json:"parties,omitempty"`
	MaxParties    int                  `json:"max_parties,omitempty"`   // лимит активных групп и занятых локаций; 0 — без лимита
	NewPartyIDs   []PartyID            `json:"new_party_ids,omitempty"` // идентификаторы для групп, созданных движком в этом раунде
	PartyNames    []string             `json:"party_names,omitempty"`   // имена для новых групп из шаблона жанра
	Submissions   []SnapshotSubmission `json:"submissions"`
	Speeches      []SnapshotSpeech     `json:"speeches,omitempty"` // реплики раунда (ADR-0027); в сценах рассказчика по присутствию персонажа
	Hints         []SnapshotHint       `json:"hints,omitempty"`    // фракции, попросившие подсказку рассказчика (пункт 35, ADR-0038)
}

// SnapshotSchemaVersion — текущая версия снимка.
const SnapshotSchemaVersion = 6

// SnapshotHint — просьба фракции о подсказке в снимке: фракция и раунд просьбы.
type SnapshotHint struct {
	FactionID FactionID   `json:"faction_id"`
	Faction   FactionCode `json:"faction"`
	Round     int         `json:"round"`
}

// Party возвращает группу снимка по id.
func (s *RoundSnapshot) Party(id PartyID) (*SnapshotParty, bool) {
	for i := range s.Parties {
		if s.Parties[i].ID == id {
			return &s.Parties[i], true
		}
	}
	return nil, false
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

// LocationAt возвращает клетку снимка по координатам.
func (s *RoundSnapshot) LocationAt(x, y int) (*SnapshotLocation, bool) {
	for i := range s.Locations {
		if s.Locations[i].X == x && s.Locations[i].Y == y {
			return &s.Locations[i], true
		}
	}
	return nil, false
}

// Neighbor возвращает соседа клетки по направлению, если он есть в снимке.
func (s *RoundSnapshot) Neighbor(loc *SnapshotLocation, d Direction) (*SnapshotLocation, bool) {
	dx, dy := d.Offset()
	return s.LocationAt(loc.X+dx, loc.Y+dy)
}

// Exits — выходы клетки по направлениям к существующим соседям в порядке Directions.
func (s *RoundSnapshot) Exits(loc *SnapshotLocation) []Exit {
	var out []Exit
	for _, d := range Directions {
		if n, ok := s.Neighbor(loc, d); ok {
			out = append(out, Exit{Direction: d, To: n})
		}
	}
	return out
}

// ExitTo находит выход клетки по цели заявки: направление словом или имя видимого соседа (без учёта регистра).
// Имя скрытого соседа игроку неизвестно и не сопоставляется (ADR-0025): туда идут по направлению.
func (s *RoundSnapshot) ExitTo(loc *SnapshotLocation, target string) (Exit, bool) {
	if d, ok := ParseDirection(target); ok {
		if n, ok := s.Neighbor(loc, d); ok {
			return Exit{Direction: d, To: n}, true
		}
		return Exit{}, false
	}
	for _, e := range s.Exits(loc) {
		if !e.To.Hidden && strings.EqualFold(strings.TrimSpace(target), e.To.Name) {
			return e, true
		}
	}
	return Exit{}, false
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
	Gender          Gender          `json:"gender,omitempty"` // пол персонажа (B18); пусто — не указан
	Status          CharacterStatus `json:"status"`
	Entry           string          `json:"entry,omitempty"` // вход в историю в этом раунде: EntryNew, EntryReturned или пусто
	Away            bool            `json:"away,omitempty"`  // уходит по слову игрока (/away, ADR-0026); для журнала и уведомлений, рассказчику не передаётся
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

// SnapshotSpeech — реплика персонажа в снимке раунда (ADR-0027).
type SnapshotSpeech struct {
	SpeechID    SpeechID    `json:"speech_id"`
	CharacterID CharacterID `json:"character_id"`
	Text        string      `json:"text"`
	SubmittedAt time.Time   `json:"submitted_at"`
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
	SpeechMaxLen            = 200         // реплика короче заявки (решение руководителя 2026-10-13, пункт 32)
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

// Speech — реплика персонажа на раунд (пункт 32, ADR-0027): слова в сцене, не действие; одна на раунд, до
// `SpeechMaxLen` символов; текст игрока — данные, не инструкции. Кворум и пропуски не затрагивает (допущение G19).
type Speech struct {
	ID          SpeechID
	GameID      GameID
	RoundID     RoundID
	CharacterID CharacterID
	Text        string
	SubmittedAt time.Time
}

// OutboxKind — вид исходящего игрового сообщения.
type OutboxKind string

// Виды сообщений outbox.
const (
	OutboxScene       OutboxKind = "scene"            // стартовая сцена
	OutboxRoundOpen   OutboxKind = "round_open"       // анонс открытия раунда
	OutboxRoundClosed OutboxKind = "round_closed"     // раунд закрыт или пропущен
	OutboxSystem      OutboxKind = "system"           // пауза, возобновление, служебные уведомления игры
	OutboxChronicle   OutboxKind = "chronicle"        // хроника раунда в группу; длинная делится на части (part 1..N)
	OutboxPrivate     OutboxKind = "private"          // уведомление о заявке игроку в личку
	OutboxPrivateOpen OutboxKind = "private_open"     // личное «раунд открыт» с вариантами персонажам без уведомления о заявке (B15)
	OutboxJoined      OutboxKind = "character_joined" // объявление в группе о новом персонаже (B25, ADR-0022)
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
	Entities          []TextEntity // разметка текста (ADR-0029, ADR-0030); nil — обычный текст
	Keyboard          Keyboard     // кнопки под сообщением (ADR-0015); nil — без кнопок
	Status            OutboxStatus
	TelegramMessageID *int64
	Attempts          int
	SendAfter         time.Time
	ClaimedAt         *time.Time
	LastError         string
	CreatedAt         time.Time
	SentAt            *time.Time
}

// Formatted — текст сообщения с разметкой для отправки.
func (m OutboxMessage) Formatted() FormattedText {
	return FormattedText{Text: m.Text, Entities: m.Entities}
}
