package sim

import (
	"context"
	"sync"

	"taletogether/internal/model"
)

// LLMCall — вызов модели по записи журнала: роль, раунд, размеры системной части и входа в символах, токены.
type LLMCall struct {
	Role        model.LLMRole
	Version     string
	RoundID     model.RoundID
	SystemChars int
	InputChars  int
	OutputChars int
	TokensIn    int
	TokensOut   int
	CostUSD     float64
	Error       string
}

// Journal — журнал чатов стека с отводом в память: размеры промптов вызовов LLM и ошибки сценариев попадают
// в сводку прогона; каждая запись уходит и в файловый журнал Next (nil — только память).
type Journal struct {
	Next interface {
		Write(ctx context.Context, rec model.ChatLogRecord)
	}

	mu    sync.Mutex
	calls []LLMCall
	errs  []model.ChatLogRecord
}

// Write пишет запись в файловый журнал и запоминает вызовы LLM и ошибки.
func (j *Journal) Write(ctx context.Context, rec model.ChatLogRecord) {
	if j.Next != nil {
		j.Next.Write(ctx, rec)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	switch rec.Kind {
	case model.ChatLogLLM:
		if c := rec.LLM; c != nil {
			j.calls = append(j.calls, LLMCall{Role: c.Role, Version: c.PromptVersion, RoundID: rec.RoundID,
				SystemChars: len([]rune(c.System)), InputChars: len([]rune(c.Input)), OutputChars: len([]rune(c.Output)),
				TokensIn: c.TokensIn, TokensOut: c.TokensOut, CostUSD: c.CostUSD, Error: rec.Error})
		}
	case model.ChatLogError:
		j.errs = append(j.errs, rec)
	}
}

// LLMCalls — вызовы LLM в порядке поступления.
func (j *Journal) LLMCalls() []LLMCall {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]LLMCall(nil), j.calls...)
}

// Errors — ошибки сценариев, показанные игрокам.
func (j *Journal) Errors() []model.ChatLogRecord {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]model.ChatLogRecord(nil), j.errs...)
}
