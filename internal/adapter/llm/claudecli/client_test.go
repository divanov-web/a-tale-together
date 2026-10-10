package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	path, err := filepath.Abs("testdata/claude")
	if err != nil {
		t.Fatal(err)
	}
	return New(Settings{Path: path, Workdir: t.TempDir(), Timeout: 2 * time.Second})
}

func TestComplete_ParsesStructuredOutput(t *testing.T) {
	t.Parallel()
	c := newTestClient(t)
	resp, err := c.Complete(context.Background(), model.LLMRequest{
		System: "sys", Input: "hello", Model: "haiku", Effort: "low", MaxBudgetUSD: 0.5,
		JSONSchema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if string(resp.JSON) != `{"echo":"hello"}` || resp.Model != "claude-haiku-5-5" || resp.TokensIn != 5139 || resp.TokensOut != 322 ||
		resp.CostUSD != 0.0011886 || !resp.Subscription || resp.RequestID != "sess-1" || resp.Duration <= 0 {
		t.Fatalf("response = %+v", resp)
	}
	args := strings.Join(c.args(model.LLMRequest{System: "s", JSONSchema: json.RawMessage(`{}`), Model: "haiku", Effort: "low", MaxBudgetUSD: 0.5}), " ")
	for _, want := range []string{"-p --output-format json", "--tools  --no-session-persistence", "--setting-sources  --strict-mcp-config", "--exclude-dynamic-system-prompt-sections", "--system-prompt s", "--json-schema {}", "--model haiku", "--effort low", "--max-budget-usd 0.5"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q lack %q", args, want)
		}
	}
	if strings.Contains(args, "--bare") {
		t.Error("--bare must not be used: subscription auth is skipped")
	}
}

// TestComplete_Errors без t.Parallel: режим подменного бинаря задаётся через окружение (t.Setenv).
func TestComplete_Errors(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{"error", "Not logged in"},
		{"exit", "Invalid API key"},
		{"garbage", "ответ не JSON"},
		{"empty", "пустой structured_output"},
		{"sleep", "таймаут"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			c := newTestClient(t)
			t.Setenv("FAKE_CLAUDE_MODE", tc.mode)
			_, err := c.Complete(context.Background(), model.LLMRequest{Input: "x", JSONSchema: json.RawMessage(`{}`)})
			if !errors.Is(err, ErrCLI) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("mode %s: err = %v; want ErrCLI with %q", tc.mode, err, tc.want)
			}
		})
	}
}

func TestComplete_MissingBinary(t *testing.T) {
	t.Parallel()
	c := New(Settings{Path: "/nonexistent/claude", Workdir: t.TempDir()})
	if _, err := c.Complete(context.Background(), model.LLMRequest{Input: "x"}); !errors.Is(err, ErrCLI) {
		t.Fatalf("err = %v", err)
	}
}

// TestComplete_Live вызывает настоящий claude по подписке; включается CLAUDE_CLI_LIVE=1 (ручная проверка, не CI).
func TestComplete_Live(t *testing.T) {
	if os.Getenv("CLAUDE_CLI_LIVE") == "" {
		t.Skip("CLAUDE_CLI_LIVE is not set")
	}
	c := New(Settings{Path: os.Getenv("CLAUDE_CLI_PATH"), Workdir: t.TempDir()})
	resp, err := c.Complete(context.Background(), model.LLMRequest{
		System: "Ты интерпретатор заявок текстовой RPG. Верни только JSON по схеме.", Input: "Персонаж Элис: иду к башне и осматриваю дверь",
		JSONSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"},"target":{"type":"string"}},"required":["action","target"],"additionalProperties":false}`),
		Model:      "haiku", Effort: "low", MaxBudgetUSD: 0.5,
	})
	if err != nil {
		t.Fatalf("live call: %v", err)
	}
	t.Logf("model=%s tokens=%d/%d cost=%.5f duration=%s json=%s", resp.Model, resp.TokensIn, resp.TokensOut, resp.CostUSD, resp.Duration, resp.JSON)
	if len(resp.JSON) == 0 || !resp.Subscription || resp.Model == "" {
		t.Fatalf("response = %+v", resp)
	}
}
