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
	"taletogether/internal/service/narrator"
	"taletogether/internal/service/rules"
	"taletogether/internal/service/visibility"
)

// snapshot собирает снимок: персонажи с характеристиками и группой, локации с объектами, заявки; группы, лимиты и
// резерв идентификаторов дописывает `snapshotParties` при заморозке.
func snapshot(g model.Game, chars []model.Character, locations []model.Location, subs []model.Submission, def model.CharacterModelDef) model.RoundSnapshot {
	snap := model.RoundSnapshot{SchemaVersion: model.SnapshotSchemaVersion, Mode: g.Mode, RulesVersion: g.RulesVersion, Characters: []model.SnapshotCharacter{}, Locations: []model.SnapshotLocation{}, Submissions: []model.SnapshotSubmission{}}
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
			ID: c.ID, UserID: c.UserID, Nickname: c.Nickname, Gender: c.Gender, Status: c.Status, MissedRounds: c.MissedRounds, DeathRoundsLeft: c.DeathRoundsLeft,
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

// resolve разрешает замороженный раунд (пункты 07–09): интерпретатор одним вызовом (или разбор кодом) → движок →
// события, итоги, состояние персонажей и локаций → рассказчик одним вызовом (или шаблон) → статус `published`,
// хроника частями и уведомления о заявках в outbox. Внутри транзакции заморозки (ADR-0010, ADR-0011): вызовы модели
// держат блокировку игры, `/act` и `/status` её не ждут. Метрики шагов (пункт 10) пишутся в `rounds.metrics`, лог и журнал.
func (s *Service) resolve(ctx context.Context, g model.Game, chat model.Chat, r model.Round, genre model.Genre, def model.CharacterModelDef, chars []model.Character, now time.Time, m *model.RoundMetrics) error {
	if r.Snapshot == nil {
		return fmt.Errorf("round.resolve: round %d without snapshot", r.ID)
	}
	step := s.clock.Now()
	intents := s.interpret(ctx, g, chat, r, def, m)
	m.InterpretMS, step = s.lap(step)
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
	if err := s.saveParties(ctx, g, r.SeasonID, r.Snapshot, &state, chars); err != nil {
		return err
	}
	if err := s.saveState(ctx, g, r.Snapshot, &state, now); err != nil {
		return err
	}
	if err := s.syncParties(ctx, g, r.SeasonID); err != nil {
		return err
	}
	m.Events = len(result.Events)
	for _, res := range result.Results {
		switch res.Status {
		case model.ResultResolved:
			m.Resolved++
		case model.ResultRejected:
			m.Rejected++
		case model.ResultAmbiguous:
			m.Ambiguous++
		}
	}
	m.EngineMS, step = s.lap(step)
	r.Status = model.RoundPublished
	r.Narration = s.narrate(ctx, g, chat, r, genre, def, &state, chars, result.Events, m)
	m.NarrateMS, step = s.lap(step)
	s.journal(ctx, g, chat.TelegramChatID, r, "resolved", map[string]any{"results": result.Results, "events": result.Events})
	if err := s.publish(ctx, g, chat, r, &state, result, def, m); err != nil {
		return err
	}
	m.PublishMS, _ = s.lap(step)
	m.TotalMS = s.clock.Now().Sub(now).Milliseconds()
	r.Metrics = m
	if err := s.store.UpdateRound(ctx, r); err != nil {
		return err
	}
	s.logger.Info("round resolved", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.Int("events", len(result.Events)), slog.Int("results", len(result.Results)))
	s.logger.Info("round metrics", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.Int("attempt", m.Attempt),
		slog.Int64("total_ms", m.TotalMS), slog.Int64("interpret_ms", m.InterpretMS), slog.Int64("engine_ms", m.EngineMS), slog.Int64("narrate_ms", m.NarrateMS),
		slog.Int("llm_calls", m.LLMCalls), slog.Int("tokens_in", m.TokensIn), slog.Int("tokens_out", m.TokensOut),
		slog.Bool("interpreter_fallback", m.InterpreterFallback), slog.Bool("narrator_fallback", m.NarratorFallback),
		slog.Int("submissions", m.Submissions), slog.Int("resolved", m.Resolved), slog.Int("rejected", m.Rejected), slog.Int("ambiguous", m.Ambiguous), slog.Int("safe", m.Safe),
		slog.Int("events", m.Events), slog.Int("patched", m.Patched), slog.Int("issues", m.Issues))
	s.journal(ctx, g, chat.TelegramChatID, r, "metrics", m)
	return nil
}

// lap — миллисекунды с момента since и новый отсчёт.
func (s *Service) lap(since time.Time) (int64, time.Time) {
	now := s.clock.Now()
	return now.Sub(since).Milliseconds(), now
}

// interpret — вызов 1 из 2 (docs/llm/round-calls.md): все заявки снимка одним вызовом модели через `app/llm.Complete`
// с ролью `interpreter` и раундом для учёта. Без провайдера, профиля или при сбое — запасной разбор кодом
// (`interpreter.Fallback`: типовые команды, остальное безопасное толкование); раунд не падает.
func (s *Service) interpret(ctx context.Context, g model.Game, chat model.Chat, r model.Round, def model.CharacterModelDef, m *model.RoundMetrics) []model.Intent {
	snap := *r.Snapshot
	if len(snap.Submissions) == 0 {
		return nil
	}
	if s.LLM == nil {
		m.InterpreterFallback = true
		intents := interpreter.Fallback(snap, "интерпретатор не настроен: типовые команды")
		s.journal(ctx, g, chat.TelegramChatID, r, "interpreted", map[string]any{"fallback": "интерпретатор не настроен", "intents": intents})
		return intents
	}
	call := applm.Call{Game: &g, Round: &r.ID, Role: model.RoleInterpreter, PromptVersion: interpreter.PromptVersion}
	svc := interpreter.New(interpreter.Func(func(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		return s.LLM.Complete(ctx, call, req)
	}), s.logger)
	res, err := svc.Interpret(ctx, snap, def)
	if err != nil {
		s.logger.Warn("interpreter unavailable, typed fallback", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.Any("err", err))
		m.InterpreterFallback = true
		intents := interpreter.Fallback(snap, "интерпретатор недоступен: типовые команды")
		s.journal(ctx, g, chat.TelegramChatID, r, "interpreted", map[string]any{"fallback": "интерпретатор недоступен", "error": err.Error(), "intents": intents})
		return intents
	}
	s.logger.Info("round interpreted", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)),
		slog.Int("submissions", len(snap.Submissions)), slog.Int("calls", res.Calls), slog.Int("safe", res.Invalid),
		slog.Int("tokens_in", res.TokensIn), slog.Int("tokens_out", res.TokensOut))
	s.journal(ctx, g, chat.TelegramChatID, r, "interpreted", map[string]any{"calls": res.Calls, "safe": res.Invalid, "intents": res.Intents})
	m.LLMCalls, m.TokensIn, m.TokensOut, m.Safe = m.LLMCalls+res.Calls, m.TokensIn+res.TokensIn, m.TokensOut+res.TokensOut, res.Invalid
	return res.Intents
}

// narrate — вызов 2 из 2 (docs/llm/round-calls.md): публичные и локационные события, состояние после свёртки,
// описания персонажей как данные и выжимка прошлых хроник → хроника по сценам через `app/llm.Complete` с ролью
// `narrator`. Без провайдера, при сбое, утечке или превышении бюджета — nil: публикуется шаблонная хроника, раунд не откатывается.
func (s *Service) narrate(ctx context.Context, g model.Game, chat model.Chat, r model.Round, genre model.Genre, def model.CharacterModelDef, state *model.RoundSnapshot, chars []model.Character, events []model.Event, m *model.RoundMetrics) *model.Narration {
	if s.LLM == nil {
		m.NarratorFallback = true
		s.journal(ctx, g, chat.TelegramChatID, r, "narrated", map[string]any{"fallback": "рассказчик не настроен: шаблонная хроника"})
		return nil
	}
	p := narrator.Params{Title: g.Title, Tone: genre.Template.Tone, Mode: g.Mode, RoundNo: r.RoundNo, State: state, Bios: map[model.CharacterID]narrator.Bio{}, Events: visibility.Public(events), Def: def}
	for _, c := range chars {
		p.Bios[c.ID] = narrator.Bio{Public: c.PublicBio, Secret: c.SecretBio}
	}
	factions, err := s.store.ListFactions(ctx, g.ID)
	if err != nil {
		s.logger.Warn("narrator: factions unavailable", slog.Int64("game_id", int64(g.ID)), slog.Any("err", err))
	}
	for _, f := range factions {
		rel := map[string]string{}
		for k, v := range f.Relations {
			rel[string(k)] = v
		}
		p.Factions = append(p.Factions, narrator.Faction{Code: string(f.Code), Title: f.Title, Description: f.Description, Tone: f.Tone, Relations: rel})
	}
	if p.Previous, err = s.store.RecentNarrations(ctx, g.ID, 3); err != nil {
		s.logger.Warn("narrator: previous chronicles unavailable", slog.Int64("game_id", int64(g.ID)), slog.Any("err", err))
	}
	call := applm.Call{Game: &g, Round: &r.ID, Role: model.RoleNarrator, PromptVersion: narrator.PromptVersion}
	svc := narrator.New(narrator.Func(func(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		return s.LLM.Complete(ctx, call, req)
	}), s.logger)
	res, err := svc.Narrate(ctx, p)
	if err != nil {
		s.logger.Warn("narrator unavailable, template chronicle", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.Any("err", err))
		m.NarratorFallback = true
		s.journal(ctx, g, chat.TelegramChatID, r, "narrated", map[string]any{"fallback": "шаблонная хроника", "error": err.Error()})
		return nil
	}
	s.logger.Info("round narrated", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)),
		slog.Int("scenes", len(res.Narration.Scenes)), slog.Int("calls", res.Calls), slog.Int("patched", res.Patched), slog.Int("issues", len(res.Issues)),
		slog.Int("tokens_in", res.TokensIn), slog.Int("tokens_out", res.TokensOut))
	s.journal(ctx, g, chat.TelegramChatID, r, "narrated", map[string]any{"calls": res.Calls, "patched": res.Patched, "issues": res.Issues, "narration": res.Narration})
	m.LLMCalls, m.TokensIn, m.TokensOut, m.Patched, m.Issues = m.LLMCalls+res.Calls, m.TokensIn+res.TokensIn, m.TokensOut+res.TokensOut, res.Patched, len(res.Issues)
	return &res.Narration
}

// saveState записывает изменившихся персонажей (включая группу после `party_split`) и локации; значения берутся
// только из свёртки событий.
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
		state := model.Character{ID: c.ID, LocationID: c.LocationID, PartyID: c.PartyID, HP: c.HP, Conditions: c.Conditions, Knowledge: c.Knowledge, Status: c.Status, MissedRounds: c.MissedRounds, DeathRoundsLeft: c.DeathRoundsLeft, DiedAt: diedAt}
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

// mentions — `@username` игроков по никнеймам персонажей снимка для тегов в хронике (B2); без username персонаж не тегается.
func (s *Service) mentions(ctx context.Context, state *model.RoundSnapshot) (map[string]string, error) {
	out := map[string]string{}
	for _, c := range state.Characters {
		user, err := s.store.GetUser(ctx, c.UserID)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if user.Username != "" {
			out[c.Nickname] = "@" + user.Username
		}
	}
	return out, nil
}

// publish ставит в outbox хронику (рассказчика или шаблонную) с подходами следующего раунда, частями по лимиту
// Telegram, и уведомление о заявке каждому подавшему её с вариантами следующего раунда по его локации (B15) и кнопками
// этих вариантов (ADR-0015).
func (s *Service) publish(ctx context.Context, g model.Game, chat model.Chat, r model.Round, state *model.RoundSnapshot, result engine.Result, def model.CharacterModelDef, m *model.RoundMetrics) error {
	preset := rules.Preset(g.Mode)
	mentions, err := s.mentions(ctx, state)
	if err != nil {
		return err
	}
	opts := narrator.Options(state, def, g.Mode)
	chronicle := tagMentions(chronicleMessage(g, r, visibility.Public(result.Events), state, opts), mentions)
	for i, part := range splitParts(chronicle, header(g, r.RoundNo)+" (продолжение)\n", partLimit) {
		if err := s.store.EnqueueOutbox(ctx, model.OutboxMessage{GameID: g.ID, RoundID: &r.ID, TelegramChatID: chat.TelegramChatID, Kind: model.OutboxChronicle, Part: i + 1, Text: part, SendAfter: s.clock.Now()}); err != nil {
			return err
		}
	}
	parts := len(splitParts(chronicle, header(g, r.RoundNo)+" (продолжение)\n", partLimit))
	notices := 0
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
		text := noticeText(g, r, res, own, preset, optionsFor(*c, state, opts, fmt.Sprintf("Варианты на раунд %d", r.RoundNo+1)))
		if err := s.store.EnqueueOutbox(ctx, model.OutboxMessage{GameID: g.ID, RoundID: &r.ID, TelegramChatID: user.PrivateChat(), Kind: model.OutboxPrivate, Part: 1, Text: text, Keyboard: optionsKeyboard(*c, opts, r.RoundNo+1), SendAfter: s.clock.Now()}); err != nil {
			return err
		}
		notices++
	}
	m.ChronicleParts, m.Notices = parts, notices
	s.journal(ctx, g, chat.TelegramChatID, r, "published", map[string]any{"chronicle_parts": parts, "notices": notices})
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
