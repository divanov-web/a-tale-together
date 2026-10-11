package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"taletogether/internal/model"
)

// handleDialogAnswer передаёт текст из лички или ответ кнопкой активному диалогу пользователя. Кнопка несёт шаг
// (ADR-0015): нажатие на устаревшем вопросе не применяется, бот повторяет текущий вопрос; кнопка завершённого
// диалога не становится заявкой.
func (s *Service) handleDialogAnswer(ctx context.Context, req *request) error {
	step := ""
	if req.in.Callback != nil {
		step = req.in.Callback.Step
	}
	d, err := s.dialogs.GetDialog(ctx, req.user.ID)
	if errors.Is(err, model.ErrNotFound) {
		if step != "" {
			return s.reply(ctx, req.in, "Этот диалог уже завершён.")
		}
		return s.implicitAct(ctx, req)
	}
	if err != nil {
		return err
	}
	if step != "" && step != d.Step {
		q, err := s.dialogQuestion(ctx, &d)
		if err != nil {
			return err
		}
		return s.replyKeyboard(ctx, req.in, "Кнопка устарела: этот вопрос уже отвечен. Текущий вопрос:\n\n"+q.text, q.kb)
	}
	var reply question
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
		return err
	}
	if done {
		if err := s.dialogs.DeleteDialog(ctx, req.user.ID); err != nil {
			return err
		}
	} else if err := s.dialogs.SaveDialog(ctx, d); err != nil {
		return err
	}
	return s.replyKeyboard(ctx, req.in, reply.text, reply.kb)
}

// question — вопрос диалога: текст и кнопки с ответами шага (ADR-0015); свободные поля без кнопок, кроме «Прервать».
type question struct {
	text string
	kb   model.Keyboard
}

// dialogQuestion — текущий вопрос диалога любого вида.
func (s *Service) dialogQuestion(ctx context.Context, d *model.Dialog) (question, error) {
	g, err := s.dialogGame(ctx, d)
	if err != nil {
		return question{}, err
	}
	switch d.Kind {
	case model.DialogGameSetup:
		return s.setupQuestion(ctx, g, d.Step)
	case model.DialogCharacterCreate:
		return s.characterQuestion(ctx, g, d.Step)
	}
	return question{}, fmt.Errorf("bot.dialogQuestion: unknown dialog kind %q", d.Kind)
}

// answerButtons — ряды кнопок с ответами на шаг: подпись → данные `@шаг ответ`, по perRow кнопок в ряду;
// последним рядом «Прервать» (/cancel).
func answerButtons(step string, perRow int, answers ...[2]string) model.Keyboard {
	var kb model.Keyboard
	var row []model.Button
	for _, a := range answers {
		row = append(row, model.Button{Text: a[0], Data: model.CallbackAnswer(step, a[1])})
		if len(row) == perRow {
			kb = append(kb, row)
			row = nil
		}
	}
	if len(row) > 0 {
		kb = append(kb, row)
	}
	return append(kb, cancelRow())
}

// cancelRow — кнопка «Прервать» диалога.
func cancelRow() []model.Button {
	return []model.Button{{Text: "Прервать", Data: model.CallbackCommand("cancel", "")}}
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
