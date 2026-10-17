package round

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"taletogether/internal/model"
	"taletogether/internal/service/rules"
)

// Ошибки просьбы о подсказке (пункт 35, ADR-0038); тексты показываются игроку.
var (
	ErrHintPending  = errors.New("подсказку уже просили: появится в ближайшей хронике")
	ErrHintCooldown = errors.New("подсказка на перезарядке") // сентинел для errors.Is; текст игроку даёт HintCooldownError
)

// HintCooldownError — подсказку уже просили недавно: сколько раундов осталось и с какого раунда можно снова.
type HintCooldownError struct {
	Left    int
	ReadyAt int
}

func (e *HintCooldownError) Error() string {
	return fmt.Sprintf("подсказку уже просили, следующая через %d раундов (с раунда %d)", e.Left, e.ReadyAt)
}

// Is сопоставляет ошибку с ErrHintCooldown.
func (e *HintCooldownError) Is(target error) bool { return target == ErrHintCooldown }

// Hint — просьба о подсказке рассказчика для фракции персонажа (`/hint`, кнопка «Подсказка»; пункт 35, ADR-0038):
// игра `active`, персонаж не выбыл, раунд открыт. Просьба одна на фракцию до выдачи (`ErrHintPending`), затем
// перезарядка `hint` (`rules.CooldownRounds`, `HintCooldownError`). Флаг фракции и перезарядка пишутся в одной
// транзакции; подсказку даёт рассказчик в хронике ближайшего раунда, флаг снимает `applyHints`.
func (s *Service) Hint(ctx context.Context, game model.Game, c model.Character) (model.Round, error) {
	if game.Status != model.GameActive {
		return model.Round{}, statusError(game.Status)
	}
	switch c.Status {
	case model.CharacterDead:
		return model.Round{}, fmt.Errorf("%w (ещё %d раундов)", ErrDead, c.DeathRoundsLeft)
	case model.CharacterInactive:
		return model.Round{}, ErrAbsent
	}
	now := s.clock.Now()
	r, err := s.store.OpenRound(ctx, game.ID)
	if errors.Is(err, model.ErrNotFound) || (err == nil && !r.DeadlineAt.After(now)) {
		return model.Round{}, ErrNoOpenRound
	}
	if err != nil {
		return model.Round{}, fmt.Errorf("round.Hint: %w", err)
	}
	err = s.store.InTx(ctx, func(ctx context.Context) error {
		factions, err := s.store.ListFactions(ctx, game.ID)
		if err != nil {
			return err
		}
		var f model.Faction
		found := false
		for _, x := range factions {
			if x.ID == c.FactionID {
				f, found = x, true
			}
		}
		if !found {
			return fmt.Errorf("faction %d not found", c.FactionID)
		}
		if f.HintRound != nil {
			return ErrHintPending
		}
		list, err := s.store.ListCooldowns(ctx, game.ID, model.CooldownFaction, int64(f.ID))
		if err != nil {
			return err
		}
		last := 0
		for _, cd := range list {
			if cd.Action == model.CooldownHint {
				last = cd.LastRound
			}
		}
		if st := rules.CheckCooldown(model.CooldownHint, last, r.RoundNo); !st.Ready {
			return &HintCooldownError{Left: st.Left, ReadyAt: st.ReadyAt}
		}
		no := r.RoundNo
		if err := s.store.SetFactionHint(ctx, game.ID, f.ID, &no); err != nil {
			return err
		}
		return s.store.SetCooldown(ctx, model.Cooldown{GameID: game.ID, Owner: model.CooldownFaction, OwnerID: int64(f.ID), Action: model.CooldownHint, LastRound: r.RoundNo})
	})
	var cooldown *HintCooldownError
	if errors.As(err, &cooldown) {
		return model.Round{}, cooldown // текст отказа игроку без обёрток
	}
	if errors.Is(err, ErrHintPending) {
		return model.Round{}, ErrHintPending
	}
	if err != nil {
		return model.Round{}, fmt.Errorf("round.Hint: %w", err)
	}
	s.logger.Info("hint requested", slog.Int64("game_id", int64(game.ID)), slog.Int64("round_id", int64(r.ID)), slog.Int64("character_id", int64(c.ID)), slog.Int64("faction_id", int64(c.FactionID)))
	if s.Journal != nil {
		if chat, err := s.store.GetChat(ctx, game.ChatID); err == nil {
			s.journal(ctx, game, chat.TelegramChatID, r, "hint", map[string]any{"character_id": c.ID, "character": c.Nickname, "faction_id": c.FactionID})
		}
	}
	return r, nil
}

// snapshotHints — просьбы фракций о подсказке для снимка раунда (ADR-0038): фракции с флагом.
func snapshotHints(factions []model.Faction) []model.SnapshotHint {
	var out []model.SnapshotHint
	for _, f := range factions {
		if f.HintRound != nil {
			out = append(out, model.SnapshotHint{FactionID: f.ID, Faction: f.Code, Round: *f.HintRound})
		}
	}
	return out
}

// applyHints закрывает выполненные подсказки после хроники (ADR-0038): по кодам фракций из `hints_given` рассказчика
// флаг снимается, перезарядка `hint` переписывается раундом хроники (просьба, пережившая сбой, не теряет раунды),
// событие `hint_given` (public) уходит в журнал. Фракция без подсказки остаётся с флагом до следующей хроники.
func (s *Service) applyHints(ctx context.Context, g model.Game, r model.Round, state *model.RoundSnapshot, given []model.FactionCode) ([]model.Event, error) {
	if r.Snapshot == nil || len(r.Snapshot.Hints) == 0 || len(given) == 0 {
		return nil, nil
	}
	done := map[model.FactionCode]bool{}
	for _, code := range given {
		done[code] = true
	}
	var events []model.Event
	for _, h := range r.Snapshot.Hints {
		if !done[h.Faction] {
			continue
		}
		if err := s.store.SetFactionHint(ctx, g.ID, h.FactionID, nil); err != nil {
			return nil, err
		}
		if err := s.store.SetCooldown(ctx, model.Cooldown{GameID: g.ID, Owner: model.CooldownFaction, OwnerID: int64(h.FactionID), Action: model.CooldownHint, LastRound: r.RoundNo}); err != nil {
			return nil, err
		}
		payload, err := json.Marshal(map[string]any{"faction_id": h.FactionID, "faction": h.Faction, "requested_round": h.Round})
		if err != nil {
			return nil, fmt.Errorf("round.applyHints: %w", err)
		}
		events = append(events, model.Event{GameID: g.ID, RoundID: r.ID, Type: model.EventHintGiven, Payload: payload, Visibility: model.VisibilityPublic})
	}
	if err := s.commitEvents(ctx, g, state, events); err != nil {
		return nil, err
	}
	return events, nil
}
