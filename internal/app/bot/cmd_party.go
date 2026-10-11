package bot

import (
	"context"
	"fmt"
	"strings"

	"taletogether/internal/app/game"
	"taletogether/internal/model"
)

// party — /party [create <имя> | join <имя или #id> | leave]: группа персонажа в текущей игре (пункт 19). Лидер группы
// игрокам не показывается (решение руководителя 2026-10-11). Кнопки: вступить в группу, покинуть группу (ADR-0015).
func (s *Service) party(ctx context.Context, req *request) error {
	action, rest := splitArgs(req.in.Args)
	g, err := s.games.Current(ctx, req.user)
	if err != nil {
		return err
	}
	switch strings.ToLower(action) {
	case "":
		return s.partyShow(ctx, req, g)
	case "create":
		if rest == "" {
			return s.reply(ctx, req.in, fmt.Sprintf("Формат: /party create <имя группы от %d до %d символов>", model.PartyNameMin, model.PartyNameMax))
		}
		p, err := s.games.CreateParty(ctx, g, req.user.ID, rest)
		if err != nil {
			return err
		}
		return s.reply(ctx, req.in, fmt.Sprintf("Группа «%s» создана. Позвать других: они пишут /party join %s", p.Name, p.Name))
	case "join":
		if rest == "" {
			return s.reply(ctx, req.in, "Формат: /party join <имя группы>. Список групп: /party")
		}
		p, err := s.games.JoinParty(ctx, g, req.user.ID, rest)
		if err != nil {
			return err
		}
		return s.reply(ctx, req.in, fmt.Sprintf("Вы в группе «%s».", p.Name))
	case "leave":
		p, err := s.games.LeaveParty(ctx, g, req.user.ID)
		if err != nil {
			return err
		}
		return s.reply(ctx, req.in, fmt.Sprintf("Вы покинули группу «%s». Теперь вы действуете в одиночку: помощь и совместные действия доступны только внутри группы.", p.Name))
	}
	return s.reply(ctx, req.in, "Формат: /party — моя группа; /party create <имя>; /party join <имя>; /party leave")
}

// partyShow — карточка группы персонажа и список остальных групп с кнопками вступления.
func (s *Service) partyShow(ctx context.Context, req *request, g model.Game) error {
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
			names[l.ID] = l.Name
		}
	}
	var b strings.Builder
	var kb model.Keyboard
	var mine *game.PartyInfo
	for i := range parties {
		if c.PartyID != nil && parties[i].Party.ID == *c.PartyID {
			mine = &parties[i]
		}
	}
	if mine != nil {
		fmt.Fprintf(&b, "Группа «%s»\n", mine.Party.Name)
		if mine.Party.LocationID != nil {
			fmt.Fprintf(&b, "Локация: %s\n", names[*mine.Party.LocationID])
		}
		b.WriteString("Участники:")
		for _, m := range mine.Members {
			fmt.Fprintf(&b, "\n• %s — %s", m.Nickname, m.Status)
		}
		kb = append(kb, []model.Button{{Text: "Покинуть группу", Data: model.CallbackCommand("party", "leave")}})
	} else if g.Status == model.GameDraft && len(parties) == 0 {
		b.WriteString("Группы ещё не собраны: при старте игры все персонажи соберутся в одну группу.\nСобрать свою заранее: /party create <имя>")
	} else {
		b.WriteString("Вы не в группе: помощь и совместные действия доступны только внутри группы.\nСоздать свою: /party create <имя>")
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
		kb = append(kb, []model.Button{{Text: "Вступить: " + p.Party.Name, Data: model.CallbackCommand("party", fmt.Sprintf("join #%d", p.Party.ID))}})
	}
	if len(others) > 0 {
		b.WriteString("\n\nДругие группы:\n" + strings.Join(others, "\n") + "\n\nВступить: кнопкой или /party join <имя>")
	}
	if len(kb) == 0 {
		return s.reply(ctx, req.in, b.String())
	}
	return s.replyKeyboard(ctx, req.in, b.String(), kb)
}
