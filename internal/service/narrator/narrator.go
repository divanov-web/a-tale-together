// Package narrator — вызов 2 из 2 на раунд (docs/llm/round-calls.md): публичные и локационные события → хроника
// раунда в группу одним запросом к модели: общий текст и блок на сцену. Факты и исходы не меняет; код проверяет,
// что каждое событие упомянуто по ID, скрытые описания не раскрыты; пропуски дозапрашиваются адресно, затем
// дописываются кодом. В том же вызове модель достраивает мир по заданиям кода (`world.go`, пункт 27, ADR-0019):
// клетки, в которые вошли группы, и карточки недостающих соседей; пакет проверяется по заданиям и лимитам, остаток
// закрывает шаблон. Там же квесты и цели фракций (`quests.go`, пункт 28, ADR-0020): новые квесты по счётчику
// открытий, размещение этапов в клетках, пересмотр целей. При сбое вызывающий публикует шаблонную хронику и шаблонные клетки. Отдельный вызов на игру —
// завязка истории и стартовый пакет при `/begin` (`opening.go`, ADR-0018, ADR-0019).
package narrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"taletogether/assets/prompts"
	"taletogether/internal/model"
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

// Ошибки рассказчика; при любой из них вызывающий переходит на шаблонную хронику.
var (
	ErrNoLLM  = errors.New("narrator: провайдер LLM не задан")
	ErrBudget = errors.New("narrator: контекст больше бюджета вызова")
	ErrLeak   = errors.New("narrator: в хронике раскрыто скрытое описание")
)

// MaxInputBytes — бюджет переменной части вызова по умолчанию; больше — шаблонная хроника без вызова модели.
const MaxInputBytes = 60_000

// Service — рассказчик.
type Service struct {
	llm    LLM
	logger *slog.Logger
	system string
	// opening — системная часть промпта завязки (OpeningSystemPrompt).
	opening string
	// Retries — дозапросов с замечаниями к ответу; по умолчанию 1.
	Retries int
	// MaxInputBytes — бюджет контекста; 0 — MaxInputBytes пакета.
	MaxInputBytes int
}

// New собирает рассказчика с промптом текущей версии.
func New(llm LLM, logger *slog.Logger) *Service {
	return &Service{llm: llm, logger: logger, system: SystemPrompt(), opening: OpeningSystemPrompt(), Retries: 1, MaxInputBytes: MaxInputBytes}
}

// SystemPrompt — системная часть промпта текущей версии (стабильный префикс для кэширования).
func SystemPrompt() string {
	b, err := prompts.FS.ReadFile("narrator/v6.md")
	if err != nil {
		panic("narrator: prompt: " + err.Error())
	}
	return strings.TrimSpace(string(b))
}

// Result — хроника, пакет мира и учёт вызова.
type Result struct {
	Narration model.Narration
	World     WorldPackage // клетки и карточки по заданиям (пункт 27); пустой без заданий
	Calls     int          // вызовов модели (1 или 2)
	Patched   int          // событий, дописанных кодом после дозапроса
	Issues    []string     // замечания к ответам модели (для лога и eval)
	TokensIn  int
	TokensOut int
}

// Narrate пишет хронику раунда одним вызовом; при пропущенных событиях, раскрытых секретах, числах в режиме
// «повествование» или лишних сценах — один дозапрос с замечаниями. Оставшиеся пропуски дописываются кодом,
// оставшаяся утечка — ErrLeak. Ошибка возвращается при сбое вызова, ответе не по схеме, утечке или превышении бюджета.
func (s *Service) Narrate(ctx context.Context, p Params) (Result, error) {
	if s.llm == nil {
		return Result{}, ErrNoLLM
	}
	in := BuildInput(p)
	input := in.Encode()
	if limit := s.MaxInputBytes; limit > 0 && len(input) > limit {
		return Result{}, fmt.Errorf("%w: %d байт", ErrBudget, len(input))
	}
	v := newValidator(in, p)
	res := Result{}
	cur, err := s.call(ctx, v, input, &res)
	if err != nil {
		return res, err
	}
	for attempt := 0; cur.needsRetry() && attempt < s.Retries; attempt++ {
		s.logger.Warn("narrator: issues in chronicle, retrying", slog.Int("issues", len(cur.issues)), slog.Int("missing", len(cur.missing)))
		retryInput := input + "\n\nЗамечания к прошлому ответу — верни полный исправленный ответ по той же схеме:\n- " + strings.Join(cur.issues, "\n- ")
		again, err := s.call(ctx, v, retryInput, &res)
		if err != nil {
			s.logger.Warn("narrator: retry failed, keeping first answer", slog.Any("err", err))
			break
		}
		res.Issues = append(res.Issues, cur.issues...)
		cur = again
	}
	res.Issues = append(res.Issues, cur.issues...)
	if cur.leak {
		return res, ErrLeak
	}
	res.Narration = cur.narration
	res.Narration.PromptVersion = PromptVersion
	res.World = cur.world
	res.Issues = append(res.Issues, cur.world.Issues...)
	res.Patched = len(cur.missing)
	if len(cur.missing) > 0 {
		v.patch(&res.Narration, cur.missing)
	}
	if cur.suspect != "" {
		s.logger.Warn("narrator: outcome wording suspect", slog.String("scene", cur.suspect))
	}
	return res, nil
}

// call выполняет один вызов и проверку ответа.
func (s *Service) call(ctx context.Context, v *validator, input string, res *Result) (checked, error) {
	resp, err := s.llm.Complete(ctx, model.LLMRequest{Role: model.RoleNarrator, System: s.system, Input: input, JSONSchema: Schema})
	res.Calls++
	res.TokensIn += resp.TokensIn
	res.TokensOut += resp.TokensOut
	if err != nil {
		return checked{}, fmt.Errorf("narrator: %w", err)
	}
	out, err := v.validate(resp.JSON)
	if err != nil {
		return checked{}, fmt.Errorf("narrator: %w", err)
	}
	return out, nil
}
