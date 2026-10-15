package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"taletogether/internal/app/game"
	"taletogether/internal/app/round"
	"taletogether/internal/model"
)

// setupSkip — ответ «оставить как есть» в диалоге настройки.
const setupSkip = "-"

// setupPostponedText — ответ на /cancel в настройке мира (ADR-0023): игра остаётся в `setup`.
const setupPostponedText = "Настройка отложена: мир остаётся в настройке, игроки пока не могут создать персонажей. Продолжить: /set"

// startGameSetup начинает диалог настройки игры в личке администратора или возвращает к текущему шагу, если
// диалог настройки этой игры уже идёт (ADR-0023: `/start setup_<код>`, `/set` без аргументов, кнопка в `/status`).
func (s *Service) startGameSetup(ctx context.Context, user model.User, g model.Game) error {
	d, err := s.dialogs.GetDialog(ctx, user.ID)
	resume := err == nil && d.Kind == model.DialogGameSetup && d.GameID != nil && *d.GameID == g.ID
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return err
	}
	if !resume {
		d = model.Dialog{UserID: user.ID, Kind: model.DialogGameSetup, Step: s.nextParam(ctx, g, ""), GameID: &g.ID}
		if err := s.dialogs.SaveDialog(ctx, d); err != nil {
			return err
		}
	}
	q, err := s.setupQuestion(ctx, g, d.Step)
	if err != nil {
		return err
	}
	if resume {
		return s.replyPrivateFormatted(ctx, user, prefixed(fmt.Sprintf("Продолжаем настройку мира «%s».\n\n", g.Title), q.text), q.kb)
	}
	return s.replyPrivateFormatted(ctx, user, prefixed(fmt.Sprintf("Настройка мира «%s». На каждый вопрос ответьте значением, кнопкой или «%s», чтобы оставить как есть.\n\n", g.Title, setupSkip), q.text), q.kb)
}

// setupStep применяет ответ к параметру и задаёт следующий вопрос; после последнего мир переходит из `setup`
// в `draft` (`game.FinishSetup`, ADR-0023), итог — сводка игры.
func (s *Service) setupStep(ctx context.Context, req *request, d *model.Dialog, answer string) (question, bool, error) {
	g, err := s.dialogGame(ctx, d)
	if err != nil {
		return question{}, false, err
	}
	answer = strings.TrimSpace(answer)
	if d.Step == game.ScenarioParam {
		// Стартовый мир выбирается обязательно (ADR-0028): «-» не пропускает шаг; шаг отпадает сам, только если
		// варианты не придуманы после всех попыток — тогда мир по шаблону жанра.
		switch {
		case !s.scenarioStep(g):
			answer = setupSkip
		case answer == setupSkip:
			return question{}, false, fmt.Errorf("%w: выберите один из вариантов", game.ErrBadValue)
		}
	}
	if answer != setupSkip {
		if g, err = s.games.Set(ctx, g, req.user.ID, d.Step, answer); err != nil {
			return question{}, false, err
		}
	}
	next := s.nextParam(ctx, g, d.Step)
	if next == "" {
		if g, err = s.games.FinishSetup(ctx, g, req.user.ID); err != nil {
			return question{}, false, err
		}
		status, err := s.renderStatus(ctx, g)
		if err != nil {
			return question{}, false, err
		}
		return question{text: prefixed("Настройка завершена. Игроки могут создавать персонажей.\n\n", status)}, true, nil
	}
	d.Step = next
	q, err := s.setupQuestion(ctx, g, next)
	return q, false, err
}

// nextParam — следующий параметр диалога после current (пусто — первый); продвинутые параметры, жанр при
// единственном в справочнике и стартовый мир без модели или после неудачной генерации (ADR-0028) пропускаются.
func (s *Service) nextParam(ctx context.Context, g model.Game, current string) string {
	found := current == ""
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
			if p.Key == game.ScenarioParam && !s.scenarioStep(g) {
				continue
			}
			return p.Key
		}
		found = found || p.Key == current
	}
	return ""
}

// scenarioStep — показывать ли шаг «Стартовый мир»: модель подключена и варианты либо уже есть, либо ещё ожидаются.
func (s *Service) scenarioStep(g model.Game) bool {
	if !s.rounds.WorldgenEnabled() {
		return false
	}
	return g.WorldSetups.Pending() || len(g.WorldSetups.List()) > 0
}

// setupQuestion — вопрос по параметру с текущим значением («Сейчас» жирным, B40) и кнопками типовых значений
// (ADR-0015); на каждом шаге кнопка «Оставить как есть».
func (s *Service) setupQuestion(ctx context.Context, g model.Game, key string) (question, error) {
	var p game.Param
	for _, it := range game.Params {
		if it.Key == key {
			p = it
		}
	}
	if key == game.ScenarioParam {
		return s.scenarioQuestion(g), nil
	}
	current, err := s.paramValue(ctx, g, key)
	if err != nil {
		return question{}, err
	}
	answers, perRow, err := s.setupAnswers(ctx, key)
	if err != nil {
		return question{}, err
	}
	answers = append(answers, [2]string{"Оставить как есть", setupSkip})
	var b model.TextBuilder
	b.Printf("%s (%s).\n", p.Title, p.Hint)
	b.Key("Сейчас")
	b.WriteString(current)
	return question{text: b.Build(), kb: answerButtons(key, perRow, answers...)}, nil
}

// scenarioQuestion — шаг «Стартовый мир» (ADR-0028): варианты с кнопками, когда они уже получены; иначе ожидание
// без кнопок — варианты придут отдельным сообщением с кнопками, и выбрать один из них обязательно.
func (s *Service) scenarioQuestion(g model.Game) question {
	if len(g.WorldSetups.List()) > 0 {
		var b model.TextBuilder
		b.WriteString(round.SetupOptionsText(g))
		if cur := g.Setup(); cur != nil {
			b.WriteString("\n")
			b.Key("Сейчас")
			b.WriteString(cur.Title)
		}
		return question{text: b.Build(), kb: round.SetupOptionsKeyboard(g)}
	}
	return ask("Стартовый мир. Придумываю четыре варианта, это займёт минуту-две: они придут сюда отдельным сообщением с кнопками. "+
		"Дождитесь его и выберите один из вариантов — без этого настройка не продолжится.", nil)
}

// setupTimezones — часовые пояса на кнопках диалога настройки; остальные вводятся текстом.
var setupTimezones = []string{"Europe/Kaliningrad", "Europe/Moscow", "Europe/Samara", "Asia/Yekaterinburg", "Asia/Novosibirsk", "Asia/Vladivostok"}

// setupAnswers — типовые ответы параметра для кнопок и число кнопок в ряду; свободные параметры без ответов.
func (s *Service) setupAnswers(ctx context.Context, key string) ([][2]string, int, error) {
	nums := func(vals ...string) [][2]string {
		out := make([][2]string, 0, len(vals))
		for _, v := range vals {
			out = append(out, [2]string{v, v})
		}
		return out
	}
	switch key {
	case "genre":
		genres, err := s.games.Genres(ctx)
		if err != nil {
			return nil, 0, err
		}
		var out [][2]string
		for _, ge := range genres {
			out = append(out, [2]string{ge.Title, ge.Code})
		}
		return out, 1, nil
	case "round_minutes":
		return nums("10", "20", "30", "60"), 4, nil
	case "timezone":
		return nums(setupTimezones...), 2, nil
	case "play_window":
		return [][2]string{{"Без окна", "нет"}}, 1, nil
	case "min_answers":
		return nums("1", "2", "3", "5"), 4, nil
	}
	return nil, 1, nil
}

// paramValue — текущее значение параметра для вопроса.
func (s *Service) paramValue(ctx context.Context, g model.Game, key string) (string, error) {
	switch key {
	case game.ScenarioParam:
		if cur := g.Setup(); cur != nil {
			return cur.Title, nil
		}
		return "не выбран", nil
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
