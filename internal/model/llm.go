package model

import (
	"encoding/json"
	"time"
)

// Ключи провайдеров LLM (docs/llm/providers.md, ADR-0003).
const (
	ProviderClaudeCLI = "claude-cli"
	ProviderAnthropic = "anthropic-api"
	ProviderOpenAI    = "openai-api"
	ProviderFake      = "fake"
)

// SettingDefaultLLMProfile — ключ `app_settings` с именем профиля LLM по умолчанию.
const SettingDefaultLLMProfile = "default_llm_profile"

// LLMRole — роль вызова: выбирает модель профиля и промпт.
type LLMRole string

// Роли вызовов LLM.
const (
	RoleInterpreter LLMRole = "interpreter"
	RoleNarrator    LLMRole = "narrator"
	RoleTest        LLMRole = "test" // проверка профиля командой /llm test
)

// LLMParams — параметры профиля в `llm_profiles.params` (jsonb).
type LLMParams struct {
	Effort         string   `json:"effort,omitempty"`          // low|medium|high для claude-cli
	Temperature    *float64 `json:"temperature,omitempty"`     // nil — по умолчанию провайдера
	MaxOutput      int      `json:"max_output,omitempty"`      // лимит токенов ответа; 0 — провайдер по умолчанию
	MaxBudgetUSD   float64  `json:"max_budget_usd,omitempty"`  // потолок оценки стоимости одного вызова
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"` // таймаут вызова; 0 — по умолчанию провайдера
	Parallelism    int      `json:"parallelism,omitempty"`     // одновременных вызовов; 0 — по умолчанию провайдера
}

// LLMProfile — набор настроек провайдера: модели по ролям и параметры.
type LLMProfile struct {
	ID               LLMProfileID
	Name             string
	Provider         string
	ModelInterpreter string
	ModelNarrator    string
	Params           LLMParams
	Enabled          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Model возвращает модель профиля для роли; для проверки берётся модель интерпретатора.
func (p LLMProfile) Model(role LLMRole) string {
	if role == RoleNarrator {
		return p.ModelNarrator
	}
	return p.ModelInterpreter
}

// LLMRequest — запрос к модели: стабильная системная часть и переменный ввод отдельно; текст игрока только во Input.
type LLMRequest struct {
	Role         LLMRole
	Model        string
	System       string
	Input        string
	JSONSchema   json.RawMessage // непустая → ответ обязан соответствовать схеме
	MaxOutput    int
	Temperature  *float64
	Effort       string
	MaxBudgetUSD float64
	Timeout      time.Duration
}

// LLMResponse — ответ модели и учёт вызова.
type LLMResponse struct {
	Text         string
	JSON         json.RawMessage // структурированный ответ по схеме
	Model        string
	TokensIn     int
	TokensOut    int
	CostUSD      float64 // оценка по тарифам API; при подписке реальная плата 0
	Subscription bool    // вызов оплачен подпиской, не токенами
	RequestID    string
	Duration     time.Duration
}

// LLMRun — запись одного вызова в `llm_runs`.
type LLMRun struct {
	ID            LLMRunID
	GameID        *GameID
	RoundID       *RoundID
	ProfileID     LLMProfileID
	Provider      string
	Role          LLMRole
	Model         string
	PromptVersion string
	TokensIn      int
	TokensOut     int
	CostUSD       float64
	Subscription  bool
	DurationMS    int64
	Error         string
	RequestID     string
	CreatedAt     time.Time
}
