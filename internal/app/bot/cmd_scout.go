package bot

import (
	"context"

	"taletogether/internal/app/round"
)

// scout — /scout: что в соседних клетках (B36, ADR-0025); в личке — текущая игра пользователя, в группе — игра чата;
// клетка персонажа отправителя, направления жирным (ADR-0029). Переходов среди «Вариантов» нет, куда идти — отсюда,
// заявкой текстом.
func (s *Service) scout(ctx context.Context, req *request) error {
	g, c, err := s.actor(ctx, req)
	if err != nil {
		return err
	}
	locations, err := s.games.Locations(ctx, g.ID)
	if err != nil {
		return err
	}
	text, err := round.ScoutText(c, locations)
	if err != nil {
		return err
	}
	return s.replyFormatted(ctx, req.in, text)
}
