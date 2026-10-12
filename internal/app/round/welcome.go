package round

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// welcomeSummaryRunes — предел краткого пересказа завязки или последней хроники в итоге создания персонажа.
const welcomeSummaryRunes = 500

// Welcome — что делать дальше новому персонажу (B23), по статусу игры: `draft` — ждать старта, бот напишет;
// `paused` — игра на паузе; `active` — номер раунда и дедлайн (или время следующего раунда), кратко завязка или
// последняя хроника, варианты хода по локации персонажа кнопками (`/act #…`, только при открытом раунде).
func (s *Service) Welcome(ctx context.Context, g model.Game, c model.Character) (string, model.Keyboard, error) {
	switch g.Status {
	case model.GameDraft:
		return "Администратор ещё не начал историю. Бот напишет сюда, когда она начнётся.", nil, nil
	case model.GamePaused:
		return "Игра на паузе. Бот напишет сюда, когда администратор её возобновит.", nil, nil
	case model.GameFinished:
		return "Игра завершена.", nil, nil
	}
	now := s.clock.Now()
	summary, err := s.summary(ctx, g, c)
	if err != nil {
		return "", nil, fmt.Errorf("round.Welcome: %w", err)
	}
	open, err := s.store.OpenRound(ctx, g.ID)
	if errors.Is(err, model.ErrNotFound) {
		text := "История уже идёт."
		if g.NextRoundAt != nil {
			text = fmt.Sprintf("История уже идёт. Следующий раунд откроется в %s, бот пришлёт сюда варианты хода.", FormatTime(g, *g.NextRoundAt, now))
		}
		return joinBlocks(text, summary), nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("round.Welcome: %w", err)
	}
	chars, err := s.store.ListCharacters(ctx, g.ID, open.SeasonID)
	if err != nil {
		return "", nil, fmt.Errorf("round.Welcome: %w", err)
	}
	locations, err := s.store.ListLocations(ctx, g.ID)
	if err != nil {
		return "", nil, fmt.Errorf("round.Welcome: %w", err)
	}
	def, err := s.characterDef(ctx, g)
	if err != nil {
		return "", nil, fmt.Errorf("round.Welcome: %w", err)
	}
	state := snapshot(g, chars, locations, nil, nil, def)
	opts := narrator.Options(&state, def, g.Mode)
	head := fmt.Sprintf("Идёт раунд %d, заявки до %s.", open.RoundNo, FormatTime(g, open.DeadlineAt, now))
	sc, ok := state.Character(c.ID)
	if !ok {
		return joinBlocks(head, summary, "Напишите, что делает персонаж, или /act <текст>."), nil, nil
	}
	options := optionsFor(*sc, &state, opts, "Варианты хода")
	hint := "Напишите, что делает персонаж, или /act <текст>."
	if options != "" {
		hint = "Нажмите вариант или напишите действие своими словами."
	}
	return joinBlocks(head, summary, options, hint), optionsKeyboard(*sc, opts, open.RoundNo), nil
}

// summary — кратко, что происходит: сцена последней опубликованной хроники в локации персонажа, иначе её общий
// текст или первая сцена, иначе завязка истории.
func (s *Service) summary(ctx context.Context, g model.Game, c model.Character) (string, error) {
	info, err := s.Info(ctx, g)
	if err != nil {
		return "", err
	}
	if info.Last != nil && info.Last.Narration != nil {
		n := info.Last.Narration
		text := n.Chronicle
		for i, sc := range n.Scenes {
			if (c.LocationID != nil && sc.LocationID == *c.LocationID) || (text == "" && i == 0) {
				text = sc.Text
			}
		}
		if text != "" {
			return fmt.Sprintf("Последняя хроника (раунд %d):\n%s", info.Last.RoundNo, truncateText(text, welcomeSummaryRunes)), nil
		}
	}
	if g.OpeningScene != "" {
		return "Завязка:\n" + truncateText(g.OpeningScene, welcomeSummaryRunes), nil
	}
	return "", nil
}

// notifyStartLater — личное сообщение участникам при /begin, если первый раунд откроется позже (вне игрового окна);
// при открытом сразу раунде участники получают личное «раунд открыт» (`notifyOpen`).
func (s *Service) notifyStartLater(ctx context.Context, g model.Game, chars []model.Character, next time.Time) error {
	now := s.clock.Now()
	text := fmt.Sprintf("«%s» — история началась\n\nПервый раунд откроется в %s, бот пришлёт сюда варианты хода.", g.Title, FormatTime(g, next, now))
	for _, c := range chars {
		if c.Status.Absent() {
			continue
		}
		user, err := s.store.GetUser(ctx, c.UserID)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if user.StartedAt == nil {
			continue
		}
		if err := s.store.EnqueueOutbox(ctx, model.OutboxMessage{GameID: g.ID, TelegramChatID: user.PrivateChat(), Kind: model.OutboxPrivateOpen, Part: 1, Text: text, SendAfter: now}); err != nil {
			return err
		}
	}
	return nil
}

// joinBlocks склеивает непустые блоки через пустую строку (B16).
func joinBlocks(blocks ...string) string {
	var out []string
	for _, b := range blocks {
		if b != "" {
			out = append(out, b)
		}
	}
	return strings.Join(out, "\n\n")
}
