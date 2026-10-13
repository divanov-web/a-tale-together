package round

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"taletogether/internal/model"
	"taletogether/internal/service/rules"
)

// Finish принимает завершение игры по команде администратора (`/finish`, ADR-0024): короткая транзакция — статус
// `finishing`, `next_round_at = now`; открытый раунд получает дедлайн «сейчас». Само завершение (`finishNow`:
// разрешение последнего раунда, финальная хроника, статус `finished`) делает ближайший тик планировщика.
func (s *Service) Finish(ctx context.Context, game model.Game, actor model.UserID) (model.Game, error) {
	if err := s.canAdmin(ctx, game, actor); err != nil {
		return model.Game{}, err
	}
	now := s.clock.Now()
	var out model.Game
	err := s.store.InTx(ctx, func(ctx context.Context) error {
		g, err := s.store.LockGame(ctx, game.ID)
		if err != nil {
			return err
		}
		switch g.Status {
		case model.GameActive, model.GamePaused:
		default:
			return statusError(g.Status)
		}
		open, err := s.store.OpenRound(ctx, g.ID)
		if err == nil {
			open.DeadlineAt = now
			if err := s.store.UpdateRound(ctx, open); err != nil {
				return err
			}
		} else if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		g.Status = model.GameFinishing
		g.NextRoundAt = &now
		if err := s.store.UpdateGame(ctx, g); err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return model.Game{}, wrap("round.Finish", err)
	}
	s.logger.Info("game finishing", slog.Int64("game_id", int64(out.ID)))
	return out, nil
}

// finishNow завершает игру из `finishing` в транзакции тика (ADR-0024): открытый раунд закрывается и разрешается, если
// есть хотя бы одна заявка (кворум не применяется: последний шанс), затем финальная хроника и статус `finished`.
// Планировщик завершённую игру не берёт. Границы рассказа (`max_rounds`, `ends_at`) ведут в `finish` из `afterClose`.
func (s *Service) finishNow(ctx context.Context, g *model.Game, now time.Time) error {
	chat, err := s.store.GetChat(ctx, g.ChatID)
	if err != nil {
		return err
	}
	closed := 0
	open, err := s.store.OpenRound(ctx, g.ID)
	switch {
	case err == nil:
		if open.DeadlineAt.After(now) {
			open.DeadlineAt = now
			if err := s.store.UpdateRound(ctx, open); err != nil {
				return err
			}
		}
		if _, err := s.close(ctx, *g, chat, open, now, 1); err != nil {
			return err
		}
		closed = open.RoundNo
	case errors.Is(err, model.ErrNotFound):
		last, err := s.store.LastRound(ctx, g.ID)
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return err
		}
		closed = last.RoundNo
	default:
		return err
	}
	return s.finish(ctx, g, chat, s.clock.Now(), closed, "игру завершил администратор")
}

// finish переводит игру в `finished`, снимает таймер планировщика и ставит в outbox финальную хронику:
// причина, число раундов, итог каждого персонажа (шаблон; эпилог рассказчика — вопрос G13).
func (s *Service) finish(ctx context.Context, g *model.Game, chat model.Chat, now time.Time, rounds int, reason string) error {
	g.Status = model.GameFinished
	g.NextRoundAt = nil
	if err := s.store.UpdateGame(ctx, *g); err != nil {
		return err
	}
	season, err := s.store.ActiveSeason(ctx, g.ID)
	if err != nil {
		return err
	}
	chars, err := s.store.ListCharacters(ctx, g.ID, season.ID)
	if err != nil {
		return err
	}
	locations, err := s.store.ListLocations(ctx, g.ID)
	if err != nil {
		return err
	}
	def, err := s.characterDef(ctx, *g)
	if err != nil {
		return err
	}
	s.logger.Info("game finished", slog.Int64("game_id", int64(g.ID)), slog.Int("rounds", rounds), slog.String("reason", reason))
	s.journal(ctx, *g, chat.TelegramChatID, model.Round{RoundNo: rounds}, "finished", map[string]any{"reason": reason, "rounds": rounds})
	return s.enqueue(ctx, *g, chat, nil, model.OutboxSystem, finishedText(*g, rounds, reason, chars, locations, def))
}

// characterDef — модель персонажа жанра игры.
func (s *Service) characterDef(ctx context.Context, g model.Game) (model.CharacterModelDef, error) {
	genre, err := s.store.GetGenre(ctx, g.GenreID)
	if err != nil {
		return model.CharacterModelDef{}, err
	}
	cm, err := s.store.GetCharacterModel(ctx, genre.CharacterModelID)
	if err != nil {
		return model.CharacterModelDef{}, err
	}
	return cm.Definition, nil
}

// Reset — сброс игры для повторных прогонов в песочнице (dev): ход игры удаляется (раунды, заявки, события, итоги,
// outbox, сохранённые ответы модели), мир сворачивается до стартовой клетки шаблона жанра (остальные клетки,
// открытия и счётчики исследования удаляются; пункт 27), персонажи возвращаются к стартовому состоянию (HP по
// формуле, стартовая клетка, без состояний и знаний, `active`), игра — в `draft`; участники и персонажи остаются.
// `llm_runs` не трогаются.
func (s *Service) Reset(ctx context.Context, game model.Game, actor model.UserID) (model.Game, error) {
	if err := s.canAdmin(ctx, game, actor); err != nil {
		return model.Game{}, err
	}
	var out model.Game
	err := s.store.InTx(ctx, func(ctx context.Context) error {
		g, err := s.store.LockGame(ctx, game.ID)
		if err != nil {
			return err
		}
		chat, err := s.store.GetChat(ctx, g.ChatID)
		if err != nil {
			return err
		}
		if err := s.store.DeleteGameProgress(ctx, g.ID); err != nil {
			return err
		}
		genre, err := s.store.GetGenre(ctx, g.GenreID)
		if err != nil {
			return err
		}
		cm, err := s.store.GetCharacterModel(ctx, genre.CharacterModelID)
		if err != nil {
			return err
		}
		if err := s.store.ResetWorld(ctx, g.ID); err != nil {
			return err
		}
		locations, err := s.store.ListLocations(ctx, g.ID)
		if err != nil {
			return err
		}
		var start *model.LocationID
		for _, l := range locations {
			if !l.Start && start != nil {
				continue
			}
			id := l.ID
			start = &id
			for _, lt := range genre.Template.Locations {
				if !lt.Start && len(genre.Template.Locations) > 1 {
					continue
				}
				l.Name, l.Description, l.Difficulty, l.Terrain, l.Stage, l.VisitedAt = lt.Name, lt.Description, lt.Difficulty, lt.Terrain, model.StageDetailed, nil
				if err := s.store.UpdateLocation(ctx, l); err != nil {
					return err
				}
				if err := s.store.ReplaceObjects(ctx, g.ID, l.ID, lt.Objects); err != nil {
					return err
				}
				break
			}
		}
		season, err := s.store.ActiveSeason(ctx, g.ID)
		if err != nil {
			return err
		}
		chars, err := s.store.ListCharacters(ctx, g.ID, season.ID)
		if err != nil {
			return err
		}
		for _, c := range chars {
			if hp, err := rules.HP(cm.Definition, c.Attributes); err == nil && hp > 0 {
				c.HP = hp
			}
			c.LocationID, c.Conditions, c.Knowledge = start, []string{}, []string{}
			c.Status, c.MissedRounds, c.DeathRoundsLeft, c.DiedAt = model.CharacterActive, 0, 0, nil
			if err := s.store.SaveCharacterState(ctx, g.ID, c); err != nil {
				return err
			}
		}
		g.Status = model.GameDraft
		g.NextRoundAt = nil
		g.SkippedStreak = 0
		if err := s.store.UpdateGame(ctx, g); err != nil {
			return err
		}
		s.logger.Info("game reset", slog.Int64("game_id", int64(g.ID)), slog.Int("characters", len(chars)))
		s.journal(ctx, g, chat.TelegramChatID, model.Round{}, "reset", map[string]any{"characters": len(chars)})
		out = g
		return nil
	})
	if err != nil {
		return model.Game{}, wrap("round.Reset", err)
	}
	return out, nil
}

// Detail — карточка раунда для dev-команды `/round`: раунд, заявки, итоги, число событий, имена персонажей.
type Detail struct {
	Round       model.Round
	Submissions []model.Submission
	Results     []model.ActionResult
	Events      int
	Names       map[model.CharacterID]string
}

// Detail собирает карточку раунда по номеру; 0 — последний раунд.
func (s *Service) Detail(ctx context.Context, game model.Game, no int) (Detail, error) {
	var r model.Round
	var err error
	if no <= 0 {
		r, err = s.store.LastRound(ctx, game.ID)
	} else {
		r, err = s.store.RoundByNo(ctx, game.ID, no)
	}
	if errors.Is(err, model.ErrNotFound) {
		return Detail{}, ErrNoRound
	}
	if err != nil {
		return Detail{}, fmt.Errorf("round.Detail: %w", err)
	}
	d := Detail{Round: r, Names: map[model.CharacterID]string{}}
	if d.Submissions, err = s.store.ListSubmissions(ctx, game.ID, r.ID); err != nil {
		return Detail{}, fmt.Errorf("round.Detail: %w", err)
	}
	if d.Results, err = s.store.ListResolvedActions(ctx, game.ID, r.ID); err != nil {
		return Detail{}, fmt.Errorf("round.Detail: %w", err)
	}
	events, err := s.store.ListEvents(ctx, game.ID, r.ID)
	if err != nil {
		return Detail{}, fmt.Errorf("round.Detail: %w", err)
	}
	d.Events = len(events)
	chars, err := s.store.ListCharacters(ctx, game.ID, r.SeasonID)
	if err != nil {
		return Detail{}, fmt.Errorf("round.Detail: %w", err)
	}
	for _, c := range chars {
		d.Names[c.ID] = c.Nickname
	}
	return d, nil
}
