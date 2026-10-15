// Команда eval-narrator: прогон оценочного набора рассказчика на живом провайдере `claude-cli`
// (`make eval-narrator`, пункт 09). Проверяет, что каждое событие упомянуто самой моделью (без дописывания кодом),
// запрещённые слова и секреты в хронике отсутствуют, нужные никнеймы присутствуют; печатает долю прошедших, токены и время.
// Флаг `-opening` вместо набора запускает завязку истории (ADR-0018) по миру первого случая: печатает текст и замечания.
// Флаг `-worldgen` вместо набора запускает нулевой вызов «генерация мира» (ADR-0028) по жанру набора: печатает варианты.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"taletogether/assets/prompts"
	"taletogether/internal/adapter/llm/claudecli"
	"taletogether/internal/config"
	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// evalSet — файл `narrator/eval/cases.json`: общие мир, фракции и модель персонажа, случаи с параметрами и ожиданиями.
type evalSet struct {
	Title          string                  `json:"title"`
	Tone           string                  `json:"tone"`
	Factions       []narrator.Faction      `json:"factions"`
	CharacterModel model.CharacterModelDef `json:"character_model"`
	Cases          []evalCase              `json:"cases"`
}

type evalCase struct {
	Name      string     `json:"name"`
	Comment   string     `json:"comment"`
	Params    caseParams `json:"params"`
	Required  []string   `json:"required"`  // подстроки, которые должны быть в тексте
	Forbidden []string   `json:"forbidden"` // подстроки, которых быть не должно (без учёта регистра)
	NoDigits  bool       `json:"no_digits"` // в тексте нет цифр
	World     *caseWorld `json:"world"`     // ожидания к пакету мира (пункт 27); nil — пакет не проверяется
}

// caseWorld — ожидания к пакету мира: число клеток и карточек от модели, минимум объектов в клетке, без шаблонов;
// пункт 28: число квестов, размещённых этапов и целей по заданиям; B43: подстроки, которых нет в тексте и стадии целей
// (срок, дедлайн, счётчик времени).
type caseWorld struct {
	Cells          int      `json:"cells"`
	Neighbors      int      `json:"neighbors"`
	MinObjects     int      `json:"min_objects"`
	Quests         int      `json:"quests"`
	Stages         int      `json:"stages"`
	Goals          int      `json:"goals"`
	GoalsForbidden []string `json:"goals_forbidden"` // подстроки, которых нет в целях (без учёта регистра)
}

type caseParams struct {
	Mode     model.GameMode                     `json:"mode"`
	RoundNo  int                                `json:"round_no"`
	Events   []model.Event                      `json:"events"`
	Bios     map[model.CharacterID]narrator.Bio `json:"bios"`
	Previous []model.Narration                  `json:"previous"`
	State    *model.RoundSnapshot               `json:"state"`
	Tasks    narrator.Tasks                     `json:"tasks"`    // задания на достройку мира (пункт 27) и квесты (пункт 28)
	Quests   []narrator.QuestInfo               `json:"quests"`   // активные квесты для подсказок (пункт 28)
	Speeches []model.SnapshotSpeech             `json:"speeches"` // реплики персонажей (пункт 32, ADR-0027)
	Lair     *narrator.LairInfo                 `json:"lair"`     // логово сил зла как фон (B42, ADR-0031)
}

var digits = regexp.MustCompile(`\d`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func run() error {
	only := flag.String("case", "", "запустить только случай с этим именем")
	modelName := flag.String("model", "opus", "модель claude-cli (как в профиле local-claude)")
	effort := flag.String("effort", "low", "усилие claude-cli")
	verbose := flag.Bool("v", false, "печатать хронику")
	opening := flag.Bool("opening", false, "вместо набора: завязка истории по миру первого случая")
	worldgen := flag.Bool("worldgen", false, "вместо набора: варианты стартового мира по жанру набора (ADR-0028)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	raw, err := prompts.FS.ReadFile("narrator/eval/cases.json")
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
	client := claudecli.New(claudecli.Settings{Path: cfg.LLM.ClaudeCLIPath, Workdir: cfg.LLM.ClaudeCLIWorkdir})
	svc := narrator.New(narrator.Func(func(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		req.Model, req.Effort = *modelName, *effort
		return client.Complete(ctx, req)
	}), slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	if *opening {
		return runOpening(svc, set)
	}
	if *worldgen {
		return runWorldgen(svc, set)
	}
	var passed, total, calls, tokensIn, tokensOut, patched int
	var elapsed time.Duration
	for _, c := range set.Cases {
		if *only != "" && c.Name != *only {
			continue
		}
		total++
		p := narrator.Params{Title: set.Title, Tone: set.Tone, Factions: set.Factions, Def: set.CharacterModel,
			Mode: c.Params.Mode, RoundNo: c.Params.RoundNo, Events: c.Params.Events, Bios: c.Params.Bios, Previous: c.Params.Previous, State: c.Params.State, Tasks: c.Params.Tasks, Quests: c.Params.Quests, Speeches: c.Params.Speeches, Lair: c.Params.Lair}
		started := time.Now()
		res, err := svc.Narrate(context.Background(), p)
		elapsed += time.Since(started)
		calls, tokensIn, tokensOut, patched = calls+res.Calls, tokensIn+res.TokensIn, tokensOut+res.TokensOut, patched+res.Patched
		if err != nil {
			fmt.Printf("FAIL %-22s ошибка вызова: %v\n", c.Name, err)
			continue
		}
		problems := check(c, res)
		if len(problems) == 0 {
			passed++
			fmt.Printf("ok   %-22s calls=%d issues=%d\n", c.Name, res.Calls, len(res.Issues))
		} else {
			fmt.Printf("FAIL %-22s %s\n", c.Name, strings.Join(problems, "; "))
		}
		if *verbose || len(problems) > 0 {
			fmt.Printf("     %s\n", res.Narration.Chronicle)
			for _, sc := range res.Narration.Scenes {
				fmt.Printf("     [%d %s] %s %v\n", sc.LocationID, sc.Party, sc.Text, sc.EventSeqs)
			}
			for _, cell := range res.World.Cells {
				fmt.Printf("     [клетка %d] %s (%d объектов, шаблон=%v): %s\n", cell.LocationID, cell.Name, len(cell.Objects), cell.Template, cell.Description)
			}
			printQuests(res.World.Quests, res.World.Stages, res.World.Goals)
			for _, n := range res.World.Neighbors {
				fmt.Printf("     [сосед %d,%d] %s (%s): %s\n", n.X, n.Y, n.Name, n.Terrain, n.Card)
			}
			for _, is := range res.Issues {
				fmt.Printf("     замечание: %s\n", is)
			}
		}
	}
	if total == 0 {
		return fmt.Errorf("случаев нет")
	}
	fmt.Printf("\nитого: %d/%d (%.0f %%), вызовов %d, дописано кодом %d, токены вход/выход %d/%d, время %s, модель %s/%s\n",
		passed, total, float64(passed)*100/float64(total), calls, patched, tokensIn, tokensOut, elapsed.Round(time.Second), *modelName, *effort)
	if passed*10 < total*9 {
		return fmt.Errorf("порог 90 %% не достигнут")
	}
	return nil
}

// check сверяет хронику с ожиданиями случая; возвращает расхождения.
func check(c evalCase, res narrator.Result) []string {
	var out []string
	if res.Patched > 0 {
		out = append(out, fmt.Sprintf("модель не упомянула события, дописано кодом: %v", res.Narration.Patched))
	}
	var b strings.Builder
	b.WriteString(res.Narration.Chronicle)
	for _, sc := range res.Narration.Scenes {
		b.WriteString("\n" + sc.Party + "\n" + sc.Text)
	}
	text := b.String()
	lower := strings.ToLower(text)
	for _, s := range c.Required {
		if !strings.Contains(text, s) {
			out = append(out, fmt.Sprintf("нет «%s»", s))
		}
	}
	for _, s := range c.Forbidden {
		if strings.Contains(lower, strings.ToLower(s)) {
			out = append(out, fmt.Sprintf("есть «%s»", s))
		}
	}
	if c.NoDigits && digits.MatchString(text) {
		out = append(out, "есть цифры")
	}
	if w := c.World; w != nil {
		if len(res.World.Cells) != w.Cells || len(res.World.Neighbors) != w.Neighbors {
			out = append(out, fmt.Sprintf("пакет мира: клеток %d/%d, соседей %d/%d", len(res.World.Cells), w.Cells, len(res.World.Neighbors), w.Neighbors))
		}
		if n := res.World.Templated(); n > 0 {
			out = append(out, fmt.Sprintf("пакет мира: %d заданий закрыто шаблоном", n))
		}
		for _, cell := range res.World.Cells {
			if len(cell.Objects) < w.MinObjects {
				out = append(out, fmt.Sprintf("клетка «%s»: объектов %d, нужно не меньше %d", cell.Name, len(cell.Objects), w.MinObjects))
			}
		}
		if len(res.World.Quests) != w.Quests || len(res.World.Stages) != w.Stages || len(res.World.Goals) != w.Goals {
			out = append(out, fmt.Sprintf("квесты: %d/%d, этапов %d/%d, целей %d/%d", len(res.World.Quests), w.Quests, len(res.World.Stages), w.Stages, len(res.World.Goals), w.Goals))
		}
		for _, st := range res.World.Stages {
			if st.Condition.Kind == model.ConditionVisited && st.Stage.Condition.Kind != model.ConditionVisited {
				out = append(out, fmt.Sprintf("этап «%s»: условие заменено на visited (недостижимо)", st.Stage.Title))
			}
		}
		for _, g := range res.World.Goals {
			goal := strings.ToLower(g.Text + "\n" + g.Stage)
			for _, s := range w.GoalsForbidden {
				if strings.Contains(goal, strings.ToLower(s)) {
					out = append(out, fmt.Sprintf("цель фракции %d: есть «%s»", g.FactionID, s))
				}
			}
		}
	}
	return out
}

// printQuests печатает принятые квесты, размещения этапов и цели.
func printQuests(quests []narrator.GeneratedQuest, stages []narrator.GeneratedStage, goals []narrator.GeneratedGoal) {
	for _, q := range quests {
		fmt.Printf("     [квест %s, фракция %d, шаблон=%v] %s: %s\n", q.Kind, q.FactionID, q.Template, q.Title, q.Summary)
		for i, st := range q.Stages {
			fmt.Printf("       %d. %s (%s, через %d): %s → %s\n", i+1, st.Title, st.Condition.Kind, st.Distance, st.Summary, st.Reward.Fact)
		}
	}
	for _, st := range stages {
		fmt.Printf("     [этап «%s» в клетке %d, шаблон=%v] условие %+v, объектов %d\n", st.Stage.Title, st.LocationID, st.Template, st.Condition, len(st.Objects))
	}
	for _, g := range goals {
		fmt.Printf("     [цель фракции %d, шаблон=%v] %s (%s)\n", g.FactionID, g.Template, g.Text, g.Stage)
	}
}

// runOpening — живой вызов завязки и стартового пакета по миру, первой локации и персонажам первого случая набора;
// проверка кодом: объём, цифры, никнеймы (замечания печатаются, после дозапроса не считаются ошибкой), пакет: клетка
// с объектами и четыре карточки соседей.
// runWorldgen — нулевой вызов по жанру набора: печатает принятые варианты, замечания, токены и время.
func runWorldgen(svc *narrator.Service, set evalSet) error {
	p := narrator.WorldgenParams{Genre: "Классическое фэнтези", Tone: set.Tone, Factions: set.Factions, Seed: fmt.Sprintf("eval-%d", time.Now().Unix())}
	if len(set.Cases) > 0 && set.Cases[0].Params.State != nil && len(set.Cases[0].Params.State.Locations) > 0 {
		start := set.Cases[0].Params.State.Locations[0]
		p.Start = narrator.SetupStart{Name: start.Name, Terrain: start.Terrain, Description: start.Description}
	}
	started := time.Now()
	res, err := svc.Worldgen(context.Background(), p)
	for _, is := range res.Issues {
		fmt.Printf("замечание: %s\n", is)
	}
	if err != nil {
		return fmt.Errorf("генерация мира: %w", err)
	}
	for i, o := range res.Options {
		fmt.Printf("%d. %s — «%s»\n   %s\n   тон: %s\n   завязка: %s\n", i+1, o.Title, o.World, o.Summary, o.Tone, o.Premise)
		for _, f := range o.Factions {
			fmt.Printf("   [%s] %s — %s (%s); цель: %s\n", f.Code, f.Title, f.Description, f.Tone, f.Goal)
		}
		fmt.Printf("   старт: %s (%s): %s\n", o.Start.Name, o.Start.Terrain, o.Start.Description)
	}
	fmt.Printf("\nвариантов %d, токены вход/выход %d/%d, время %s\n", len(res.Options), res.TokensIn, res.TokensOut, time.Since(started).Round(time.Second))
	return nil
}

func runOpening(svc *narrator.Service, set evalSet) error {
	if len(set.Cases) == 0 || set.Cases[0].Params.State == nil || len(set.Cases[0].Params.State.Locations) == 0 {
		return fmt.Errorf("в наборе нет случая с состоянием")
	}
	st := set.Cases[0].Params.State
	start := st.Locations[0]
	p := narrator.OpeningParams{Title: set.Title, Genre: "Классическое фэнтези", Tone: set.Tone, Mode: model.ModeBalanced, Factions: set.Factions, Def: set.CharacterModel,
		Start: narrator.OpeningStart{LocationID: start.ID, Name: start.Name, Description: start.Description, Terrain: start.Terrain, Objects: []string{}}}
	// Пункт 28: цель и сюжетный квест каждой фракции (идентификатор фракции — её номер в наборе).
	for i, f := range set.Factions {
		minStages, maxStages := model.StageBounds(model.QuestStory)
		p.Quests = append(p.Quests, narrator.QuestTask{FactionID: model.FactionID(i + 1), Faction: f.Code, Kind: model.QuestStory, MinStages: minStages, MaxStages: maxStages, Mandatory: true})
		p.Goals = append(p.Goals, narrator.GoalTask{FactionID: model.FactionID(i + 1), Faction: f.Code, Fallback: f.Description})
	}
	// B42 (ADR-0031): логово второй стороны в одном из углов `model.LairCorners`.
	if len(set.Factions) > 1 {
		lair := model.LairCorners[1]
		p.Lair = &narrator.LairTask{X: lair.X, Y: lair.Y, Bearing: model.Coord{X: start.X, Y: start.Y}.Bearing(lair), Faction: set.Factions[1].Code, Title: set.Factions[1].Title}
	}
	for _, o := range start.Objects {
		p.Start.Objects = append(p.Start.Objects, o.Title)
	}
	for _, d := range model.Directions {
		dx, dy := d.Offset()
		p.Neighbors = append(p.Neighbors, narrator.NeighborTask{X: start.X + dx, Y: start.Y + dy, Direction: d, Of: start.ID, OfName: start.Name})
	}
	for _, c := range st.Characters {
		p.Characters = append(p.Characters, narrator.OpeningCharacter{Nickname: c.Nickname, Gender: string(c.Gender), PublicBio: set.Cases[0].Params.Bios[c.ID].Public})
	}
	started := time.Now()
	res, err := svc.Opening(context.Background(), p)
	if err != nil {
		return fmt.Errorf("завязка: %w", err)
	}
	fmt.Println(res.Text)
	if res.Start != nil {
		fmt.Printf("\n[старт] %s: %s\n", res.Start.Name, res.Start.Description)
		for _, o := range res.Start.Objects {
			fmt.Printf("  объект %s «%s» (%d подходов)\n", o.Key, o.Title, len(o.Approaches))
		}
	}
	printQuests(res.Quests, nil, res.Goals)
	if l := res.Lair; l != nil {
		fmt.Printf("[логово %d,%d] %s (%s, шаблон=%v): %s; издалека: %q\n", l.X, l.Y, l.Name, l.Terrain, l.Template, l.Card, l.DistantView)
	}
	for _, n := range res.Neighbors {
		fmt.Printf("[сосед %d,%d] %s (%s, шаблон=%v): %s\n", n.X, n.Y, n.Name, n.Terrain, n.Template, n.Card)
	}
	for _, is := range res.Issues {
		fmt.Printf("замечание: %s\n", is)
	}
	fmt.Printf("\nвызовов %d, токены вход/выход %d/%d, время %s\n", res.Calls, res.TokensIn, res.TokensOut, time.Since(started).Round(time.Second))
	return nil
}
