// Package interpreter — вызов 1 из 2 на раунд (docs/llm/round-calls.md): все заявки раунда одним запросом к модели →
// массив намерений по каталогу; валидация по снимку, один повтор с ошибками, безопасное толкование (G3).
// Сложность и бросок не назначает; при сбое модели вызывающий переходит на разбор кодом (`rules.ParseTyped`).
package interpreter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"taletogether/assets/prompts"
	"taletogether/internal/model"
	"taletogether/internal/service/rules"
)

// LLM — провайдер модели; у вызывающего это обёртка над `app/llm.Complete` с ролью и раундом для учёта.
type LLM interface {
	Complete(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error)
}

// Func — функция как LLM.
type Func func(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error)

// Complete вызывает функцию.
func (f Func) Complete(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error) {
	return f(ctx, req)
}

// ErrNoLLM — провайдер не настроен.
var ErrNoLLM = errors.New("interpreter: провайдер LLM не задан")

// Service — интерпретатор заявок.
type Service struct {
	llm    LLM
	logger *slog.Logger
	system string
	// Retries — повторов с ошибками валидации в промпте; по умолчанию 1.
	Retries int
}

// New собирает интерпретатор с промптом текущей версии.
func New(llm LLM, logger *slog.Logger) *Service {
	return &Service{llm: llm, logger: logger, system: SystemPrompt(), Retries: 1}
}

// SystemPrompt — системная часть промпта текущей версии (стабильный префикс для кэширования).
func SystemPrompt() string {
	b, err := prompts.FS.ReadFile("interpreter/v3.md")
	if err != nil {
		panic("interpreter: prompt: " + err.Error())
	}
	return strings.TrimSpace(string(b))
}

// Result — намерения по всем заявкам снимка и учёт вызова.
type Result struct {
	Intents   []model.Intent
	Calls     int // вызовов модели (1 или 2)
	Invalid   int // заявок, получивших безопасное толкование
	TokensIn  int
	TokensOut int
}

// Interpret разбирает все заявки снимка одним вызовом; при ошибках валидации — один повтор с их перечнем,
// затем безопасное толкование. Ошибка возвращается только при сбое первого вызова или ответе не по схеме:
// тогда вызывающий разбирает заявки кодом. Порядок намерений — порядок заявок снимка.
func (s *Service) Interpret(ctx context.Context, snap model.RoundSnapshot, def model.CharacterModelDef) (Result, error) {
	if len(snap.Submissions) == 0 {
		return Result{}, nil
	}
	if s.llm == nil {
		return Result{}, ErrNoLLM
	}
	v := newValidator(snap, def)
	input := BuildInput(snap, def).Encode()
	res := Result{}
	valid, issues, err := s.call(ctx, v, input, &res)
	if err != nil {
		return res, err
	}
	for attempt := 0; len(issues) > 0 && attempt < s.Retries; attempt++ {
		s.logger.Warn("interpreter: invalid intents, retrying", slog.Int("issues", len(issues)))
		retryInput := input + "\n\nОшибки предыдущего ответа — исправь только эти заявки, остальные повтори без изменений:\n- " + joinIssues(issues)
		again, againIssues, err := s.call(ctx, v, retryInput, &res)
		if err != nil {
			s.logger.Warn("interpreter: retry failed, safe fallback", slog.Any("err", err))
			break
		}
		for id, in := range again {
			valid[id] = in
		}
		issues = nil
		for _, is := range againIssues {
			if _, ok := valid[is.SubmissionID]; !ok {
				issues = append(issues, is)
			}
		}
	}
	for _, is := range issues {
		sub := v.subs[is.SubmissionID]
		valid[is.SubmissionID] = v.safe(sub, v.fallbacks[is.SubmissionID], "безопасное толкование: "+is.Msg)
		res.Invalid++
	}
	for _, sub := range snap.Submissions {
		res.Intents = append(res.Intents, valid[sub.SubmissionID])
	}
	return res, nil
}

// call выполняет один вызов и валидацию; запоминает запасные действия модели для безопасного толкования.
func (s *Service) call(ctx context.Context, v *validator, input string, res *Result) (map[model.SubmissionID]model.Intent, []issue, error) {
	resp, err := s.llm.Complete(ctx, model.LLMRequest{Role: model.RoleInterpreter, System: s.system, Input: input, JSONSchema: Schema})
	res.Calls++
	res.TokensIn += resp.TokensIn
	res.TokensOut += resp.TokensOut
	if err != nil {
		return nil, nil, fmt.Errorf("interpreter: %w", err)
	}
	valid, issues, err := v.validate(resp.JSON)
	if err != nil {
		return nil, nil, fmt.Errorf("interpreter: %w", err)
	}
	return valid, issues, nil
}

// Fallback — разбор заявок кодом при недоступной модели: типовые команды через `rules.ParseTyped`, заявка, совпадающая
// с названием подхода видимого объекта в локации персонажа (кнопка варианта, ADR-0015), — этот подход; остальное —
// безопасное толкование `wait` с причиной (docs/architecture/round-pipeline.md, сбой шага 2).
func Fallback(snap model.RoundSnapshot, reason string) []model.Intent {
	out := make([]model.Intent, 0, len(snap.Submissions))
	for _, sub := range snap.Submissions {
		in := rules.ParseTyped(model.Submission{ID: sub.SubmissionID, CharacterID: sub.CharacterID, RawText: sub.Text})
		if in.ActionType == "" {
			if target, idx, action, ok := approachByTitle(snap, sub.CharacterID, sub.Text); ok {
				in.ActionType, in.Target, in.ApproachIdx, in.Ambiguities = action, target, &idx, nil
			} else {
				in.ActionType = model.ActionWait
				in.Ambiguities = append([]string{reason}, in.Ambiguities...)
			}
		}
		out = append(out, in)
	}
	return out
}

// approachByTitle находит подход видимого объекта в локации персонажа по названию (как его показывает `narrator.Options`:
// `Title`, иначе «действие объект»); сравнение без учёта регистра.
func approachByTitle(snap model.RoundSnapshot, char model.CharacterID, text string) (target string, idx int, action model.ActionType, ok bool) {
	c, found := snap.Character(char)
	if !found || c.LocationID == nil {
		return "", 0, "", false
	}
	l, found := snap.Location(*c.LocationID)
	if !found {
		return "", 0, "", false
	}
	text = strings.TrimSpace(text)
	for _, o := range l.Objects {
		if o.HiddenUntil && !o.Revealed {
			continue
		}
		for i, ap := range o.Approaches {
			title := ap.Title
			if title == "" {
				title = string(ap.Action) + " " + o.Title
			}
			if strings.EqualFold(title, text) {
				return o.Key, i, ap.Action, true
			}
		}
	}
	return "", 0, "", false
}

func joinIssues(issues []issue) string {
	parts := make([]string, 0, len(issues))
	for _, is := range issues {
		parts = append(parts, is.String())
	}
	return strings.Join(parts, "\n- ")
}
