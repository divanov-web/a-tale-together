package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"taletogether/internal/model"
)

// errNotOwner — команда доступна только владельцу бота (BOT_OWNER_ID).
var errNotOwner = errors.New("команда доступна только владельцу бота")

// llmCommand — /llm [use <профиль> [game_id] | reset <game_id> | add <имя> <провайдер> <модель_интерпретатора> <модель_рассказчика> [effort] | test [профиль]].
func (s *Service) llmCommand(ctx context.Context, req *request) error {
	if s.settings.OwnerID == 0 || req.user.TelegramID != s.settings.OwnerID {
		return errNotOwner
	}
	sub, rest := splitArgs(req.in.Args)
	switch strings.ToLower(sub) {
	case "":
		return s.llmList(ctx, req)
	case "use":
		return s.llmUse(ctx, req, rest)
	case "reset":
		return s.llmReset(ctx, req, rest)
	case "add":
		return s.llmAdd(ctx, req, rest)
	case "test":
		return s.llmTest(ctx, req, rest)
	}
	return s.reply(ctx, req.in, "Формат: /llm | /llm use <профиль> [game_id] | /llm reset <game_id> | /llm add <имя> <провайдер> <модель_интерпретатора> <модель_рассказчика> [effort] | /llm test [профиль]")
}

func (s *Service) llmList(ctx context.Context, req *request) error {
	ov, err := s.llm.List(ctx)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("Профили LLM:\n")
	if len(ov.Profiles) == 0 {
		b.WriteString("• нет; добавить: /llm add …\n")
	}
	for _, p := range ov.Profiles {
		mark := ""
		if p.Name == ov.DefaultName {
			mark = " — по умолчанию"
		}
		if !p.Enabled {
			mark += " (выключен)"
		}
		fmt.Fprintf(&b, "• %s: %s; интерпретатор %s, рассказчик %s%s\n", p.Name, p.Provider, p.ModelInterpreter, p.ModelNarrator, mark)
	}
	fmt.Fprintf(&b, "Провайдеры: %s\n", strings.Join(ov.Providers, ", "))
	if g, err := s.games.Current(ctx, req.user); err == nil {
		if g.LLMProfileID != nil {
			for _, p := range ov.Profiles {
				if p.ID == *g.LLMProfileID {
					fmt.Fprintf(&b, "Текущая игра «%s» (id %d): профиль %s\n", g.Title, g.ID, p.Name)
				}
			}
		} else {
			fmt.Fprintf(&b, "Текущая игра «%s» (id %d): профиль по умолчанию\n", g.Title, g.ID)
		}
	}
	b.WriteString("Проверить: /llm test [профиль]")
	return s.reply(ctx, req.in, b.String())
}

func (s *Service) llmUse(ctx context.Context, req *request, args string) error {
	name, gameArg := splitArgs(args)
	if name == "" {
		return s.reply(ctx, req.in, "Формат: /llm use <профиль> [game_id]")
	}
	var game *model.GameID
	if gameArg != "" {
		id, err := strconv.ParseInt(gameArg, 10, 64)
		if err != nil || id <= 0 {
			return s.reply(ctx, req.in, "game_id — число из /status или /llm")
		}
		gid := model.GameID(id)
		game = &gid
	}
	if err := s.llm.Use(ctx, name, game); err != nil {
		return err
	}
	if game != nil {
		return s.reply(ctx, req.in, fmt.Sprintf("Профиль %s назначен игре %d. Вернуть к глобальному: /llm reset %d", name, *game, *game))
	}
	return s.reply(ctx, req.in, "Профиль по умолчанию: "+name)
}

func (s *Service) llmReset(ctx context.Context, req *request, args string) error {
	gameArg, _ := splitArgs(args)
	id, err := strconv.ParseInt(gameArg, 10, 64)
	if err != nil || id <= 0 {
		return s.reply(ctx, req.in, "Формат: /llm reset <game_id>")
	}
	if err := s.llm.Reset(ctx, model.GameID(id)); err != nil {
		return err
	}
	return s.reply(ctx, req.in, fmt.Sprintf("Игра %d использует профиль по умолчанию.", id))
}

func (s *Service) llmAdd(ctx context.Context, req *request, args string) error {
	parts := strings.Fields(args)
	if len(parts) < 4 {
		return s.reply(ctx, req.in, "Формат: /llm add <имя> <провайдер> <модель_интерпретатора> <модель_рассказчика> [effort]\nПровайдеры: "+strings.Join(s.llm.Providers(), ", "))
	}
	p := model.LLMProfile{Name: parts[0], Provider: parts[1], ModelInterpreter: parts[2], ModelNarrator: parts[3],
		Params: model.LLMParams{Effort: "low", MaxBudgetUSD: 0.5, TimeoutSeconds: 120}}
	if len(parts) > 4 {
		p.Params.Effort = parts[4]
	}
	created, err := s.llm.Add(ctx, p)
	if err != nil {
		return err
	}
	return s.reply(ctx, req.in, fmt.Sprintf("Профиль %s создан (%s). Сделать основным: /llm use %s; проверить: /llm test %s", created.Name, created.Provider, created.Name, created.Name))
}

func (s *Service) llmTest(ctx context.Context, req *request, args string) error {
	name, _ := splitArgs(args)
	if err := s.reply(ctx, req.in, "Проверяю профиль, это займёт несколько секунд…"); err != nil {
		return err
	}
	p, resp, err := s.llm.Test(ctx, name)
	if err != nil {
		if text, ok := userError(err); ok {
			return s.reply(ctx, req.in, text)
		}
		return s.reply(ctx, req.in, fmt.Sprintf("Профиль %s: ошибка вызова — %s", p.Name, err.Error()))
	}
	payment := "токены API"
	if resp.Subscription {
		payment = "подписка"
	}
	u := resp.Usage
	return s.reply(ctx, req.in, fmt.Sprintf("Профиль %s (%s): ок\nМодель: %s; токены вход %d (кэш: запись %d, чтение %d), выход %d; цена по таблице $%.4f, оценка провайдера $%.4f (%s); %d мс\nОтвет по схеме: %s",
		p.Name, p.Provider, resp.Model, u.TotalIn(), u.CacheWrite, u.CacheRead, u.Output, resp.CostUSD, resp.ProviderCostUSD, payment, resp.Duration.Milliseconds(), string(resp.JSON)))
}
