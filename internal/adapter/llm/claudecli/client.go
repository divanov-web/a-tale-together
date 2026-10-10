// Package claudecli — провайдер `claude-cli`: запуск `claude -p` локально по подписке Claude Code
// (docs/llm/providers.md, ADR-0003). Один процесс на вызов, вывод JSON, структурированный ответ по схеме.
package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"taletogether/internal/model"
)

// Settings — путь к бинарю и рабочая папка.
type Settings struct {
	Path        string        // бинарь claude; пусто — "claude" из PATH
	Workdir     string        // пустая папка запуска: без CLAUDE.md и настроек проекта
	Timeout     time.Duration // таймаут вызова по умолчанию; 0 — 120 с
	Parallelism int           // одновременных процессов; 0 — 2
}

// Client — провайдер claude-cli.
type Client struct {
	settings Settings
	sem      chan struct{}
}

// ErrCLI — ошибка запуска или ответа CLI; текст пригоден для владельца бота.
var ErrCLI = errors.New("claude-cli")

// New готовит клиент; бинарь не проверяется до первого вызова.
func New(s Settings) *Client {
	if s.Path == "" {
		s.Path = "claude"
	}
	if s.Timeout <= 0 {
		s.Timeout = 120 * time.Second
	}
	if s.Parallelism <= 0 {
		s.Parallelism = 2
	}
	return &Client{settings: s, sem: make(chan struct{}, s.Parallelism)}
}

// output — поля ответа `claude -p --output-format json`, которые использует бот.
type output struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	SessionID        string          `json:"session_id"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	DurationMS       int64           `json:"duration_ms"`
	Usage            struct {
		InputTokens         int `json:"input_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens"`
		CacheReadTokens     int `json:"cache_read_input_tokens"`
		OutputTokens        int `json:"output_tokens"`
	} `json:"usage"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
}

// Complete запускает `claude -p` из рабочей папки: системная часть флагом, ввод через stdin, схема через --json-schema.
// Флаги изоляции отсекают настройки, CLAUDE.md и MCP пользователя (проверено 2026-10-10, см. providers.md).
func (c *Client) Complete(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return model.LLMResponse{}, fmt.Errorf("claudecli.Complete: %w", ctx.Err())
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = c.settings.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := c.args(req)
	cmd := exec.CommandContext(ctx, c.settings.Path, args...)
	cmd.Dir = c.settings.Workdir
	cmd.Stdin = strings.NewReader(req.Input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	started := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(started)
	if ctx.Err() != nil {
		return model.LLMResponse{}, fmt.Errorf("%w: таймаут %s", ErrCLI, timeout)
	}
	var out output
	parseErr := json.Unmarshal(stdout.Bytes(), &out)
	if runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if parseErr == nil && out.Result != "" {
			msg = out.Result
		}
		if msg == "" {
			msg = runErr.Error()
		}
		return model.LLMResponse{}, fmt.Errorf("%w: %s", ErrCLI, truncate(msg, 300))
	}
	if parseErr != nil {
		return model.LLMResponse{}, fmt.Errorf("%w: ответ не JSON: %s", ErrCLI, truncate(stdout.String(), 200))
	}
	if out.IsError {
		return model.LLMResponse{}, fmt.Errorf("%w: %s", ErrCLI, truncate(firstNonEmpty(out.Result, out.Subtype), 300))
	}
	resp := model.LLMResponse{
		Text:         out.Result,
		JSON:         out.StructuredOutput,
		Model:        modelName(out.ModelUsage, req.Model),
		TokensIn:     out.Usage.InputTokens + out.Usage.CacheCreationTokens + out.Usage.CacheReadTokens,
		TokensOut:    out.Usage.OutputTokens,
		CostUSD:      out.TotalCostUSD,
		Subscription: true,
		RequestID:    out.SessionID,
		Duration:     elapsed,
	}
	if len(req.JSONSchema) > 0 && (len(resp.JSON) == 0 || string(resp.JSON) == "null") {
		return resp, fmt.Errorf("%w: пустой structured_output", ErrCLI)
	}
	return resp, nil
}

// args — аргументы запуска по запросу и настройкам.
func (c *Client) args(req model.LLMRequest) []string {
	args := []string{
		"-p", "--output-format", "json",
		"--tools", "", "--no-session-persistence",
		"--setting-sources", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--exclude-dynamic-system-prompt-sections",
	}
	if req.System != "" {
		args = append(args, "--system-prompt", req.System)
	}
	if len(req.JSONSchema) > 0 {
		args = append(args, "--json-schema", string(req.JSONSchema))
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.Effort != "" {
		args = append(args, "--effort", req.Effort)
	}
	if req.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(req.MaxBudgetUSD, 'f', -1, 64))
	}
	return args
}

// modelName — имя модели из modelUsage (единственный ключ), иначе запрошенная.
func modelName(usage map[string]json.RawMessage, requested string) string {
	if len(usage) == 1 {
		for k := range usage {
			return k
		}
	}
	return requested
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
