package narrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"taletogether/assets/prompts"
	"taletogether/internal/model"
)

// WorldgenPromptVersion — версия промпта генерации мира, пишется в `llm_runs.prompt_version`.
const WorldgenPromptVersion = "narrator/worldgen-v2"

// Лимиты варианта стартового мира (ADR-0028): заголовок кнопки и название мира короткие, остальное — пара предложений.
const (
	WorldgenOptions       = model.WorldSetupOptions
	setupTitleMaxRunes    = 40
	setupWorldMaxRunes    = 60
	setupSummaryMaxRunes  = 300
	setupToneMaxRunes     = 300
	setupPremiseMaxRunes  = 600
	setupFactionMaxRunes  = 40
	setupFactionTextRunes = 300
	setupGoalMaxRunes     = 200
	setupStartNameRunes   = 60
	setupMinOptions       = 2
)

// ErrWorldgen — в ответе модели меньше двух пригодных вариантов; вызывающий повторит попытку или оставит шаблон жанра.
var ErrWorldgen = errors.New("narrator: варианты мира не по правилам")

// WorldgenSchema — JSON-схема ответа: варианты стартового мира с заголовками.
var WorldgenSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "options": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "title": {"type": "string"},
          "world": {"type": "string"},
          "summary": {"type": "string"},
          "tone": {"type": "string"},
          "premise": {"type": "string"},
          "factions": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "code": {"type": "string"},
                "title": {"type": "string"},
                "description": {"type": "string"},
                "tone": {"type": "string"},
                "goal": {"type": "string"}
              },
              "required": ["code", "title", "description", "tone", "goal"],
              "additionalProperties": false
            }
          },
          "start": {
            "type": "object",
            "properties": {
              "name": {"type": "string"},
              "terrain": {"type": "string"},
              "description": {"type": "string"}
            },
            "required": ["name", "terrain", "description"],
            "additionalProperties": false
          }
        },
        "required": ["title", "world", "summary", "tone", "premise", "factions", "start"],
        "additionalProperties": false
      }
    }
  },
  "required": ["options"],
  "additionalProperties": false
}`)

// WorldgenParams — описание жанра для нулевого вызова: шаблон как образец формы, не содержания; Seed различает
// попытки и игры, чтобы сохранённый по ключу задания ответ не повторялся между мирами; Avoid — заголовки прошлых
// вариантов, которые модель не должна повторять.
type WorldgenParams struct {
	Genre    string
	Tone     string
	Factions []Faction
	Start    SetupStart
	Seed     string
	Avoid    []string
}

// SetupStart — стартовая клетка шаблона жанра во входе генерации мира.
type SetupStart struct {
	Name        string `json:"name"`
	Terrain     string `json:"terrain"`
	Description string `json:"description"`
}

// WorldgenInput — переменная часть вызова генерации мира.
type WorldgenInput struct {
	Genre  worldgenGenre  `json:"genre"`
	Limits worldgenLimits `json:"limits"`
	Seed   string         `json:"seed"`
	Avoid  []string       `json:"avoid"`
}

type worldgenGenre struct {
	Title    string     `json:"title"`
	Tone     string     `json:"tone"`
	Factions []Faction  `json:"factions"`
	Start    SetupStart `json:"start"`
}

type worldgenLimits struct {
	Options          int `json:"options"`
	TitleMaxChars    int `json:"title_max_chars"`
	WorldMaxChars    int `json:"world_max_chars"`
	SummaryMaxChars  int `json:"summary_max_chars"`
	ToneMaxChars     int `json:"tone_max_chars"`
	PremiseMaxChars  int `json:"premise_max_chars"`
	FactionMaxChars  int `json:"faction_title_max_chars"`
	FactionTextChars int `json:"faction_text_max_chars"`
	GoalMaxChars     int `json:"goal_max_chars"`
	StartNameChars   int `json:"start_name_max_chars"`
	StartDescChars   int `json:"start_description_max_chars"`
}

// BuildWorldgenInput собирает вход генерации мира; срезы не nil, чтобы JSON был стабильным.
func BuildWorldgenInput(p WorldgenParams) WorldgenInput {
	avoid := p.Avoid
	if avoid == nil {
		avoid = []string{}
	}
	return WorldgenInput{
		Genre: worldgenGenre{Title: p.Genre, Tone: p.Tone, Factions: nonNilFactions(p.Factions), Start: p.Start},
		Limits: worldgenLimits{Options: WorldgenOptions, TitleMaxChars: setupTitleMaxRunes, WorldMaxChars: setupWorldMaxRunes, SummaryMaxChars: setupSummaryMaxRunes,
			ToneMaxChars: setupToneMaxRunes, PremiseMaxChars: setupPremiseMaxRunes, FactionMaxChars: setupFactionMaxRunes, FactionTextChars: setupFactionTextRunes,
			GoalMaxChars: setupGoalMaxRunes, StartNameChars: setupStartNameRunes, StartDescChars: model.MaxCellDescRunes},
		Seed:  p.Seed,
		Avoid: avoid,
	}
}

// Encode — вход как JSON.
func (in WorldgenInput) Encode() string {
	b, err := json.Marshal(in)
	if err != nil {
		panic(fmt.Sprintf("narrator.WorldgenInput: %v", err))
	}
	return string(b)
}

// WorldgenSystemPrompt — системная часть промпта генерации мира текущей версии.
func WorldgenSystemPrompt() string {
	b, err := prompts.FS.ReadFile("narrator/worldgen-v2.md")
	if err != nil {
		panic("narrator: worldgen prompt: " + err.Error())
	}
	return strings.TrimSpace(string(b))
}

// WorldgenResult — принятые варианты и учёт вызова.
type WorldgenResult struct {
	Options   []model.WorldSetup
	Issues    []string // отброшенные варианты и обрезанные поля (для журнала и eval)
	TokensIn  int
	TokensOut int
}

type rawSetup struct {
	Title    string `json:"title"`
	World    string `json:"world"`
	Summary  string `json:"summary"`
	Tone     string `json:"tone"`
	Premise  string `json:"premise"`
	Factions []struct {
		Code        string `json:"code"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Tone        string `json:"tone"`
		Goal        string `json:"goal"`
	} `json:"factions"`
	Start SetupStart `json:"start"`
}

// Worldgen — нулевой вызов «генерация мира» (ADR-0028): по описанию жанра модель возвращает до четырёх вариантов
// стартового мира. Один вызов без дозапроса: вариант с пустым или слишком длинным заголовком, названием, фракцией
// не по кодам жанра или без стартовой клетки отбрасывается, длинные тексты обрезаются; меньше двух пригодных →
// ErrWorldgen (повтор решает вызывающий новой попыткой с другим seed).
func (s *Service) Worldgen(ctx context.Context, p WorldgenParams) (WorldgenResult, error) {
	if s.llm == nil {
		return WorldgenResult{}, ErrNoLLM
	}
	input := BuildWorldgenInput(p).Encode()
	resp, err := s.llm.Complete(ctx, model.LLMRequest{Role: model.RoleWorldgen, System: s.worldgen, Input: input, JSONSchema: WorldgenSchema})
	res := WorldgenResult{TokensIn: resp.TokensIn, TokensOut: resp.TokensOut}
	if err != nil {
		return res, fmt.Errorf("narrator: worldgen: %w", err)
	}
	var out struct {
		Options []rawSetup `json:"options"`
	}
	if err := json.Unmarshal(resp.JSON, &out); err != nil {
		return res, fmt.Errorf("narrator: worldgen: ответ не по схеме: %w", err)
	}
	res.Options, res.Issues = validateSetups(out.Options, p.Factions)
	if len(res.Options) < setupMinOptions {
		return res, fmt.Errorf("%w: пригодных вариантов %d", ErrWorldgen, len(res.Options))
	}
	return res, nil
}

// validateSetups проверяет варианты: обязательные короткие поля в границах, фракции ровно по кодам жанра, местность
// латиницей; лишние варианты сверх WorldgenOptions и повторы заголовков отбрасываются.
func validateSetups(raw []rawSetup, factions []Faction) ([]model.WorldSetup, []string) {
	var out []model.WorldSetup
	var issues []string
	seen := map[string]bool{}
	for i, r := range raw {
		if len(out) == WorldgenOptions {
			issues = append(issues, fmt.Sprintf("вариант %d сверх лимита отброшен", i+1))
			break
		}
		opt, problem := validateSetup(r, factions)
		if problem != "" {
			issues = append(issues, fmt.Sprintf("вариант %d отброшен: %s", i+1, problem))
			continue
		}
		if key := strings.ToLower(opt.Title); seen[key] {
			issues = append(issues, fmt.Sprintf("вариант %d отброшен: повтор заголовка %q", i+1, opt.Title))
			continue
		} else {
			seen[key] = true
		}
		out = append(out, opt)
	}
	return out, issues
}

func validateSetup(r rawSetup, factions []Faction) (model.WorldSetup, string) {
	opt := model.WorldSetup{Title: strings.TrimSpace(r.Title), World: strings.TrimSpace(r.World), Summary: clip(r.Summary, setupSummaryMaxRunes),
		Tone: clip(r.Tone, setupToneMaxRunes), Premise: clip(r.Premise, setupPremiseMaxRunes)}
	if n := utf8.RuneCountInString(opt.Title); n < 2 || n > setupTitleMaxRunes {
		return opt, "заголовок вне границ"
	}
	if n := utf8.RuneCountInString(opt.World); n < 2 || n > setupWorldMaxRunes {
		return opt, "название мира вне границ"
	}
	if opt.Summary == "" || opt.Premise == "" {
		return opt, "нет описания или завязки"
	}
	for _, want := range factions {
		var found bool
		for _, f := range r.Factions {
			if strings.TrimSpace(f.Code) != want.Code {
				continue
			}
			title := strings.TrimSpace(f.Title)
			if n := utf8.RuneCountInString(title); n < 2 || n > setupFactionMaxRunes {
				return opt, fmt.Sprintf("имя фракции %s вне границ", want.Code)
			}
			opt.Factions = append(opt.Factions, model.SetupFaction{Code: model.FactionCode(want.Code), Title: title,
				Description: clip(f.Description, setupFactionTextRunes), Tone: clip(f.Tone, setupFactionTextRunes), Goal: clip(f.Goal, setupGoalMaxRunes)})
			found = true
			break
		}
		if !found {
			return opt, fmt.Sprintf("нет фракции %s", want.Code)
		}
	}
	opt.Start = model.SetupStart{Name: clip(r.Start.Name, setupStartNameRunes), Terrain: normalizeKey(r.Start.Terrain), Description: clip(r.Start.Description, model.MaxCellDescRunes)}
	if opt.Start.Name == "" || opt.Start.Description == "" {
		return opt, "стартовая клетка без имени или описания"
	}
	if opt.Start.Terrain == "" {
		opt.Start.Terrain = "town"
	}
	return opt, ""
}
