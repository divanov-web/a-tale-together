package bot

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"taletogether/internal/app/game"
	"taletogether/internal/model"
)

// playerButton — кнопка reply-клавиатуры лички (ADR-0021): подпись, которую Telegram отправит текстом, и команда реестра.
type playerButton struct{ label, command string }

// playerButtons — ряды клавиатуры игрока с персонажем в незавершённой игре (B29): сведения о персонаже и разведка
// соседних клеток (B36: кнопок направлений нет, куда идти — `/scout`), 6 кнопок в пределах ADR-0021.
// Кнопки `/act` нет: reply-кнопка отправляет текст сразу, а заявка и так пишется обычным сообщением (B17).
var playerButtons = [][]playerButton{
	{{"Персонаж", "character"}, {"Описание", "bio"}, {"Знания", "knowledge"}},
	{{"Группа", "party"}, {"Квесты", "quests"}, {"Разведка", "scout"}},
}

// actPlaceholder — подсказка в поле ввода у игрока в игре: текст без команды — заявка (B17).
const actPlaceholder = "Опишите действие…"

// playerKeyboardHint — сообщение перед итогом создания персонажа, с которым приходит клавиатура игрока (B29).
const playerKeyboardHint = "Кнопки под полем ввода — сведения о персонаже и разведка. Действие в раунде пишите обычным сообщением."

// buttonCommand — команда реестра по тексту нажатой reply-кнопки; ok=false, если текст не подпись кнопки.
func buttonCommand(text string) (string, bool) {
	text = strings.TrimSpace(text)
	for _, row := range playerButtons {
		for _, b := range row {
			if b.label == text {
				return b.command, true
			}
		}
	}
	return "", false
}

// statusKeyboard — reply-клавиатура лички по статусу пользователя (ADR-0021): есть персонаж в незавершённой игре —
// кнопки игрока, иначе снятие клавиатуры. Во время диалога подсказки «Опишите действие…» нет: ждётся ответ на шаг.
// Набор считается заново при каждом ответе без inline-кнопок, поэтому клавиатура сама следует за статусом:
// создание персонажа ставит её, завершение игры снимает при следующем ответе бота.
func (s *Service) statusKeyboard(ctx context.Context, chat model.TelegramChatID) (model.ReplyKeyboard, error) {
	user, err := s.store.GetUserByTelegramID(ctx, model.TelegramUserID(chat))
	if errors.Is(err, model.ErrNotFound) {
		return model.ReplyKeyboard{}, nil
	}
	if err != nil {
		return model.ReplyKeyboard{}, err
	}
	playing, err := s.hasCharacter(ctx, user)
	if err != nil || !playing {
		return model.ReplyKeyboard{}, err
	}
	rk := model.ReplyKeyboard{Placeholder: actPlaceholder}
	for _, row := range playerButtons {
		labels := make([]string, 0, len(row))
		for _, b := range row {
			labels = append(labels, b.label)
		}
		rk.Rows = append(rk.Rows, labels)
	}
	if _, err := s.dialogs.GetDialog(ctx, user.ID); err == nil {
		rk.Placeholder = ""
	} else if !errors.Is(err, model.ErrNotFound) {
		return model.ReplyKeyboard{}, err
	}
	return rk, nil
}

// hasCharacter сообщает, есть ли у пользователя персонаж хотя бы в одной незавершённой игре.
func (s *Service) hasCharacter(ctx context.Context, user model.User) (bool, error) {
	games, err := s.games.Of(ctx, user.ID)
	if err != nil {
		return false, err
	}
	for _, g := range games {
		_, err := s.games.Character(ctx, g, user.ID)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, game.ErrNoCharacter) {
			return false, err
		}
	}
	return false, nil
}

// sendPrivate отправляет ответ в личку с reply-клавиатурой по статусу; сбой расчёта набора только в лог,
// ответ уходит без смены клавиатуры.
func (s *Service) sendPrivate(ctx context.Context, chat model.TelegramChatID, text model.FormattedText) error {
	rk, err := s.statusKeyboard(ctx, chat)
	if err != nil {
		s.logger.Warn("reply keyboard not computed", slog.Int64("chat_id", int64(chat)), slog.Any("err", err))
		return s.tg.SendText(ctx, chat, text)
	}
	return s.tg.SendReplyKeyboard(ctx, chat, text, rk)
}
