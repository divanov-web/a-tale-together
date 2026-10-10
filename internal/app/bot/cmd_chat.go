package bot

import (
	"context"
	"errors"
	"strings"

	"taletogether/internal/app/bot/templates"
	"taletogether/internal/app/chat"
	"taletogether/internal/app/game"
	"taletogether/internal/model"
)

// chatInfo — /chat: владелец, модераторы, статус бота, текущая игра.
func (s *Service) chatInfo(ctx context.Context, req *request) error {
	info, err := s.chats.Info(ctx, req.chat)
	if err != nil {
		return err
	}
	mods := make([]string, 0, len(info.Moderators))
	for _, m := range info.Moderators {
		mods = append(mods, m.Mention())
	}
	data := map[string]any{"Title": req.chat.Title, "Owner": info.Owner.Mention(), "Moderators": mods, "BotStatus": string(req.chat.BotStatus)}
	if g, err := s.games.OfChat(ctx, req.chat.ID); err == nil {
		data["Game"] = map[string]string{"Title": g.Title, "Status": statusTitle(g.Status)}
	} else if !errors.Is(err, game.ErrNoGame) {
		return err
	}
	text, err := templates.Render("chat", data)
	if err != nil {
		return err
	}
	return s.reply(ctx, req.in, text)
}

// chatMod — /chat_mod add|remove @user.
func (s *Service) chatMod(ctx context.Context, req *request) error {
	if req.chat.OwnerUserID != req.user.ID {
		return chat.ErrForbidden
	}
	action, rest := splitArgs(req.in.Args)
	target, err := s.resolveUser(ctx, rest)
	if err != nil {
		return err
	}
	if target.ID == 0 {
		return s.reply(ctx, req.in, "Формат: /chat_mod add @user или /chat_mod remove @user")
	}
	switch strings.ToLower(action) {
	case "add":
		if err := s.chats.AddModerator(ctx, req.chat, req.user.ID, target); err != nil {
			return err
		}
		return s.reply(ctx, req.in, target.Mention()+" назначен модератором чата.")
	case "remove":
		if err := s.chats.RemoveModerator(ctx, req.chat, req.user.ID, target); err != nil {
			return err
		}
		return s.reply(ctx, req.in, target.Mention()+" больше не модератор чата.")
	}
	return s.reply(ctx, req.in, "Формат: /chat_mod add @user или /chat_mod remove @user")
}

// chatOwner — /chat_owner @user.
func (s *Service) chatOwner(ctx context.Context, req *request) error {
	if req.chat.OwnerUserID != req.user.ID {
		return chat.ErrForbidden
	}
	target, err := s.resolveUser(ctx, req.in.Args)
	if err != nil {
		return err
	}
	if target.ID == 0 {
		return s.reply(ctx, req.in, "Формат: /chat_owner @user")
	}
	if err := s.chats.SetOwner(ctx, req.chat, req.user.ID, target); err != nil {
		return err
	}
	return s.reply(ctx, req.in, "Владелец чата теперь "+target.Mention()+".")
}

// resolveUser находит пользователя по @username среди известных боту; пустой аргумент — нулевой пользователь.
func (s *Service) resolveUser(ctx context.Context, arg string) (model.User, error) {
	name := strings.TrimPrefix(strings.TrimSpace(arg), "@")
	if name == "" {
		return model.User{}, nil
	}
	u, err := s.store.GetUserByUsername(ctx, name)
	if errors.Is(err, model.ErrNotFound) {
		return model.User{}, errUnknownUser
	}
	return u, err
}

// errUnknownUser — @username не известен боту (пользователь ещё не писал боту и не отправлял команд в группе).
var errUnknownUser = errors.New("пользователь неизвестен боту: пусть отправит /start боту в личку или любую команду в группе")
