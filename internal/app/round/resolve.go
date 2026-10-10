package round

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
	"taletogether/internal/service/engine"
	"taletogether/internal/service/interpreter"
	"taletogether/internal/service/rules"
	"taletogether/internal/service/visibility"
)

// snapshot собирает снимок v2: персонажи с характеристиками, локации с объектами, заявки.
func snapshot(g model.Game, chars []model.Character, locations []model.Location, subs []model.Submission, def model.CharacterModelDef) model.RoundSnapshot {
	snap := model.RoundSnapshot{SchemaVersion: 2, Mode: g.Mode, RulesVersion: g.RulesVersion, Characters: []model.SnapshotCharacter{}, Locations: []model.SnapshotLocation{}, Submissions: []model.SnapshotSubmission{}}
	for _, c := range chars {
		maxHP, err := rules.HP(def, c.Attributes)
		if err != nil || maxHP <= 0 {
			maxHP = max(c.HP, 1)
		}
		conditions, knowledge := c.Conditions, c.Knowledge
		if conditions == nil {
			conditions = []string{}
		}
		if knowledge == nil {
			knowledge = []string{}
		}
		snap.Characters = append(snap.Characters, model.SnapshotCharacter{
			ID: c.ID, UserID: c.UserID, Nickname: c.Nickname, Status: c.Status, MissedRounds: c.MissedRounds, DeathRoundsLeft: c.DeathRoundsLeft,
			HP: c.HP, MaxHP: maxHP, FactionID: c.FactionID, PartyID: c.PartyID, LocationID: c.LocationID, Conditions: conditions,
			Attributes: c.Attributes, Skills: c.Skills, Inventory: c.Inventory, Knowledge: knowledge,
		})
	}
	for _, l := range locations {
		snap.Locations = append(snap.Locations, model.SnapshotLocation{ID: l.ID, Name: l.Name, Description: l.Description, Difficulty: l.Difficulty, Links: l.Links, Objects: l.Objects})
	}
	for _, sub := range subs {
		snap.Submissions = append(snap.Submissions, model.SnapshotSubmission{SubmissionID: sub.ID, CharacterID: sub.CharacterID, Text: sub.RawText, SubmittedAt: sub.SubmittedAt})
	}
	return snap
}

// resolve разрешает замороженный раунд (пункты 07–08): интерпретатор одним вызовом (или разбор кодом) → движок →
// события, итоги, состояние персонажей и локаций, статус `published`, хроника и уведомления в outbox.
// Внутри транзакции заморозки (ADR-0010): вызов модели держит блокировку игры, `/act` и `/status` её не ждут.
func (s *Service) resolve(ctx context.Context, g model.Game, chat model.Chat, r model.Round, def model.CharacterModelDef, now time.Time) error {
	if r.Snapshot == nil {
		return fmt.Errorf("round.resolve: round %d without snapshot", r.ID)
	}
	intents := s.interpret(ctx, g, r, def)
	result := engine.Resolve(*r.Snapshot, intents, r.Seed)
	offset, err := s.store.NextEventSeq(ctx, g.ID)
	if err != nil {
		return err
	}
	for i := range result.Events {
		result.Events[i].GameID, result.Events[i].RoundID = g.ID, r.ID
		result.Events[i].Seq += offset - 1
	}
	for i := range result.Results {
		for j := range result.Results[i].EventSeqs {
			result.Results[i].EventSeqs[j] += offset - 1
		}
	}
	if err := s.store.AppendEvents(ctx, result.Events); err != nil {
		return err
	}
	if err := s.store.CreateResolvedActions(ctx, g.ID, r.ID, result.Results); err != nil {
		return err
	}
	state := engine.Clone(*r.Snapshot)
	if err := engine.Apply(&state, result.Events); err != nil {
		return err
	}
	if err := s.saveState(ctx, g, r.Snapshot, &state, now); err != nil {
		return err
	}
	r.Status = model.RoundPublished
	if err := s.store.UpdateRound(ctx, r); err != nil {
		return err
	}
	if err := s.publish(ctx, g, chat, r, &state, result); err != nil {
		return err
	}
	s.logger.Info("round resolved", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.Int("events", len(result.Events)), slog.Int("results", len(result.Results)))
	return nil
}

// interpret — вызов 1 из 2 (docs/llm/round-calls.md): все заявки снимка одним вызовом модели через `app/llm.Complete`
// с ролью `interpreter` и раундом для учёта. Без провайдера, профиля или при сбое — запасной разбор кодом
// (`interpreter.Fallback`: типовые команды, остальное безопасное толкование); раунд не падает.
func (s *Service) interpret(ctx context.Context, g model.Game, r model.Round, def model.CharacterModelDef) []model.Intent {
	snap := *r.Snapshot
	if len(snap.Submissions) == 0 {
		return nil
	}
	if s.LLM == nil {
		return interpreter.Fallback(snap, "интерпретатор не настроен: типовые команды")
	}
	call := applm.Call{Game: &g, Round: &r.ID, Role: model.RoleInterpreter, PromptVersion: interpreter.PromptVersion}
	svc := interpreter.New(interpreter.Func(func(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		return s.LLM.Complete(ctx, call, req)
	}), s.logger)
	res, err := svc.Interpret(ctx, snap, def)
	if err != nil {
		s.logger.Warn("interpreter unavailable, typed fallback", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.Any("err", err))
		return interpreter.Fallback(snap, "интерпретатор недоступен: типовые команды")
	}
	s.logger.Info("round interpreted", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)),
		slog.Int("submissions", len(snap.Submissions)), slog.Int("calls", res.Calls), slog.Int("safe", res.Invalid),
		slog.Int("tokens_in", res.TokensIn), slog.Int("tokens_out", res.TokensOut))
	return res.Intents
}

// saveState записывает изменившихся персонажей и локации; значения берутся только из свёртки событий.
func (s *Service) saveState(ctx context.Context, g model.Game, before *model.RoundSnapshot, after *model.RoundSnapshot, now time.Time) error {
	for i, c := range after.Characters {
		prev := before.Characters[i]
		if reflect.DeepEqual(prev, c) {
			continue
		}
		var diedAt *time.Time
		if c.Status == model.CharacterDead {
			t := now
			diedAt = &t
		}
		state := model.Character{ID: c.ID, LocationID: c.LocationID, HP: c.HP, Conditions: c.Conditions, Knowledge: c.Knowledge, Status: c.Status, MissedRounds: c.MissedRounds, DeathRoundsLeft: c.DeathRoundsLeft, DiedAt: diedAt}
		if c.Status == model.CharacterDead && prev.Status != model.CharacterDead {
			window := 24 * 60
			if from, ok := clockMinutes(g.PlayFrom); ok {
				if to, ok := clockMinutes(g.PlayTo); ok && from != to {
					window = (to - from + 24*60) % (24 * 60)
				}
			}
			state.DeathRoundsLeft = rules.DeathRounds(g.Settings.DeathDays, rules.RoundsPerDay(g.RoundMinutes, window))
		}
		if err := s.store.SaveCharacterState(ctx, g.ID, state); err != nil {
			return err
		}
	}
	for i, l := range after.Locations {
		if reflect.DeepEqual(before.Locations[i].Objects, l.Objects) {
			continue
		}
		if err := s.store.UpdateLocationObjects(ctx, g.ID, l.ID, l.Objects); err != nil {
			return err
		}
	}
	return nil
}

// publish ставит в outbox хронику (публичные и локационные события словами) и приватные итоги каждому подавшему заявку.
func (s *Service) publish(ctx context.Context, g model.Game, chat model.Chat, r model.Round, state *model.RoundSnapshot, result engine.Result) error {
	preset := rules.Preset(g.Mode)
	chronicle := chronicleText(g, r, visibility.Public(result.Events), state)
	if err := s.enqueue(ctx, g, chat, &r.ID, model.OutboxChronicle, chronicle); err != nil {
		return err
	}
	for _, res := range result.Results {
		c, ok := state.Character(res.CharacterID)
		if !ok {
			continue
		}
		user, err := s.store.GetUser(ctx, c.UserID)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				continue
			}
			return err
		}
		if user.StartedAt == nil {
			continue
		}
		own := visibility.Filter(result.Events, visibility.Viewer{CharacterID: c.ID, LocationID: c.LocationID})
		text := privateText(g, r, res, own, preset)
		if err := s.store.EnqueueOutbox(ctx, model.OutboxMessage{GameID: g.ID, RoundID: &r.ID, TelegramChatID: user.PrivateChat(), Kind: model.OutboxPrivate, Part: 1, Text: text, SendAfter: s.clock.Now()}); err != nil {
			return err
		}
	}
	return nil
}

// Replay — проверка воспроизводимости (dev, S06): повторный прогон движка по снимку, намерениям и seed раунда
// сравнивается с записанными событиями. Возвращает число событий и признак совпадения.
func (s *Service) Replay(ctx context.Context, game model.Game, actor model.UserID, roundNo int) (int, bool, error) {
	if err := s.canAdmin(ctx, game, actor); err != nil {
		return 0, false, err
	}
	r, err := s.store.RoundByNo(ctx, game.ID, roundNo)
	if errors.Is(err, model.ErrNotFound) {
		return 0, false, ErrNoRound
	}
	if err != nil {
		return 0, false, fmt.Errorf("round.Replay: %w", err)
	}
	if r.Snapshot == nil || (r.Status != model.RoundResolved && r.Status != model.RoundPublished) {
		return 0, false, ErrNotResolved
	}
	stored, err := s.store.ListEvents(ctx, game.ID, r.ID)
	if err != nil {
		return 0, false, fmt.Errorf("round.Replay: %w", err)
	}
	actions, err := s.store.ListResolvedActions(ctx, game.ID, r.ID)
	if err != nil {
		return 0, false, fmt.Errorf("round.Replay: %w", err)
	}
	intents := make([]model.Intent, 0, len(actions))
	for _, a := range actions {
		intents = append(intents, a.Intent)
	}
	again := engine.Resolve(*r.Snapshot, intents, r.Seed)
	if len(again.Events) != len(stored) {
		return len(stored), false, nil
	}
	for i := range stored {
		if stored[i].Type != again.Events[i].Type || stored[i].Visibility != again.Events[i].Visibility || !jsonEqual(stored[i].Payload, again.Events[i].Payload) {
			return len(stored), false, nil
		}
	}
	return len(stored), true, nil
}
