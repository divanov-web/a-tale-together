package model

import "time"

// ChatLogKind — вид записи журнала чата (ADR-0012).
type ChatLogKind string

// Виды записей журнала чата.
const (
	ChatLogIn    ChatLogKind = "in"    // входящее сообщение или изменение статуса бота
	ChatLogOut   ChatLogKind = "out"   // исходящее сообщение: ответ на команду или сообщение outbox
	ChatLogLLM   ChatLogKind = "llm"   // вызов модели: вход и выход
	ChatLogRound ChatLogKind = "round" // событие жизненного цикла раунда
	ChatLogError ChatLogKind = "error" // сбой сценария, показанный игроку общим сообщением
)

// ChatLogRecord — одна строка журнала чата: пишется в папку `chat_id` в JSONL, читается ИИ при разборе ошибок.
// Записи игры (LLM, раунд) адресуются в группу игры; личные сообщения — в личку с `game_id` текущей игры игрока.
type ChatLogRecord struct {
	TS         time.Time      `json:"ts"`
	Kind       ChatLogKind    `json:"kind"`
	Chat       TelegramChatID `json:"chat_id"`
	UpdateID   int64          `json:"update_id,omitempty"`
	Duplicate  bool           `json:"duplicate,omitempty"` // повтор update: сценарий не выполнялся
	GameID     GameID         `json:"game_id,omitempty"`
	RoundID    RoundID        `json:"round_id,omitempty"`
	RoundNo    int            `json:"round_no,omitempty"`
	User       *ChatLogUser   `json:"user,omitempty"`
	Command    string         `json:"command,omitempty"`
	Text       string         `json:"text,omitempty"`
	Event      string         `json:"event,omitempty"` // round: opened | submission | skipped | frozen | interpreted | narrated | resolved | published; in: my_chat_member
	Error      string         `json:"error,omitempty"`
	OutboxID   OutboxID       `json:"outbox_id,omitempty"`
	OutboxKind OutboxKind     `json:"outbox_kind,omitempty"`
	Part       int            `json:"part,omitempty"`
	MessageID  int64          `json:"telegram_message_id,omitempty"`
	Attempt    int            `json:"attempt,omitempty"`
	LLM        *ChatLogCall   `json:"llm,omitempty"`
	Data       any            `json:"data,omitempty"` // подробности события раунда: заявки, намерения, итоги, замечания
}

// ChatLogUser — автор входящего сообщения.
type ChatLogUser struct {
	ID       TelegramUserID `json:"id"`
	Username string         `json:"username,omitempty"`
	Name     string         `json:"name,omitempty"`
}

// ChatLogCall — вход и выход одного вызова модели.
type ChatLogCall struct {
	Role          LLMRole `json:"role"`
	PromptVersion string  `json:"prompt_version,omitempty"`
	Profile       string  `json:"profile"`
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	System        string  `json:"system"`
	Input         string  `json:"input"`
	Output        string  `json:"output,omitempty"`
	TokensIn      int     `json:"tokens_in"` // все входные токены (вход без кэша + запись + чтение кэша)
	TokensCache   int     `json:"tokens_cache_write,omitempty"`
	TokensRead    int     `json:"tokens_cache_read,omitempty"`
	TokensOut     int     `json:"tokens_out"`
	CostUSD       float64 `json:"cost_usd"`                    // по таблице `llm_prices` (ADR-0032)
	CostProvider  float64 `json:"cost_provider_usd,omitempty"` // оценка провайдера
	DurationMS    int64   `json:"duration_ms"`
}
