package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	llmfake "taletogether/internal/adapter/llm/fake"
	"taletogether/internal/adapter/postgres/fake"
	"taletogether/internal/model"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

func newService(t *testing.T) (*Service, *fake.Store, *llmfake.Client) {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	store := fake.New(c.Now)
	client := llmfake.New()
	svc := New(store, map[string]Client{model.ProviderFake: client, model.ProviderClaudeCLI: llmfake.New()}, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return svc, store, client
}

func TestResolve_GameThenDefaultThenError(t *testing.T) {
	t.Parallel()
	svc, store, client := newService(t)
	ctx := context.Background()
	if _, err := svc.Resolve(ctx, nil); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("empty: %v", err)
	}
	if _, _, err := svc.Test(ctx, ""); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("test without profile: %v", err)
	}
	def, err := svc.Add(ctx, model.LLMProfile{Name: "local", Provider: model.ProviderFake, ModelInterpreter: "haiku", ModelNarrator: "sonnet", Params: model.LLMParams{Effort: "low", MaxBudgetUSD: 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Add(ctx, model.LLMProfile{Name: "local", Provider: model.ProviderFake, ModelInterpreter: "a", ModelNarrator: "b"}); !errors.Is(err, ErrProfileExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := svc.Add(ctx, model.LLMProfile{Name: "bad name", Provider: model.ProviderFake, ModelInterpreter: "a", ModelNarrator: "b"}); !errors.Is(err, ErrBadValue) {
		t.Fatalf("bad name: %v", err)
	}
	if _, err := svc.Add(ctx, model.LLMProfile{Name: "xx", Provider: "nope", ModelInterpreter: "a", ModelNarrator: "b"}); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("bad provider: %v", err)
	}
	if _, err := svc.Resolve(ctx, nil); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("no default yet: %v", err)
	}
	if err := svc.Use(ctx, "local", nil); err != nil {
		t.Fatal(err)
	}
	if p, err := svc.Resolve(ctx, nil); err != nil || p.ID != def.ID {
		t.Fatalf("default = %+v, %v", p, err)
	}
	// Профиль игры перекрывает глобальный; reset возвращает.
	g, _ := store.CreateGame(ctx, model.Game{ChatID: 1, GenreID: 1, Title: "Мир", Status: model.GameDraft, Timezone: "UTC", RoundMinutes: 10, JoinCode: "X", CreatedByUserID: 1})
	other, _ := svc.Add(ctx, model.LLMProfile{Name: "cli", Provider: model.ProviderClaudeCLI, ModelInterpreter: "haiku", ModelNarrator: "opus"})
	if err := svc.Use(ctx, "cli", &g.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Use(ctx, "cli", ptr(model.GameID(999))); !errors.Is(err, ErrNoGame) {
		t.Fatalf("unknown game: %v", err)
	}
	g, _ = store.GetGame(ctx, g.ID)
	if p, err := svc.Resolve(ctx, &g); err != nil || p.ID != other.ID {
		t.Fatalf("game profile = %+v, %v", p, err)
	}
	if err := svc.Reset(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = store.GetGame(ctx, g.ID)
	if p, _ := svc.Resolve(ctx, &g); p.ID != def.ID {
		t.Fatalf("after reset = %+v", p)
	}
	ov, err := svc.List(ctx)
	if err != nil || len(ov.Profiles) != 2 || ov.DefaultName != "local" || ov.Profiles[0].Name != "cli" || len(ov.Providers) != 2 {
		t.Fatalf("list = %+v, %v", ov, err)
	}
	// Вызов: параметры профиля подставляются, run записан.
	client.JSON = json.RawMessage(`{"action":"идти"}`)
	resp, err := svc.Complete(ctx, Call{Game: &g, Role: model.RoleNarrator, PromptVersion: "v1"}, model.LLMRequest{System: "s", Input: "i"})
	if err != nil || string(resp.JSON) != `{"action":"идти"}` {
		t.Fatalf("complete = %+v, %v", resp, err)
	}
	req := client.Requests[len(client.Requests)-1]
	if req.Model != "sonnet" || req.Effort != "low" || req.MaxBudgetUSD != 0.5 || req.Role != model.RoleNarrator {
		t.Fatalf("request = %+v", req)
	}
	runs, _ := store.ListLLMRuns(ctx, 10)
	if len(runs) != 1 || runs[0].GameID == nil || *runs[0].GameID != g.ID || runs[0].Role != model.RoleNarrator || runs[0].PromptVersion != "v1" || runs[0].TokensIn != 10 || runs[0].Error != "" {
		t.Fatalf("runs = %+v", runs)
	}
	// Ошибка провайдера тоже записана.
	client.Err = errors.New("cli down")
	if _, err := svc.Complete(ctx, Call{Role: model.RoleInterpreter}, model.LLMRequest{Input: "i"}); err == nil {
		t.Fatal("provider error swallowed")
	}
	runs, _ = store.ListLLMRuns(ctx, 10)
	if len(runs) != 2 || runs[0].Error != "cli down" || runs[0].GameID != nil || runs[0].Model != "haiku" {
		t.Fatalf("failed run = %+v", runs[0])
	}
	client.Err = nil
	p, resp, err := svc.Test(ctx, "local")
	if err != nil || p.Name != "local" || len(resp.JSON) == 0 {
		t.Fatalf("test = %+v, %+v, %v", p, resp, err)
	}
	if _, _, err := svc.Test(ctx, "missing"); !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("test missing: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
