package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"taletogether/internal/app/bot/templates"
	"taletogether/internal/app/game"
	"taletogether/internal/model"
)

// ping отвечает версией и временем сервера: проверка, что бот жив, в группе и в личке.
func (s *Service) ping(ctx context.Context, req *request) error {
	return s.reply(ctx, req.in, fmt.Sprintf("pong\nверсия: %s (%s)\nвремя: %s",
		s.settings.Version, s.settings.Env, s.clock.Now().Format(time.RFC3339)))
}

// start открывает личку (users.started_at) и разбирает deep link: join_<код> — вход в игру, setup_<код> — настройка.
func (s *Service) start(ctx context.Context, req *request) error {
	if err := s.store.MarkStarted(ctx, req.user.ID); err != nil {
		return err
	}
	now := s.clock.Now()
	req.user.StartedAt = &now
	arg := strings.TrimSpace(req.in.Args)
	switch {
	case strings.HasPrefix(arg, "join_"):
		return s.joinGame(ctx, req, strings.TrimPrefix(arg, "join_"))
	case strings.HasPrefix(arg, "setup_"):
		g, err := s.games.ByCode(ctx, strings.TrimPrefix(arg, "setup_"))
		if err != nil {
			return err
		}
		ok, err := s.games.CanAdmin(ctx, g, req.user.ID)
		if err != nil {
			return err
		}
		if !ok {
			return game.ErrForbidden
		}
		return s.startGameSetup(ctx, req.user, g)
	}
	games, err := s.games.Of(ctx, req.user.ID)
	if err != nil {
		return err
	}
	text, err := templates.Render("start", map[string]any{"Name": req.user.FirstName, "HasGames": len(games) > 0})
	if err != nil {
		return err
	}
	return s.reply(ctx, req.in, text)
}

// help выводит команды текущего контекста; под сообщением кнопки-ссылки на разделы руководства игрока (ADR-0015),
// если задан корень руководства.
func (s *Service) help(ctx context.Context, req *request) error {
	c := ctxGroup
	if req.in.IsPrivate() {
		c = ctxPrivate
	}
	text, err := templates.Render("help", map[string]any{"Private": req.in.IsPrivate(), "Commands": s.Commands(c)})
	if err != nil {
		return err
	}
	return s.replyKeyboard(ctx, req.in, text, s.guideKeyboard())
}

// guideSections — разделы руководства игрока для кнопок /help: подпись и путь от корня руководства.
var guideSections = [][2]string{{"Руководство: команды", "commands.md"}, {"Персонаж", "character/README.md"}, {"Испытания", "checks/checks.md"}}

// guideKeyboard — кнопки-ссылки на руководство; nil без GuideURL.
func (s *Service) guideKeyboard() model.Keyboard {
	base := strings.TrimRight(s.settings.GuideURL, "/")
	if base == "" {
		return nil
	}
	var kb model.Keyboard
	for _, sec := range guideSections {
		kb = append(kb, []model.Button{{Text: sec[0], URL: base + "/" + sec[1]}})
	}
	return kb
}

// me показывает пользователя, чаты с правами и игры с персонажами.
func (s *Service) me(ctx context.Context, req *request) error {
	chats, err := s.store.ChatsOfUser(ctx, req.user.ID)
	if err != nil {
		return err
	}
	type chatRow struct{ Title, Role string }
	var chatRows []chatRow
	for _, c := range chats {
		role := "модератор"
		if c.OwnerUserID == req.user.ID {
			role = "владелец"
		}
		chatRows = append(chatRows, chatRow{Title: c.Title, Role: role})
	}
	games, err := s.games.Of(ctx, req.user.ID)
	if err != nil {
		return err
	}
	current, _ := s.games.Current(ctx, req.user)
	type gameRow struct {
		Title, Status, Role, Character string
		Current                        bool
	}
	var gameRows []gameRow
	for _, g := range games {
		row := gameRow{Title: g.Title, Status: statusTitle(g.Status), Role: "игрок", Current: g.ID == current.ID && current.ID != 0}
		if g.CreatedByUserID == req.user.ID {
			row.Role = "администратор"
		}
		if ch, err := s.games.Character(ctx, g, req.user.ID); err == nil {
			row.Character = ch.Nickname
		} else if !errors.Is(err, game.ErrNoCharacter) {
			return err
		}
		gameRows = append(gameRows, row)
	}
	text, err := templates.Render("me", map[string]any{
		"Mention": req.user.Mention(), "TelegramID": int64(req.user.TelegramID), "Started": req.user.StartedAt != nil,
		"Chats": chatRows, "Games": gameRows,
	})
	if err != nil {
		return err
	}
	return s.reply(ctx, req.in, text)
}

// cancel прерывает текущий диалог. Настройку мира он только откладывает (ADR-0023): удаляется курсор диалога, игра
// остаётся в `setup`, продолжить — /set.
func (s *Service) cancel(ctx context.Context, req *request) error {
	d, err := s.dialogs.GetDialog(ctx, req.user.ID)
	if errors.Is(err, model.ErrNotFound) {
		return s.reply(ctx, req.in, "Активного диалога нет.")
	} else if err != nil {
		return err
	}
	if err := s.dialogs.DeleteDialog(ctx, req.user.ID); err != nil {
		return err
	}
	if d.Kind == model.DialogGameSetup {
		return s.reply(ctx, req.in, setupPostponedText)
	}
	return s.reply(ctx, req.in, "Диалог прерван.")
}

// statusTitle — статус игры по-русски.
func statusTitle(st model.GameStatus) string {
	switch st {
	case model.GameSetup:
		return "настройка"
	case model.GameDraft:
		return "черновик"
	case model.GameActive:
		return "идёт"
	case model.GamePaused:
		return "пауза"
	case model.GameFinished:
		return "завершена"
	}
	return string(st)
}
