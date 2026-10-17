package sim

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"taletogether/internal/model"
)

// Report — итог прогона: транскрипт партии и сводка по БД и журналу.
type Report struct {
	GameID     model.GameID
	Status     model.GameStatus
	Rounds     int     // закрытых раундов
	Calls      int     // вызовов LLM по игре, включая игроков
	CostUSD    float64 // по таблице тарифов
	Stopped    string  // причина досрочной остановки; пусто — до финала
	Transcript string  // Markdown
	Summary    string  // Markdown
}

// report собирает отчёт; ошибки чтения БД попадают в текст сводки, не прерывают его.
func (r *Runner) report(ctx context.Context) Report {
	rep := Report{GameID: r.game.ID, Stopped: r.stopped}
	rep.Transcript = r.transcript()
	rep.Summary = r.summary(ctx, &rep)
	return rep
}

// transcript — партия по раундам глазами группы и каждого игрока: сообщения бота, действия и мысли игроков.
func (r *Runner) transcript() string {
	r.mu.Lock()
	entries := append([]entry(nil), r.entries...)
	marks := append([]mark(nil), r.marks...)
	r.mu.Unlock()
	for _, m := range r.tg.Messages(r.group, 0) {
		entries = append(entries, entry{At: m.SentAt, Who: "бот", Where: "группа", Text: m.Text.Text})
	}
	for _, p := range r.players {
		for _, m := range r.tg.Messages(p.private, 0) {
			entries = append(entries, entry{At: m.SentAt, Who: "бот", Where: "личка " + p.persona.Name, Text: m.Text.Text + buttons(m)})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].At.Before(entries[j].At) })
	var b strings.Builder
	fmt.Fprintf(&b, "# Транскрипт прогона «%s»\n\nИгра %d. Игроки:", r.sc.Name, r.game.ID)
	for _, p := range r.players {
		fmt.Fprintf(&b, " %s (раунд входа %d);", p.persona.Name, p.persona.JoinsRound)
	}
	b.WriteString("\n\n## До старта\n")
	mi := 0
	for _, e := range entries {
		for mi < len(marks) && !e.At.Before(marks[mi].At) {
			fmt.Fprintf(&b, "\n## Раунд %d\n", marks[mi].Round)
			mi++
		}
		switch {
		case e.Thought:
			fmt.Fprintf(&b, "\n> _%s думает: %s_\n", e.Who, e.Text)
		case e.Where == "заметка":
			fmt.Fprintf(&b, "\n> ⚙ %s\n", e.Text)
		case e.Player:
			fmt.Fprintf(&b, "\n**%s → %s:** %s\n", e.Who, e.Where, e.Text)
		default:
			fmt.Fprintf(&b, "\n**бот → %s** (%s):\n\n%s\n", e.Where, e.At.Format("15:04:05"), indent(e.Text))
		}
	}
	return b.String()
}

// buttons — кнопки сообщения строкой для транскрипта.
func buttons(m model.SentMessage) string {
	var labels []string
	for _, row := range m.Keyboard {
		for _, btn := range row {
			labels = append(labels, "«"+btn.Text+"»")
		}
	}
	if len(labels) == 0 {
		return ""
	}
	return "\n[кнопки: " + strings.Join(labels, " ") + "]"
}

// indent оформляет текст бота цитатой Markdown.
func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// summary — сводка: раунды, вызовы по ролям, рост контекста, карта и квесты, ошибки.
func (r *Runner) summary(ctx context.Context, rep *Report) string {
	var b strings.Builder
	var problems []string
	g, err := r.store.GetGame(ctx, r.game.ID)
	if err != nil {
		problems = append(problems, "игра: "+err.Error())
		g = r.game
	}
	rep.Status = g.Status
	var rounds []model.Round
	for no := 1; ; no++ {
		rd, err := r.store.RoundByNo(ctx, g.ID, no)
		if err != nil {
			break
		}
		rounds = append(rounds, rd)
	}
	rep.Rounds = 0
	roundNo := map[model.RoundID]int{}
	for _, rd := range rounds {
		roundNo[rd.ID] = rd.RoundNo
		if rd.Status != model.RoundOpen {
			rep.Rounds++
		}
	}
	runs, err := r.store.ListLLMRuns(ctx, 10000)
	if err != nil {
		problems = append(problems, "llm_runs: "+err.Error())
	}
	var mine []model.LLMRun
	for _, run := range runs {
		if run.GameID != nil && *run.GameID == g.ID {
			mine = append(mine, run)
			rep.Calls++
			rep.CostUSD += run.CostUSD
		}
	}

	fmt.Fprintf(&b, "# Сводка прогона «%s»\n\n", r.sc.Name)
	fmt.Fprintf(&b, "- Игра: %d «%s», статус `%s`, раундов закрыто: %d из %d.\n", g.ID, g.Title, g.Status, rep.Rounds, r.sc.Rounds)
	if r.sc.Endless {
		fmt.Fprintf(&b, "- Бесконечная игра: границы рассказа нет (`max_rounds 0`), после раунда %d администратор завершил её командой `/finish yes` (ADR-0037).\n", r.sc.Rounds)
	}
	fmt.Fprintf(&b, "- Профиль `%s` (`%s`): %s; вызовов LLM: %d, стоимость по тарифам: $%.4f.\n", r.sc.Profile, r.profile.Provider, r.modelsLine(), rep.Calls, rep.CostUSD)
	if rep.Stopped != "" {
		fmt.Fprintf(&b, "- Остановка: %s.\n", rep.Stopped)
	} else {
		b.WriteString("- Партия доиграна до финала.\n")
	}

	b.WriteString("\n## Раунды\n\n| № | Статус | Заявок | Разрешено / отказ / безопасно | Запасной разбор | Шаблон хроники | Вызовов | Токены вход / выход | $ | Время, с | Замечаний |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, rd := range rounds {
		m := rd.Metrics
		if m == nil {
			fmt.Fprintf(&b, "| %d | %s | | | | | | | | | |\n", rd.RoundNo, rd.Status)
			continue
		}
		fmt.Fprintf(&b, "| %d | %s | %d | %d / %d / %d | %s | %s | %d | %d / %d | %.4f | %.1f | %d |\n", rd.RoundNo, rd.Status, m.Submissions,
			m.Resolved, m.Rejected, m.Ambiguous, yesNo(m.InterpreterFallback), yesNo(m.NarratorFallback), m.LLMCalls, m.TokensIn, m.TokensOut, m.CostUSD,
			float64(m.TotalMS)/1000, m.Issues)
	}

	b.WriteString("\n## Вызовы по ролям\n\n| Роль | Вызовов | Ошибок | Вход без кэша | Запись кэша | Чтение кэша | Выход | $ | Среднее время, с |\n|---|---|---|---|---|---|---|---|---|\n")
	type agg struct {
		calls, errs, in, cw, cr, out int
		cost                         float64
		dur                          int64
	}
	byRole := map[model.LLMRole]*agg{}
	var roles []model.LLMRole
	for _, run := range mine {
		a := byRole[run.Role]
		if a == nil {
			a = &agg{}
			byRole[run.Role] = a
			roles = append(roles, run.Role)
		}
		a.calls++
		if run.Error != "" {
			a.errs++
		}
		a.in += run.TokensIn - run.TokensCacheWrite - run.TokensCacheRead
		a.cw += run.TokensCacheWrite
		a.cr += run.TokensCacheRead
		a.out += run.TokensOut
		a.cost += run.CostUSD
		a.dur += run.DurationMS
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	for _, role := range roles {
		a := byRole[role]
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %.4f | %.1f |\n", role, a.calls, a.errs, a.in, a.cw, a.cr, a.out, a.cost, float64(a.dur)/1000/float64(a.calls))
	}

	b.WriteString("\n## Рост контекста по раундам\n\nТокены входа (все виды) и размер системной части и входа в символах по записям журнала; рост входа от раунда к раунду без роста числа заявок — кандидат в утечки.\n\n| Раунд | Роль | Вызовов | Токены вход | Токены выход | Системная часть, симв. | Вход, симв. | Выход, симв. |\n|---|---|---|---|---|---|---|---|\n")
	type key struct {
		round int
		role  model.LLMRole
	}
	type size struct {
		calls, tin, tout, sys, in, out int
	}
	bySize := map[key]*size{}
	var keys []key
	var callErrors []string
	if r.journal != nil {
		for _, c := range r.journal.LLMCalls() {
			k := key{roundNo[c.RoundID], c.Role}
			s := bySize[k]
			if s == nil {
				s = &size{}
				bySize[k] = s
				keys = append(keys, k)
			}
			s.calls++
			s.tin += c.TokensIn
			s.tout += c.TokensOut
			s.sys += c.SystemChars
			s.in += c.InputChars
			s.out += c.OutputChars
			if c.Error != "" {
				callErrors = append(callErrors, fmt.Sprintf("раунд %d, %s: %s", k.round, c.Role, truncate(c.Error, 200)))
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].round != keys[j].round {
			return keys[i].round < keys[j].round
		}
		return keys[i].role < keys[j].role
	})
	for _, k := range keys {
		s := bySize[k]
		fmt.Fprintf(&b, "| %d | %s | %d | %d | %d | %d | %d | %d |\n", k.round, k.role, s.calls, s.tin, s.tout, s.sys, s.in, s.out)
	}

	b.WriteString("\n## Карта и квесты\n\n")
	if locs, err := r.store.ListLocations(ctx, g.ID); err != nil {
		problems = append(problems, "локации: "+err.Error())
	} else {
		visited, detailed, hidden := 0, 0, 0
		for _, l := range locs {
			if l.VisitedAt != nil {
				visited++
			}
			if l.Stage == model.StageDetailed {
				detailed++
			}
			if l.Hidden {
				hidden++
			}
		}
		fmt.Fprintf(&b, "- Клеток: %d, посещено: %d, с подробным описанием: %d, скрытых: %d.\n", len(locs), visited, detailed, hidden)
		var names []string
		for _, l := range locs {
			if l.VisitedAt != nil {
				names = append(names, fmt.Sprintf("%s (%d,%d)", l.Name, l.X, l.Y))
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(&b, "- Посещённые: %s.\n", strings.Join(names, "; "))
		}
	}
	if quests, err := r.store.ListQuests(ctx, g.ID); err != nil {
		problems = append(problems, "квесты: "+err.Error())
	} else {
		done, stagesDone, stages := 0, 0, 0
		for _, q := range quests {
			if q.Status == model.QuestDone {
				done++
			}
			for _, st := range q.Stages {
				stages++
				if st.Status == model.StageDone {
					stagesDone++
				}
			}
		}
		fmt.Fprintf(&b, "- Квестов: %d, закрыто: %d; этапов: %d, пройдено: %d.\n", len(quests), done, stages, stagesDone)
		for _, q := range quests {
			var st []string
			for _, s := range q.Stages {
				st = append(st, fmt.Sprintf("%s:%s", s.Title, s.Status))
			}
			fmt.Fprintf(&b, "  - [%s] «%s» (%s): %s\n", q.Kind, q.Title, q.Status, strings.Join(st, "; "))
		}
	}
	if goals, err := r.store.ListFactionGoals(ctx, g.ID); err != nil {
		problems = append(problems, "цели фракций: "+err.Error())
	} else {
		for _, goal := range goals {
			fmt.Fprintf(&b, "- Цель фракции %d (v%d, %s): %s\n", goal.FactionID, goal.Version, goal.Stage, goal.Text)
		}
	}

	b.WriteString("\n## Ошибки и сбои\n\n")
	if ob, err := r.store.ListOutbox(ctx, g.ID); err != nil {
		problems = append(problems, "outbox: "+err.Error())
	} else {
		failed := 0
		for _, m := range ob {
			if m.Status == model.OutboxFailed || m.Status == model.OutboxUnknown {
				failed++
			}
		}
		fmt.Fprintf(&b, "- Outbox: сообщений %d, не доставлено: %d.\n", len(ob), failed)
	}
	skipped, failedRounds := 0, 0
	for _, rd := range rounds {
		switch rd.Status {
		case model.RoundSkipped:
			skipped++
		case model.RoundFailed:
			failedRounds++
		}
	}
	fmt.Fprintf(&b, "- Раундов пропущено: %d, со сбоем: %d.\n", skipped, failedRounds)
	fmt.Fprintf(&b, "- Ошибок вызовов LLM: %d.\n", len(callErrors))
	for _, e := range callErrors {
		fmt.Fprintf(&b, "  - %s\n", e)
	}
	if r.journal != nil {
		errs := r.journal.Errors()
		fmt.Fprintf(&b, "- Ошибок сценариев, показанных игрокам: %d.\n", len(errs))
		for _, e := range errs {
			fmt.Fprintf(&b, "  - %s: %s\n", e.TS.Format(time.TimeOnly), truncate(e.Error, 200))
		}
	}
	for _, p := range problems {
		fmt.Fprintf(&b, "- Сводка неполна: %s.\n", p)
	}
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "да"
	}
	return "нет"
}

// modelsLine — фактические модели ролей для шапки сводки; подменённые флагом помечены.
func (r *Runner) modelsLine() string {
	if r.profile.Name == "" {
		return "профиль не загружен"
	}
	part := func(title string, role model.LLMRole) string {
		m := r.roleModel(role)
		if role == model.RoleSimPlayer {
			if r.settings.Models[role] != "" {
				return fmt.Sprintf("%s `%s` (флаг)", title, m)
			}
			return fmt.Sprintf("%s `%s`", title, m)
		}
		if r.settings.Models[role] != "" {
			return fmt.Sprintf("%s `%s` (флаг, в профиле `%s`)", title, m, r.profile.Model(role))
		}
		return fmt.Sprintf("%s `%s`", title, m)
	}
	return part("интерпретатор", model.RoleInterpreter) + ", " + part("рассказчик", model.RoleNarrator) + ", " + part("игрок", model.RoleSimPlayer)
}
