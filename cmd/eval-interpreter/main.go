// Команда eval-interpreter: прогон оценочного набора интерпретатора на живом провайдере `claude-cli`
// (`make eval-interpreter`, пункт 08). Печатает итог по каждому случаю, долю прошедших, токены и длительность.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"taletogether/assets/prompts"
	"taletogether/cmd/internal/evalcost"
	"taletogether/internal/adapter/llm/claudecli"
	"taletogether/internal/config"
	"taletogether/internal/model"
	"taletogether/internal/service/interpreter"
)

// evalSet — файл `interpreter/eval/cases.json`: общий снимок без заявок, модель персонажа, случаи.
type evalSet struct {
	CharacterModel model.CharacterModelDef `json:"character_model"`
	Snapshot       model.RoundSnapshot     `json:"snapshot"`
	Cases          []evalCase              `json:"cases"`
}

type evalCase struct {
	Name        string                     `json:"name"`
	Submissions []model.SnapshotSubmission `json:"submissions"`
	Expect      []expectation              `json:"expect"`
}

// expectation — ожидаемые поля намерения; незаданные не проверяются.
type expectation struct {
	SubmissionID model.SubmissionID `json:"submission_id"`
	ActionType   []string           `json:"action_type"`
	Target       *string            `json:"target"`
	ApproachIdx  *int               `json:"approach_idx"`
	Free         *bool              `json:"free"`
	HasCheck     *bool              `json:"has_check"`
	Attribute    string             `json:"attribute"`
	Push         *bool              `json:"push"`
	Ambiguous    *bool              `json:"ambiguous"`
	NoApproach   *bool              `json:"no_approach"`
	Flavor       *bool              `json:"flavor"` // окраска непуста (ADR-0039)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func run() error {
	only := flag.String("case", "", "запустить только случай с этим именем")
	modelName := flag.String("model", "sonnet", "модель claude-cli (как в профиле local-claude)")
	effort := flag.String("effort", "low", "усилие claude-cli")
	verbose := flag.Bool("v", false, "печатать намерения")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	raw, err := prompts.FS.ReadFile("interpreter/eval/cases.json")
	if err != nil {
		return err
	}
	var set evalSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return fmt.Errorf("cases.json: %w", err)
	}
	if cfg.LLM.ClaudeCLIWorkdir != "" {
		if err := os.MkdirAll(cfg.LLM.ClaudeCLIWorkdir, 0o755); err != nil {
			return err
		}
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	prices := evalcost.Load(context.Background(), cfg.DatabaseURL, logger)
	client := claudecli.New(claudecli.Settings{Path: cfg.LLM.ClaudeCLIPath, Workdir: cfg.LLM.ClaudeCLIWorkdir})
	svc := interpreter.New(interpreter.Func(func(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		req.Model, req.Effort = *modelName, *effort
		resp, err := client.Complete(ctx, req)
		evalcost.Price(prices, &resp)
		if *verbose && err == nil {
			evalcost.Print(resp)
		}
		return resp, err
	}), logger)

	var passed, total, calls, tokensIn, tokensOut, safe int
	var cost float64
	var elapsed time.Duration
	for _, c := range set.Cases {
		if *only != "" && c.Name != *only {
			continue
		}
		total++
		snap := set.Snapshot
		snap.Submissions = c.Submissions
		started := time.Now()
		res, err := svc.Interpret(context.Background(), snap, set.CharacterModel)
		elapsed += time.Since(started)
		calls, tokensIn, tokensOut, safe, cost = calls+res.Calls, tokensIn+res.TokensIn, tokensOut+res.TokensOut, safe+res.Invalid, cost+res.CostUSD
		if err != nil {
			fmt.Printf("FAIL %-26s ошибка вызова: %v\n", c.Name, err)
			continue
		}
		problems := check(c.Expect, res.Intents)
		if len(problems) == 0 {
			passed++
			fmt.Printf("ok   %-26s calls=%d safe=%d\n", c.Name, res.Calls, res.Invalid)
		} else {
			fmt.Printf("FAIL %-26s %s\n", c.Name, strings.Join(problems, "; "))
		}
		if *verbose || len(problems) > 0 {
			for _, in := range res.Intents {
				b, _ := json.Marshal(in)
				fmt.Printf("     %s\n", b)
			}
		}
	}
	if total == 0 {
		return fmt.Errorf("случаев нет")
	}
	fmt.Printf("\nитого: %d/%d (%.0f %%), вызовов %d, безопасных толкований %d, токены вход/выход %d/%d, цена $%.4f, время %s, модель %s/%s\n",
		passed, total, float64(passed)*100/float64(total), calls, safe, tokensIn, tokensOut, cost, elapsed.Round(time.Second), *modelName, *effort)
	if passed*10 < total*9 {
		return fmt.Errorf("порог 90 %% не достигнут")
	}
	return nil
}

// check сверяет намерения с ожиданиями; возвращает расхождения.
func check(expect []expectation, intents []model.Intent) []string {
	byID := map[model.SubmissionID]model.Intent{}
	for _, in := range intents {
		byID[in.SubmissionID] = in
	}
	var out []string
	for _, e := range expect {
		in, ok := byID[e.SubmissionID]
		if !ok {
			out = append(out, fmt.Sprintf("%d: нет намерения", e.SubmissionID))
			continue
		}
		p := func(format string, a ...any) {
			out = append(out, fmt.Sprintf("%d: ", e.SubmissionID)+fmt.Sprintf(format, a...))
		}
		if len(e.ActionType) > 0 && !contains(e.ActionType, string(in.ActionType)) {
			p("action_type %s, ожидалось %v", in.ActionType, e.ActionType)
		}
		if e.Target != nil && in.Target != *e.Target {
			p("target %q, ожидалось %q", in.Target, *e.Target)
		}
		if e.ApproachIdx != nil && (in.ApproachIdx == nil || *in.ApproachIdx != *e.ApproachIdx) {
			p("approach_idx %v, ожидалось %d", in.ApproachIdx, *e.ApproachIdx)
		}
		if e.Free != nil && in.FreeCheck() != *e.Free {
			p("free=%v, ожидалось %v", in.FreeCheck(), *e.Free)
		}
		if e.HasCheck != nil && (in.ApproachIdx != nil || in.FreeCheck()) != *e.HasCheck {
			p("has_check, ожидалось %v", *e.HasCheck)
		}
		if e.Attribute != "" && in.Attribute != e.Attribute {
			p("attribute %q, ожидалось %q", in.Attribute, e.Attribute)
		}
		if e.Push != nil && in.Push != *e.Push {
			p("push=%v, ожидалось %v", in.Push, *e.Push)
		}
		if e.Ambiguous != nil && (len(in.Ambiguities) > 0) != *e.Ambiguous {
			p("ambiguities=%v, ожидалось непусто=%v", in.Ambiguities, *e.Ambiguous)
		}
		if e.NoApproach != nil && *e.NoApproach && in.ApproachIdx != nil {
			p("approach_idx задан, ожидалось без подхода")
		}
		if e.Flavor != nil && (in.Flavor != "") != *e.Flavor {
			p("flavor=%q, ожидалось непусто=%v", in.Flavor, *e.Flavor)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, it := range list {
		if it == v {
			return true
		}
	}
	return false
}
