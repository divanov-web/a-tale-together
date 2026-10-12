package bot

import (
	"context"
	"fmt"
	"strings"

	"taletogether/internal/app/game"
	"taletogether/internal/model"
)

// party — /party: карточка группы персонажа в текущей игре (пункт 19, ADR-0017). Группы собирает и ведёт код:
// при старте все в одной группе, новичок входит в группу своей локации, расхождение делит или удерживает группу в
// раунде; команд входа и выхода у игрока нет. Лидер группы не показывается (решение руководителя 2026-10-11).
func (s *Service) party(ctx context.Context, req *request) error {
	g, err := s.games.Current(ctx, req.user)
	if err != nil {
		return err
	}
	c, err := s.games.Character(ctx, g, req.user.ID)
	if err != nil {
		return err
	}
	parties, err := s.games.Parties(ctx, g)
	if err != nil {
		return err
	}
	names := map[model.LocationID]string{}
	if locs, err := s.games.Locations(ctx, g.ID); err == nil {
		for _, l := range locs {
			names[l.ID] = cellLabel(l)
		}
	}

	var mine *game.PartyInfo
	for i := range parties {
		if c.PartyID != nil && parties[i].Party.ID == *c.PartyID {
			mine = &parties[i]
		}
	}
	var b strings.Builder
	switch {
	case mine != nil:
		fmt.Fprintf(&b, "Группа «%s»\n", mine.Party.Name)
		if mine.Party.LocationID != nil {
			fmt.Fprintf(&b, "Локация: %s\n", names[*mine.Party.LocationID])
		}
		b.WriteString("Участники:")
		for _, m := range mine.Members {
			fmt.Fprintf(&b, "\n• %s — %s", m.Nickname, m.Status)
		}
	case g.Status == model.GameDraft:
		b.WriteString("Группы соберутся при старте игры: все персонажи войдут в одну группу.")
	default:
		b.WriteString("Группа определится в ближайшем раунде: персонаж войдёт в группу своей локации.")
	}
	var others []string
	for _, p := range parties {
		if mine != nil && p.Party.ID == mine.Party.ID {
			continue
		}
		where := ""
		if p.Party.LocationID != nil {
			where = ", " + names[*p.Party.LocationID]
		}
		members := make([]string, 0, len(p.Members))
		for _, m := range p.Members {
			members = append(members, m.Nickname)
		}
		others = append(others, fmt.Sprintf("• %s (%d%s): %s", p.Party.Name, len(p.Members), where, strings.Join(members, ", ")))
	}
	if len(others) > 0 {
		b.WriteString("\n\nДругие группы:\n" + strings.Join(others, "\n"))
	}
	b.WriteString("\n\nГруппы ведёт рассказчик: кто расходится по разным локациям, становится отдельной группой, пока групп меньше лимита; иначе группа держится вместе.")
	return s.reply(ctx, req.in, b.String())
}

// cellLabel — клетка для карточек и `/status`: имя и координаты на сетке (пункт 27), например «Лес (0, 1)».
func cellLabel(l model.Location) string {
	return fmt.Sprintf("%s (%d, %d)", l.Name, l.X, l.Y)
}
