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
// раунде; команд входа и выхода у игрока нет. Лидер группы не показывается (решение руководителя 2026-10-11), названия
// групп тоже: механика групп геймдизайном не проработана, группа в карточке — только состав и клетка (решение
// руководителя 2026-10-13).
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
	var b model.TextBuilder // ключи и заголовки списков жирным (B40)
	switch {
	case mine != nil:
		b.WriteString("Ваша группа\n")
		if mine.Party.LocationID != nil {
			b.Key("Локация")
			b.WriteString(names[*mine.Party.LocationID] + "\n")
		}
		b.Header("Участники")
		for _, m := range mine.Members {
			b.Printf("\n• %s — %s", m.Nickname, m.Status.Word()) // статус словами (B47)
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
			where = " — " + names[*p.Party.LocationID]
		}
		members := make([]string, 0, len(p.Members))
		for _, m := range p.Members {
			members = append(members, m.Nickname)
		}
		others = append(others, fmt.Sprintf("• %s%s", strings.Join(members, ", "), where))
	}
	if len(others) > 0 {
		b.WriteString("\n\n")
		b.Header("Другие группы")
		b.WriteString("\n" + strings.Join(others, "\n"))
	}
	b.WriteString("\n\nГруппы ведёт рассказчик: кто расходится по разным локациям, становится отдельной группой, пока групп меньше лимита; иначе группа держится вместе.")
	return s.replyFormatted(ctx, req.in, b.Build())
}

// cellLabel — клетка для карточек и `/status`: имя и координаты на сетке (пункт 27), например «Лес (0, 1)».
func cellLabel(l model.Location) string {
	return fmt.Sprintf("%s (%d, %d)", l.Name, l.X, l.Y)
}
