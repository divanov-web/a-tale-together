package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"taletogether/internal/app/round"
	"taletogether/internal/model"
)

// away — /away в личке (пункт 20, ADR-0026): без аргумента — вопрос с кнопками «Уйти» / «Отмена», `yes` — пометка
// ухода: заявка открытого раунда удаляется, с ближайшей заморозки рассказчик выводит персонажа из истории. Ответ несёт
// кнопку «Вернуться» (`/back`).
func (s *Service) away(ctx context.Context, req *request) error {
	g, c, err := s.actor(ctx, req)
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(req.in.Args)) {
	case confirmYes:
	case confirmNo:
		return s.reply(ctx, req.in, "Уход отменён: персонаж остаётся в истории.")
	default:
		if c.Away {
			return round.ErrAlreadyAway
		}
		return s.replyKeyboard(ctx, req.in, fmt.Sprintf("Вывести %s из истории? С ближайшего раунда рассказчик опишет уход, дальше персонаж не упоминается; заявка на текущий раунд будет снята. Вернуться можно в любой момент: /back или любая заявка. Подтвердить: кнопкой или /away yes", c.Nickname), confirmKeyboard("away", "Уйти"))
	}
	cancelled, err := s.rounds.Away(ctx, g, c)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("%s уходит из истории: рассказчик опишет уход в ближайшем раунде, дальше персонаж не упоминается и личные сообщения раундов не приходят.", c.Nickname)
	if cancelled {
		text = "Заявка на текущий раунд снята. " + text
	}
	return s.replyKeyboard(ctx, req.in, text+" Вернуться: /back", model.Keyboard{{{Text: "Вернуться", Data: model.CallbackCommand("back", "")}}})
}

// back — /back в личке (ADR-0026): снимает пометку ухода; выведенному персонажу при открытом раунде ставит заявку
// ожидания (её заменит `/act` того же раунда, B51), возвращение объясняет рассказчик; без открытого раунда вернёт
// первая заявка.
func (s *Service) back(ctx context.Context, req *request) error {
	g, c, err := s.actor(ctx, req)
	if err != nil {
		return err
	}
	r, err := s.rounds.Back(ctx, g, c)
	switch {
	case err == nil && r != nil:
		return s.reply(ctx, req.in, fmt.Sprintf("%s возвращается: заявка «%s» принята на раунд %d, до %s. Рассказчик объяснит возвращение; хотите действовать в этом раунде — подайте заявку /act, она заменит «%s».", c.Nickname, round.BackSubmission, r.RoundNo, round.FormatTime(g, r.DeadlineAt, s.clock.Now()), round.BackSubmission))
	case err == nil:
		return s.reply(ctx, req.in, fmt.Sprintf("Уход отменён: %s остаётся в истории.", c.Nickname))
	case errors.Is(err, round.ErrNoOpenRound):
		return s.reply(ctx, req.in, fmt.Sprintf("Пометка ухода снята, но сейчас нет открытого раунда: %s вернётся в историю первой заявкой (/act) в ближайшем раунде.", c.Nickname))
	}
	return err
}
