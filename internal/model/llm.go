package model

import (
	"encoding/json"
	"sort"
	"strings"
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
	RoleOpening     LLMRole = "opening"  // завязка истории при /begin: модель рассказчика профиля (ADR-0018)
	RoleWorldgen    LLMRole = "worldgen" // генерация мира: нулевой вызов при /create, варианты стартового мира (ADR-0028); модель рассказчика
	RoleTest        LLMRole = "test"     // проверка профиля командой /llm test
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
	if role == RoleNarrator || role == RoleOpening || role == RoleWorldgen {
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

// LLMUsage — токены одного вызова по видам: вход без кэша, запись кэша, чтение кэша, выход (ADR-0032).
type LLMUsage struct {
	Input      int `json:"input"`
	CacheWrite int `json:"cache_write,omitempty"`
	CacheRead  int `json:"cache_read,omitempty"`
	Output     int `json:"output"`
}

// TotalIn — все входные токены: вход без кэша, запись и чтение кэша (как `llm_runs.tokens_in`).
func (u LLMUsage) TotalIn() int { return u.Input + u.CacheWrite + u.CacheRead }

// Add складывает токены двух вызовов.
func (u LLMUsage) Add(o LLMUsage) LLMUsage {
	return LLMUsage{Input: u.Input + o.Input, CacheWrite: u.CacheWrite + o.CacheWrite, CacheRead: u.CacheRead + o.CacheRead, Output: u.Output + o.Output}
}

// LLMResponse — ответ модели и учёт вызова.
type LLMResponse struct {
	Text            string
	JSON            json.RawMessage // структурированный ответ по схеме
	Model           string
	Usage           LLMUsage
	CostUSD         float64 // оценка по таблице `llm_prices` (ADR-0032); заполняет `app/llm` или консоль, адаптер оставляет 0
	ProviderCostUSD float64 // собственная оценка провайдера (`claude -p`: total_cost_usd); 0, если провайдер её не даёт
	Subscription    bool    // вызов оплачен подпиской, не токенами
	RequestID       string
	Duration        time.Duration
}

// LLMRun — запись одного вызова в `llm_runs`.
type LLMRun struct {
	ID               LLMRunID
	GameID           *GameID
	RoundID          *RoundID
	ProfileID        LLMProfileID
	Provider         string
	Role             LLMRole
	Model            string
	PromptVersion    string
	TokensIn         int // все входные токены: вход без кэша + запись кэша + чтение кэша
	TokensCacheWrite int
	TokensCacheRead  int
	TokensOut        int
	CostUSD          float64 // оценка по `llm_prices`; 0 у неизвестной модели
	CostProviderUSD  float64 // оценка провайдера для сверки
	Subscription     bool
	DurationMS       int64
	Error            string
	RequestID        string
	CreatedAt        time.Time
}

// Usage собирает токены записи по видам.
func (r LLMRun) Usage() LLMUsage {
	return LLMUsage{Input: r.TokensIn - r.TokensCacheWrite - r.TokensCacheRead, CacheWrite: r.TokensCacheWrite, CacheRead: r.TokensCacheRead, Output: r.TokensOut}
}

// LLMPrice — тариф модели в `llm_prices`: USD за 1M токенов по видам (ADR-0032). Запись кэша — пятиминутный кэш.
type LLMPrice struct {
	Model         string // полное имя модели или префикс датированных снимков
	InputUSD      float64
	CacheWriteUSD float64
	CacheReadUSD  float64
	OutputUSD     float64
	Source        string    // страница цен провайдера
	PricedAt      time.Time // дата сверки с источником
	UpdatedAt     time.Time
}

// Cost — цена вызова в USD по тарифу.
func (p LLMPrice) Cost(u LLMUsage) float64 {
	return (float64(u.Input)*p.InputUSD + float64(u.CacheWrite)*p.CacheWriteUSD + float64(u.CacheRead)*p.CacheReadUSD + float64(u.Output)*p.OutputUSD) / 1e6
}

// LLMPriceTable — тарифы по имени модели: точное совпадение, иначе самый длинный префикс до дефиса
// (`claude-opus-5-5-20260901` → `claude-opus-5-5`). Пустая таблица любую модель считает неизвестной.
type LLMPriceTable struct {
	prices []LLMPrice // по убыванию длины имени
}

// NewLLMPriceTable строит таблицу из строк `llm_prices`.
func NewLLMPriceTable(prices []LLMPrice) LLMPriceTable {
	sorted := make([]LLMPrice, len(prices))
	copy(sorted, prices)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i].Model) > len(sorted[j].Model) })
	return LLMPriceTable{prices: sorted}
}

// Find возвращает тариф модели; false — модель неизвестна.
func (t LLMPriceTable) Find(model string) (LLMPrice, bool) {
	for _, p := range t.prices {
		if model == p.Model || strings.HasPrefix(model, p.Model+"-") {
			return p, true
		}
	}
	return LLMPrice{}, false
}

// Estimate — цена вызова по таблице; у неизвестной модели 0 и false.
func (t LLMPriceTable) Estimate(model string, u LLMUsage) (float64, bool) {
	p, ok := t.Find(model)
	if !ok {
		return 0, false
	}
	return p.Cost(u), true
}

// Prices — тарифы таблицы по имени модели.
func (t LLMPriceTable) Prices() []LLMPrice {
	out := make([]LLMPrice, len(t.prices))
	copy(out, t.prices)
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}
