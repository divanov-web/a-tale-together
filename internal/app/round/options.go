package round

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// Ошибки заявки по кнопке варианта (ADR-0015).
var (
	ErrRoundClosed = errors.New("кнопки этого раунда устарели: он уже закрыт. Заявку на текущий раунд напишите текстом или нажмите вариант из свежего сообщения")
	ErrOptionGone  = errors.New("этот вариант уже недоступен: обстановка изменилась. Напишите заявку текстом или выберите вариант из свежего сообщения")
)

// OptionRef — ссылка на вариант сцены в данных кнопки: `#<раунд>:<локация>:<объект>:<подход>`; умещается в 64 байта
// `callback_data` вместо названия подхода. Раунд защищает от нажатия после закрытия, остальное находит подход заново по
// текущему состоянию. Кнопок переходов нет (B36, ADR-0025): старая ссылка с объектом `move` даёт ErrOptionGone.
type OptionRef struct {
	RoundNo    int
	LocationID model.LocationID
	Object     string
	Idx        int
}

// String — аргумент `/act` для кнопки.
func (r OptionRef) String() string {
	return fmt.Sprintf("#%d:%d:%s:%d", r.RoundNo, r.LocationID, r.Object, r.Idx)
}

// ParseOptionRef разбирает аргумент `/act`, начинающийся с `#`; false — это обычный текст заявки.
func ParseOptionRef(arg string) (OptionRef, bool) {
	arg = strings.TrimSpace(arg)
	if !strings.HasPrefix(arg, "#") {
		return OptionRef{}, false
	}
	parts := strings.Split(arg[1:], ":")
	if len(parts) != 4 {
		return OptionRef{}, false
	}
	roundNo, err1 := strconv.Atoi(parts[0])
	loc, err2 := strconv.ParseInt(parts[1], 10, 64)
	idx, err3 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil || err3 != nil || parts[2] == "" {
		return OptionRef{}, false
	}
	return OptionRef{RoundNo: roundNo, LocationID: model.LocationID(loc), Object: parts[2], Idx: idx}, true
}

// optionsKeyboard — кнопки вариантов персонажа по его локации под личным сообщением (ADR-0015): по кнопке на подход,
// нажатие = `/act #ссылка`; порядок как в тексте `optionsFor`. nil без локации или подходов.
func optionsKeyboard(c model.SnapshotCharacter, opts []narrator.Option, roundNo int) model.Keyboard {
	if c.LocationID == nil {
		return nil
	}
	var kb model.Keyboard
	for _, o := range opts {
		if o.LocationID != *c.LocationID {
			continue
		}
		ref := OptionRef{RoundNo: roundNo, LocationID: o.LocationID, Object: o.Object, Idx: o.Idx}
		kb = append(kb, []model.Button{{Text: o.Title, Data: model.CallbackCommand("act", ref.String())}})
	}
	return kb
}

// SubmitOption — заявка по кнопке варианта: принимается, только если открыт раунд с номером из ссылки и подход ещё
// существует в локации персонажа; текстом заявки становится название подхода, дальше как Submit.
func (s *Service) SubmitOption(ctx context.Context, game model.Game, c model.Character, ref OptionRef) (model.Round, model.Submission, error) {
	if game.Status != model.GameActive {
		return model.Round{}, model.Submission{}, statusError(game.Status)
	}
	r, err := s.store.OpenRound(ctx, game.ID)
	if errors.Is(err, model.ErrNotFound) || (err == nil && (r.RoundNo != ref.RoundNo || !r.DeadlineAt.After(s.clock.Now()))) {
		return model.Round{}, model.Submission{}, ErrRoundClosed
	}
	if err != nil {
		return model.Round{}, model.Submission{}, fmt.Errorf("round.SubmitOption: %w", err)
	}
	if c.LocationID == nil || *c.LocationID != ref.LocationID {
		return model.Round{}, model.Submission{}, ErrOptionGone
	}
	chars, err := s.store.ListCharacters(ctx, game.ID, r.SeasonID)
	if err != nil {
		return model.Round{}, model.Submission{}, fmt.Errorf("round.SubmitOption: %w", err)
	}
	locations, err := s.store.ListLocations(ctx, game.ID)
	if err != nil {
		return model.Round{}, model.Submission{}, fmt.Errorf("round.SubmitOption: %w", err)
	}
	def, err := s.characterDef(ctx, game)
	if err != nil {
		return model.Round{}, model.Submission{}, fmt.Errorf("round.SubmitOption: %w", err)
	}
	state := snapshot(game, chars, locations, nil, nil, nil, def)
	for _, o := range narrator.Options(&state, def, game.Mode) {
		if o.LocationID == ref.LocationID && o.Object == ref.Object && o.Idx == ref.Idx {
			return s.Submit(ctx, game, c, o.Title)
		}
	}
	return model.Round{}, model.Submission{}, ErrOptionGone
}
