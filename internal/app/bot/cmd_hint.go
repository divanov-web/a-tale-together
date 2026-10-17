package bot

import (
	"context"
	"fmt"

	"taletogether/internal/model"
	"taletogether/internal/service/rules"
)

// hint — /hint и кнопка «Подсказка»: просьба о подсказке рассказчика для фракции персонажа (пункт 35, ADR-0038).
// В личке — текущая игра пользователя, в группе — игра чата (решение руководителя 2026-10-17: команда в обоих
// контекстах); ответ и отказ в группе адресованы персонажу по нику (B50). Подсказка публичная: рассказчик вплетает
// её в хронику ближайшего раунда; отказы — просьба уже есть или перезарядка 5 раундов на фракцию.
func (s *Service) hint(ctx context.Context, req *request) error {
	g, c, err := s.actor(ctx, req)
	if err != nil {
		return err
	}
	r, err := s.rounds.Hint(ctx, g, c)
	if err != nil {
		return addressed(req, c, err)
	}
	text := fmt.Sprintf("подсказка появится в тексте рассказчика в следующем раунде (по итогам раунда %d). Следующую можно попросить через %d раундов.", r.RoundNo, rules.CooldownRounds[model.CooldownHint])
	if !req.in.IsPrivate() {
		return s.reply(ctx, req.in, c.Nickname+": "+text)
	}
	return s.reply(ctx, req.in, upperFirst(text))
}
