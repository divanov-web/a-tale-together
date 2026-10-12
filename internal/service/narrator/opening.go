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
const OpeningPromptVersion = "narrator/opening-v3"

// Границы текста завязки в рунах: короче — не завязка, длиннее — не влезает в сообщение; в выжимку для хроник
// уходит начало до openingDigestRune.
const (
	openingMinRune    = 80
	openingMaxRune    = 3000
	openingDigestRune = 1200
)

// ErrOpening — ответ модели не годится в завязку после дозапроса; вызывающий публикует сцену шаблона жанра.
var ErrOpening = errors.New("narrator: завязка не по правилам")

// OpeningSchema — JSON-схема ответа: текст завязки и стартовый пакет (ADR-0019): стартовая клетка с объектами и
// карточки соседей по направлениям; цель и сюжетный квест каждой фракции (ADR-0020).
var OpeningSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "text": {"type": "string"},
    "factions": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "code": {"type": "string"},
          "goal": {"type": "object", "properties": {"text": {"type": "string"}, "stage": {"type": "string"}}, "required": ["text", "stage"], "additionalProperties": false},
          "quest": ` + questSchema + `
        },
        "required": ["code", "goal", "quest"],
        "additionalProperties": false
      }
    },
    "start": ` + cellSchema + `,
    "neighbors": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "direction": {"type": "string", "enum": ["north", "south", "west", "east"]},
          "name": {"type": "string"},
          "terrain": {"type": "string"},
          "card": {"type": "string"}
        },
        "required": ["direction", "name", "terrain", "card"],
        "additionalProperties": false
      }
    }
  },
  "required": ["text", "factions", "start", "neighbors"],
  "additionalProperties": false
}`)

// OpeningParams — данные мира при `/begin` (ADR-0018, ADR-0019): жанр, фракции, стартовая клетка шаблона с объектами
// как подсказка, недостающие соседи, модель персонажа для подходов и персонажи с открытыми описаниями; скрытые
// описания не передаются.
type OpeningParams struct {
	Title      string
	Genre      string
	Tone       string
	Mode       model.GameMode
	Factions   []Faction
	Start      OpeningStart
	Neighbors  []NeighborTask // недостающие соседи стартовой клетки
	Def        model.CharacterModelDef
	Characters []OpeningCharacter
	Quests     []QuestTask          // сюжетный квест каждой фракции (пункт 28)
	Goals      []GoalTask           // цель каждой фракции
	Template   *model.QuestTemplate // сюжетный квест шаблона жанра при сбое
}

// OpeningStart — стартовая клетка шаблона жанра: что уже есть, модель может переписать описание и объекты.
type OpeningStart struct {
	LocationID  model.LocationID `json:"location_id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Terrain     string           `json:"terrain"`
	Objects     []string         `json:"objects"` // названия объектов шаблона как подсказка
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
	Start      OpeningStart       `json:"start"`
	Characters []OpeningCharacter `json:"characters"`
	Generate   generate           `json:"generate"`
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
	start := p.Start
	if start.Objects == nil {
		start.Objects = []string{}
	}
	neighbors := p.Neighbors
	if neighbors == nil {
		neighbors = []NeighborTask{}
	}
	in := OpeningInput{
		World:      openingWorld{Title: p.Title, Genre: p.Genre, Tone: p.Tone, Mode: string(p.Mode), Factions: nonNilFactions(p.Factions)},
		Start:      start,
		Characters: []OpeningCharacter{},
		Generate:   generate{Cells: []CellTask{}, Neighbors: neighbors, Limits: generationLimits(), CharacterModel: modelKeys(p.Def), Quests: questTasks(Tasks{Quests: p.Quests, Goals: p.Goals})},
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
	b, err := prompts.FS.ReadFile("narrator/opening-v3.md")
	if err != nil {
		panic("narrator: opening prompt: " + err.Error())
	}
	return strings.TrimSpace(string(b))
}

// OpeningResult — текст завязки, стартовый пакет и учёт вызова.
type OpeningResult struct {
	Text      string
	Start     *GeneratedCell      // стартовая клетка от модели; nil — объекты шаблона остаются
	Neighbors []GeneratedNeighbor // карточки соседей по заданиям (шаблон там, где модель не ответила)
	Quests    []GeneratedQuest    // сюжетный квест каждой фракции по заданиям (пункт 28)
	Goals     []GeneratedGoal     // цель каждой фракции
	Calls     int                 // вызовов модели (1 или 2)
	Issues    []string            // замечания к ответам модели (для лога и eval)
	TokensIn  int
	TokensOut int
}

// Opening придумывает завязку и стартовый пакет одним вызовом; при замечаниях к тексту (объём, цифры вне
// «механики», не представленный персонаж) — один дозапрос. После него объём вне границ → ErrOpening, остальные
// замечания только в Issues. Пакет мира проверяется по заданиям; что не прошло — шаблон.
func (s *Service) Opening(ctx context.Context, p OpeningParams) (OpeningResult, error) {
	if s.llm == nil {
		return OpeningResult{}, ErrNoLLM
	}
	input := BuildOpeningInput(p).Encode()
	if limit := s.MaxInputBytes; limit > 0 && len(input) > limit {
		return OpeningResult{}, fmt.Errorf("%w: %d байт", ErrBudget, len(input))
	}
	res := OpeningResult{}
	cur, issues, err := s.openingCall(ctx, input, p, &res)
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
		cur, issues = again, moreIssues
	}
	res.Issues = append(res.Issues, issues...)
	if n := utf8.RuneCountInString(cur.Text); n < openingMinRune || n > openingMaxRune {
		return res, fmt.Errorf("%w: %d символов", ErrOpening, n)
	}
	res.Text = cur.Text
	res.Start, res.Neighbors, issues = validateOpeningWorld(cur, p)
	res.Issues = append(res.Issues, issues...)
	var quests []rawQuest
	var goals []rawGoal
	for _, f := range cur.Factions {
		q := f.Quest
		q.Faction = f.Code
		quests = append(quests, q)
		goals = append(goals, rawGoal{Faction: f.Code, Text: f.Goal.Text, Stage: f.Goal.Stage})
	}
	res.Quests, issues = validateQuests(quests, p.Quests, p.Template)
	res.Issues = append(res.Issues, issues...)
	res.Goals, issues = validateGoals(goals, p.Goals)
	res.Issues = append(res.Issues, issues...)
	return res, nil
}

// openingResponse — ответ модели по OpeningSchema.
type openingResponse struct {
	Text      string            `json:"text"`
	Factions  []openingFaction  `json:"factions"`
	Start     rawCell           `json:"start"`
	Neighbors []openingNeighbor `json:"neighbors"`
}

type openingFaction struct {
	Code  string   `json:"code"`
	Goal  rawGoal  `json:"goal"`
	Quest rawQuest `json:"quest"`
}

type openingNeighbor struct {
	Direction model.Direction `json:"direction"`
	Name      string          `json:"name"`
	Terrain   string          `json:"terrain"`
	Card      string          `json:"card"`
}

// openingCall — один вызов модели и разбор ответа; возвращает ответ и замечания к тексту.
func (s *Service) openingCall(ctx context.Context, input string, p OpeningParams, res *OpeningResult) (openingResponse, []string, error) {
	resp, err := s.llm.Complete(ctx, model.LLMRequest{Role: model.RoleOpening, System: s.opening, Input: input, JSONSchema: OpeningSchema})
	res.Calls++
	res.TokensIn += resp.TokensIn
	res.TokensOut += resp.TokensOut
	if err != nil {
		return openingResponse{}, nil, fmt.Errorf("narrator: opening: %w", err)
	}
	var out openingResponse
	if err := json.Unmarshal(resp.JSON, &out); err != nil {
		return openingResponse{}, nil, fmt.Errorf("narrator: opening: ответ не по схеме: %w", err)
	}
	out.Text = strings.TrimSpace(out.Text)
	return out, openingIssues(out.Text, p), nil
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

// validateOpeningWorld — стартовый пакет: клетка (без валидных объектов — nil, объекты шаблона остаются) и соседи
// по направлениям заданий (не из задания — отброшены, пропущенные — шаблон).
func validateOpeningWorld(out openingResponse, p OpeningParams) (*GeneratedCell, []GeneratedNeighbor, []string) {
	task := CellTask{LocationID: p.Start.LocationID, Name: p.Start.Name, Terrain: p.Start.Terrain, Card: p.Start.Description}
	cell, issues := validateCell(out.Start, task, p.Def)
	start := &cell
	if len(cell.Objects) == 0 {
		issues = append(issues, "стартовая клетка без объектов: остаются объекты шаблона")
		start = nil
	}
	byDir := map[model.Direction]openingNeighbor{}
	for _, n := range out.Neighbors {
		if _, dup := byDir[n.Direction]; !dup {
			byDir[n.Direction] = n
		}
	}
	var neighbors []GeneratedNeighbor
	for _, t := range p.Neighbors {
		n, ok := byDir[t.Direction]
		card, more := validateNeighbor(rawNeighbor{X: t.X, Y: t.Y, Name: n.Name, Terrain: n.Terrain, Card: n.Card}, ok, t)
		issues = append(issues, more...)
		neighbors = append(neighbors, card)
	}
	return start, neighbors, issues
}

// openingDigest — завязка для выжимки прошлых хроник: начало текста до openingDigestRune.
func openingDigest(text string) string {
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > openingDigestRune {
		return string(r[:openingDigestRune]) + "…"
	}
	return text
}
