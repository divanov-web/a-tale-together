package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"taletogether/internal/app/game"
	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// quests — /quests: цель и квесты фракции персонажа (пункт 28, ADR-0020). Кнопки «взять квест» нет: квест живёт в мире,
// рассказчик намекает, код закрывает этапы по условиям. Этапы: закрыт, ждёт в клетке (с подсказкой, что нужно),
// ещё не проявился.
func (s *Service) quests(ctx context.Context, req *request) error {
	g, err := s.games.Current(ctx, req.user)
	if err != nil {
		return err
	}
	c, err := s.games.Character(ctx, g, req.user.ID)
	if errors.Is(err, game.ErrNoCharacter) {
		return s.reply(ctx, req.in, "Персонажа в игре «"+g.Title+"» ещё нет. Создать: /create")
	}
	if err != nil {
		return err
	}
	factions, err := s.games.Factions(ctx, g.ID)
	if err != nil {
		return err
	}
	faction := model.Faction{}
	for _, f := range factions {
		if f.ID == c.FactionID {
			faction = f
		}
	}
	goals, err := s.games.Goals(ctx, g.ID)
	if err != nil {
		return err
	}
	quests, err := s.games.Quests(ctx, g.ID)
	if err != nil {
		return err
	}
	cells := map[model.LocationID]model.Location{}
	if locs, err := s.games.Locations(ctx, g.ID); err == nil {
		for _, l := range locs {
			cells[l.ID] = l
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Фракция «%s»\n", faction.Title)
	goalText := ""
	for _, goal := range goals {
		if goal.FactionID == faction.ID {
			goalText = goal.Text
			if goal.Stage != "" {
				goalText += " (" + goal.Stage + ")"
			}
		}
	}
	if goalText == "" {
		goalText = "ещё не определена: появится при старте игры"
	}
	b.WriteString("Цель: " + goalText + "\n")
	shown := 0
	for _, q := range quests {
		if q.FactionID != faction.ID {
			continue
		}
		shown++
		kind := "сюжетный"
		if q.Kind == model.QuestSide {
			kind = "побочный"
		}
		status := ""
		if q.Status == model.QuestDone {
			status = ", завершён"
		}
		fmt.Fprintf(&b, "\n«%s» (%s%s)", q.Title, kind, status)
		if q.Summary != "" {
			b.WriteString("\n" + q.Summary)
		}
		for _, st := range q.Stages {
			switch st.Status {
			case model.StageDone:
				fmt.Fprintf(&b, "\n✓ %s", st.Title)
			case model.StageBound:
				where := ""
				hint := ""
				if st.LocationID != nil {
					if l, ok := cells[*st.LocationID]; ok {
						where = " — " + cellLabel(l)
						hint = narrator.ConditionWordsFor(st.Condition, l.Objects)
					}
				}
				fmt.Fprintf(&b, "\n▶ %s%s", st.Title, where)
				if hint != "" {
					b.WriteString(": " + hint)
				}
			default:
				fmt.Fprintf(&b, "\n○ %s", st.Title)
			}
		}
	}
	if shown == 0 {
		b.WriteString("\nКвестов пока нет: первый сюжетный появится при старте игры.")
	}
	b.WriteString("\n\nКвесты живут в мире: рассказчик намекает на ближайший этап, а закрывает его игра по вашим действиям. ✓ — сделано, ▶ — этап проявился в клетке, ○ — впереди.")
	return s.reply(ctx, req.in, b.String())
}
