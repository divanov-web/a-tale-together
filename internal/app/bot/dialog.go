package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"taletogether/internal/model"
)

// handleDialogAnswer передаёт текст из лички активному диалогу пользователя.
func (s *Service) handleDialogAnswer(ctx context.Context, req *request) error {
	d, err := s.dialogs.GetDialog(ctx, req.user.ID)
	if errors.Is(err, model.ErrNotFound) {
		return s.reply(ctx, req.in, "Я понимаю только команды. Список: /help")
	}
	if err != nil {
		return err
	}
	var reply string
	var done bool
	switch d.Kind {
	case model.DialogGameSetup:
		reply, done, err = s.setupStep(ctx, req, &d, req.in.Text)
	case model.DialogCharacterCreate:
		reply, done, err = s.characterStep(ctx, req, &d, req.in.Text)
	default:
		err = fmt.Errorf("bot.handleDialogAnswer: unknown dialog kind %q", d.Kind)
	}
	if err != nil {
		if text, ok := userError(err); ok {
			return s.reply(ctx, req.in, text+"\nПовторите ответ или /cancel.")
		}
		return s.fail(ctx, req, err)
	}
	if done {
		if err := s.dialogs.DeleteDialog(ctx, req.user.ID); err != nil {
			return err
		}
	} else if err := s.dialogs.SaveDialog(ctx, d); err != nil {
		return err
	}
	return s.reply(ctx, req.in, reply)
}

// saveData сериализует данные диалога.
func saveData(d *model.Dialog, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("bot.saveData: %w", err)
	}
	d.Data = b
	return nil
}

// loadData разбирает данные диалога.
func loadData(d *model.Dialog, v any) error {
	if len(d.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(d.Data, v); err != nil {
		return fmt.Errorf("bot.loadData: %w", err)
	}
	return nil
}

// dialogGame возвращает игру диалога.
func (s *Service) dialogGame(ctx context.Context, d *model.Dialog) (model.Game, error) {
	if d.GameID == nil {
		return model.Game{}, fmt.Errorf("bot.dialogGame: dialog %s without game", d.Kind)
	}
	return s.games.Get(ctx, *d.GameID)
}
