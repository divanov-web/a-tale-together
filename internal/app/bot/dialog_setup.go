package bot

import (
	"context"
	"fmt"
	"strings"

	"taletogether/internal/app/game"
	"taletogether/internal/model"
)

// setupSkip — ответ «оставить как есть» в диалоге настройки.
const setupSkip = "-"

// startGameSetup начинает диалог настройки игры в личке администратора.
func (s *Service) startGameSetup(ctx context.Context, user model.User, g model.Game) error {
	d := model.Dialog{UserID: user.ID, Kind: model.DialogGameSetup, Step: game.Params[0].Key, GameID: &g.ID}
	if err := s.dialogs.SaveDialog(ctx, d); err != nil {
		return err
	}
	q, err := s.setupQuestion(ctx, g, d.Step)
	if err != nil {
		return err
	}
	return s.replyPrivate(ctx, user, fmt.Sprintf("Настройка мира «%s». На каждый вопрос ответьте значением или «%s», чтобы оставить как есть. Прервать: /cancel\n\n%s", g.Title, setupSkip, q))
}

// setupStep применяет ответ к параметру и задаёт следующий вопрос.
func (s *Service) setupStep(ctx context.Context, req *request, d *model.Dialog, answer string) (string, bool, error) {
	g, err := s.dialogGame(ctx, d)
	if err != nil {
		return "", false, err
	}
	answer = strings.TrimSpace(answer)
	if answer != setupSkip {
		if g, err = s.games.Set(ctx, g, req.user.ID, d.Step, answer); err != nil {
			return "", false, err
		}
	}
	next := s.nextParam(ctx, d.Step)
	if next == "" {
		status, err := s.renderStatus(ctx, g)
		if err != nil {
			return "", false, err
		}
		return "Настройка завершена.\n\n" + status, true, nil
	}
	d.Step = next
	q, err := s.setupQuestion(ctx, g, next)
	return q, false, err
}

// nextParam — следующий параметр диалога; продвинутые параметры и жанр при единственном в справочнике пропускаются.
func (s *Service) nextParam(ctx context.Context, current string) string {
	found := false
	for _, p := range game.Params {
		if found {
			if p.Advanced {
				continue
			}
			if p.Key == "genre" {
				if genres, err := s.games.Genres(ctx); err == nil && len(genres) <= 1 {
					continue
				}
			}
			return p.Key
		}
		found = p.Key == current
	}
	return ""
}

// setupQuestion — вопрос по параметру с текущим значением.
func (s *Service) setupQuestion(ctx context.Context, g model.Game, key string) (string, error) {
	var p game.Param
	for _, it := range game.Params {
		if it.Key == key {
			p = it
		}
	}
	current, err := s.paramValue(ctx, g, key)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s (%s).\nСейчас: %s", p.Title, p.Hint, current), nil
}

// paramValue — текущее значение параметра для вопроса.
func (s *Service) paramValue(ctx context.Context, g model.Game, key string) (string, error) {
	switch key {
	case "title":
		return g.Title, nil
	case "genre":
		genres, err := s.games.Genres(ctx)
		if err != nil {
			return "", err
		}
		var list []string
		cur := ""
		for _, ge := range genres {
			list = append(list, ge.Code+" — "+ge.Title)
			if ge.ID == g.GenreID {
				cur = ge.Code
			}
		}
		return cur + "\nДоступно: " + strings.Join(list, "; "), nil
	case "round_minutes":
		return fmt.Sprint(g.RoundMinutes), nil
	case "timezone":
		return g.Timezone, nil
	case "play_window":
		if g.PlayFrom == "" {
			return "нет", nil
		}
		return g.PlayFrom + "-" + g.PlayTo, nil
	case "ends_at":
		if g.Settings.EndsAt == nil {
			return "нет", nil
		}
		return g.Settings.EndsAt.Format("2006-01-02"), nil
	case "max_rounds":
		return fmt.Sprint(g.Settings.MaxRounds), nil
	case "min_answers":
		return fmt.Sprint(g.Settings.MinRoundAnswers), nil
	}
	if strings.HasPrefix(key, "faction_") {
		factions, err := s.games.Factions(ctx, g.ID)
		if err != nil {
			return "", err
		}
		for _, f := range factions {
			if string(f.Code) == strings.TrimPrefix(key, "faction_") {
				return f.Title, nil
			}
		}
	}
	return "", nil
}
