package bot

import (
	"context"
	"fmt"
	"strings"

	"taletogether/internal/app/round"
	"taletogether/internal/model"
)

// gameBegin — /begin в группе: запуск кампании, стартовая сцена и первый раунд уходят через outbox.
func (s *Service) gameBegin(ctx context.Context, req *request) error {
	g, err := s.games.OfChat(ctx, req.chat.ID)
	if err != nil {
		return err
	}
	g, err = s.rounds.Begin(ctx, g, req.user.ID)
	if err != nil {
		return err
	}
	return s.reply(ctx, req.in, fmt.Sprintf("Игра «%s» запущена.", g.Title))
}

// gamePause — /pause в группе: пауза или возобновление.
func (s *Service) gamePause(ctx context.Context, req *request) error {
	g, err := s.games.OfChat(ctx, req.chat.ID)
	if err != nil {
		return err
	}
	g, paused, err := s.rounds.Pause(ctx, g, req.user.ID)
	if err != nil {
		return err
	}
	if paused {
		return s.reply(ctx, req.in, fmt.Sprintf("Игра «%s» на паузе. Возобновить: /pause", g.Title))
	}
	return s.reply(ctx, req.in, fmt.Sprintf("Игра «%s» возобновлена.", g.Title))
}

// roundNext — /next в группе (только dev): закрыть текущий раунд и открыть следующий.
func (s *Service) roundNext(ctx context.Context, req *request) error {
	g, err := s.games.OfChat(ctx, req.chat.ID)
	if err != nil {
		return err
	}
	if _, err := s.rounds.Next(ctx, g, req.user.ID); err != nil {
		return err
	}
	return s.reply(ctx, req.in, "Раунд закрыт, следующий открыт.")
}

// act — /act <текст> в личке: заявка персонажа на открытый раунд текущей игры.
func (s *Service) act(ctx context.Context, req *request) error {
	text := strings.TrimSpace(req.in.Args)
	if text == "" {
		return s.reply(ctx, req.in, fmt.Sprintf("Формат: /act <что делает персонаж> (до %d символов). Последняя заявка до дедлайна действует.", model.SubmissionMaxLen))
	}
	g, err := s.games.Current(ctx, req.user)
	if err != nil {
		return err
	}
	c, err := s.games.Character(ctx, g, req.user.ID)
	if err != nil {
		return err
	}
	r, _, err := s.rounds.Submit(ctx, g, c, text)
	if err != nil {
		return err
	}
	return s.reply(ctx, req.in, fmt.Sprintf("Заявка принята: раунд %d, до %s. Заменить: /act <новый текст>.", r.RoundNo, round.FormatTime(g, r.DeadlineAt, s.clock.Now())))
}

// roundLines — строки /status о раундах: текущий, предыдущий, следующий или причина простоя.
func (s *Service) roundLines(ctx context.Context, g model.Game) (current, last, next string, err error) {
	now := s.clock.Now()
	switch g.Status {
	case model.GameDraft:
		return "", "", "", nil
	case model.GamePaused:
		next = "игра на паузе; возобновить: /pause в группе"
	case model.GameFinished:
		next = "игра завершена"
	}
	info, err := s.rounds.Info(ctx, g)
	if err != nil {
		return "", "", "", err
	}
	if info.Open != nil {
		current = fmt.Sprintf("%d, заявки до %s; заявок %d из %d", info.Open.RoundNo, round.FormatTime(g, info.Open.DeadlineAt, now), info.Submissions, g.Settings.MinRoundAnswers)
		if g.Status == model.GamePaused {
			current += " (пауза)"
		}
	}
	if info.Last != nil {
		switch info.Last.Status {
		case model.RoundSkipped:
			last = fmt.Sprintf("%d пропущен: %s", info.Last.RoundNo, info.Last.SkipReason)
		case model.RoundFrozen:
			n := 0
			if info.Last.Snapshot != nil {
				n = len(info.Last.Snapshot.Submissions)
			}
			last = fmt.Sprintf("%d закрыт, заявок %d", info.Last.RoundNo, n)
		default:
			last = fmt.Sprintf("%d — %s", info.Last.RoundNo, info.Last.Status)
		}
	}
	if g.Status == model.GameActive {
		switch {
		case info.Open != nil:
			next = "сразу после текущего, в " + round.FormatTime(g, info.Open.DeadlineAt, now)
		case g.NextRoundAt != nil:
			next = fmt.Sprintf("в %s (игровое окно %s–%s)", round.FormatTime(g, *g.NextRoundAt, now), g.PlayFrom, g.PlayTo)
		default:
			next = "по расписанию"
		}
	}
	return current, last, next, nil
}
