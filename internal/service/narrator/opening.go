package narrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"taletogether/assets/prompts"
	"taletogether/internal/model"
)

// OpeningPromptVersion — версия промпта завязки, пишется в `llm_runs.prompt_version`.
const OpeningPromptVersion = "narrator/opening-v1"

// Границы текста завязки в рунах: короче — не завязка, длиннее — не влезает в сообщение; в выжимку для хроник
// уходит начало до openingDigestRune.
const (
	openingMinRune    = 80
	openingMaxRune    = 3000
	openingDigestRune = 1200
)

// ErrOpening — ответ модели не годится в завязку после дозапроса; вызывающий публикует сцену шаблона жанра.
var ErrOpening = errors.New("narrator: завязка не по правилам")

// OpeningSchema — JSON-схема ответа: один текст завязки.
var OpeningSchema = json.RawMessage(`{
  "type": "object",
  "properties": {"text": {"type": "string"}},
  "required": ["text"],
  "additionalProperties": false
}`)

// OpeningParams — данные мира при `/begin` (ADR-0018): жанр, фракции, локации и персонажи с открытыми описаниями;
// скрытые описания не передаются.
type OpeningParams struct {
	Title      string
	Genre      string
	Tone       string
	Mode       model.GameMode
	Factions   []Faction
	Locations  []OpeningLocation
	Characters []OpeningCharacter
}

// OpeningLocation — локация для завязки: описание, признак стартовой и названия видимых выходов.
type OpeningLocation struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Start       bool     `json:"start"`
	Exits       []string `json:"exits"`
}

// OpeningCharacter — персонаж для завязки: только открытые данные.
type OpeningCharacter struct {
	Nickname  string `json:"nickname"`
	Gender    string `json:"gender,omitempty"`
	Faction   string `json:"faction,omitempty"`
	Archetype string `json:"archetype,omitempty"`
	PublicBio string `json:"public_bio"`
}

// OpeningInput — переменная часть вызова завязки.
type OpeningInput struct {
	World      openingWorld       `json:"world"`
	Locations  []OpeningLocation  `json:"locations"`
	Characters []OpeningCharacter `json:"characters"`
}

type openingWorld struct {
	Title    string    `json:"title"`
	Genre    string    `json:"genre"`
	Tone     string    `json:"tone"`
	Mode     string    `json:"mode"`
	Factions []Faction `json:"factions"`
}

// BuildOpeningInput собирает вход завязки; срезы всегда не nil, чтобы JSON был стабильным.
func BuildOpeningInput(p OpeningParams) OpeningInput {
	in := OpeningInput{
		World:      openingWorld{Title: p.Title, Genre: p.Genre, Tone: p.Tone, Mode: string(p.Mode), Factions: nonNilFactions(p.Factions)},
		Locations:  []OpeningLocation{},
		Characters: []OpeningCharacter{},
	}
	for _, l := range p.Locations {
		if l.Exits == nil {
			l.Exits = []string{}
		}
		in.Locations = append(in.Locations, l)
	}
	in.Characters = append(in.Characters, p.Characters...)
	return in
}

// Encode — вход как JSON.
func (in OpeningInput) Encode() string {
	b, err := json.Marshal(in)
	if err != nil {
		panic(fmt.Sprintf("narrator.OpeningInput: %v", err))
	}
	return string(b)
}

// OpeningSystemPrompt — системная часть промпта завязки текущей версии.
func OpeningSystemPrompt() string {
	b, err := prompts.FS.ReadFile("narrator/opening-v1.md")
	if err != nil {
		panic("narrator: opening prompt: " + err.Error())
	}
	return strings.TrimSpace(string(b))
}

// OpeningResult — текст завязки и учёт вызова.
type OpeningResult struct {
	Text      string
	Calls     int      // вызовов модели (1 или 2)
	Issues    []string // замечания к ответам модели (для лога и eval)
	TokensIn  int
	TokensOut int
}

// Opening придумывает завязку истории одним вызовом; при замечаниях (объём, цифры вне «механики», не представленный
// персонаж) — один дозапрос. После него объём вне границ → ErrOpening, остальные замечания только в Issues.
func (s *Service) Opening(ctx context.Context, p OpeningParams) (OpeningResult, error) {
	if s.llm == nil {
		return OpeningResult{}, ErrNoLLM
	}
	input := BuildOpeningInput(p).Encode()
	if limit := s.MaxInputBytes; limit > 0 && len(input) > limit {
		return OpeningResult{}, fmt.Errorf("%w: %d байт", ErrBudget, len(input))
	}
	res := OpeningResult{}
	text, issues, err := s.openingCall(ctx, input, p, &res)
	if err != nil {
		return res, err
	}
	for attempt := 0; len(issues) > 0 && attempt < s.Retries; attempt++ {
		s.logger.Warn("narrator: issues in opening, retrying", slog.Int("issues", len(issues)))
		retryInput := input + "\n\nЗамечания к прошлому ответу — верни полный исправленный ответ по той же схеме:\n- " + strings.Join(issues, "\n- ")
		again, moreIssues, err := s.openingCall(ctx, retryInput, p, &res)
		if err != nil {
			s.logger.Warn("narrator: opening retry failed, keeping first answer", slog.Any("err", err))
			break
		}
		res.Issues = append(res.Issues, issues...)
		text, issues = again, moreIssues
	}
	res.Issues = append(res.Issues, issues...)
	if n := utf8.RuneCountInString(text); n < openingMinRune || n > openingMaxRune {
		return res, fmt.Errorf("%w: %d символов", ErrOpening, n)
	}
	res.Text = text
	return res, nil
}

// openingCall — один вызов модели и разбор ответа; возвращает текст и замечания к нему.
func (s *Service) openingCall(ctx context.Context, input string, p OpeningParams, res *OpeningResult) (string, []string, error) {
	resp, err := s.llm.Complete(ctx, model.LLMRequest{Role: model.RoleOpening, System: s.opening, Input: input, JSONSchema: OpeningSchema})
	res.Calls++
	res.TokensIn += resp.TokensIn
	res.TokensOut += resp.TokensOut
	if err != nil {
		return "", nil, fmt.Errorf("narrator: opening: %w", err)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(resp.JSON, &out); err != nil {
		return "", nil, fmt.Errorf("narrator: opening: ответ не по схеме: %w", err)
	}
	text := strings.TrimSpace(out.Text)
	return text, openingIssues(text, p), nil
}

// openingIssues — замечания к тексту завязки для дозапроса.
func openingIssues(text string, p OpeningParams) []string {
	var issues []string
	switch n := utf8.RuneCountInString(text); {
	case n < openingMinRune:
		issues = append(issues, "текст слишком короткий: нужно 6–12 предложений")
	case n > openingMaxRune:
		issues = append(issues, "текст слишком длинный: не больше 12 предложений")
	}
	if p.Mode != model.ModeMechanics && digits.MatchString(text) {
		issues = append(issues, "в тексте есть цифры: назови всё словами")
	}
	for _, c := range p.Characters {
		if c.Nickname != "" && !strings.Contains(text, c.Nickname) {
			issues = append(issues, fmt.Sprintf("персонаж %q не представлен: упомяни его по никнейму", c.Nickname))
		}
	}
	return issues
}

// openingDigest — завязка для выжимки прошлых хроник: начало текста до openingDigestRune.
func openingDigest(text string) string {
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > openingDigestRune {
		return string(r[:openingDigestRune]) + "…"
	}
	return text
}
