// Package round — жизненный цикл раунда (пункты плана 05, 07–09): запуск кампании, открытие раундов по расписанию
// и игровому окну, приём заявок, заморозка снимка или пропуск без кворума, разрешение с двумя вызовами LLM и
// публикация через outbox, пауза, ручной шаг в dev.
package round

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
)

// Store — хранилище раундов; реализует `adapter/postgres`. Транзакция через контекст (ADR-0008).
type Store interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
	LockGame(ctx context.Context, id model.GameID) (model.Game, error)
	DueGameIDs(ctx context.Context, now time.Time) ([]model.GameID, error)
	UpdateGame(ctx context.Context, g model.Game) error
	ActiveSeason(ctx context.Context, game model.GameID) (model.Season, error)
	GetChat(ctx context.Context, id model.ChatID) (model.Chat, error)
	GetGenre(ctx context.Context, id model.GenreID) (model.Genre, error)
	GetCharacterModel(ctx context.Context, id model.CharacterModelID) (model.CharacterModel, error)
	GetUser(ctx context.Context, id model.UserID) (model.User, error)
	ListLocations(ctx context.Context, game model.GameID) ([]model.Location, error)
	UpdateLocationObjects(ctx context.Context, game model.GameID, id model.LocationID, objects []model.LocationObject) error
	SaveCharacterState(ctx context.Context, game model.GameID, c model.Character) error
	NextEventSeq(ctx context.Context, game model.GameID) (int64, error)
	AppendEvents(ctx context.Context, events []model.Event) error
	ListEvents(ctx context.Context, game model.GameID, round model.RoundID) ([]model.Event, error)
	CreateResolvedActions(ctx context.Context, game model.GameID, round model.RoundID, results []model.ActionResult) error
	ListResolvedActions(ctx context.Context, game model.GameID, round model.RoundID) ([]model.ActionResult, error)
	ListFactions(ctx context.Context, game model.GameID) ([]model.Faction, error)
	ListCharacters(ctx context.Context, game model.GameID, season model.SeasonID) ([]model.Character, error)
	UpdateCharacterActivity(ctx context.Context, game model.GameID, id model.CharacterID, status model.CharacterStatus, missed int) error
	CreateRound(ctx context.Context, r model.Round) (model.Round, error)
	UpdateRound(ctx context.Context, r model.Round) error
	OpenRound(ctx context.Context, game model.GameID) (model.Round, error)
	LastRound(ctx context.Context, game model.GameID) (model.Round, error)
	RoundByNo(ctx context.Context, game model.GameID, no int) (model.Round, error)
	RecentNarrations(ctx context.Context, game model.GameID, limit int) ([]model.Narration, error)
	UpsertSubmission(ctx context.Context, s model.Submission) (model.Submission, error)
	GetSubmission(ctx context.Context, game model.GameID, round model.RoundID, character model.CharacterID) (model.Submission, error)
	ListSubmissions(ctx context.Context, game model.GameID, round model.RoundID) ([]model.Submission, error)
	MoveSubmissions(ctx context.Context, game model.GameID, from, to model.RoundID) error
	SetSubmissionsStatus(ctx context.Context, game model.GameID, round model.RoundID, status model.SubmissionStatus) error
	EnqueueOutbox(ctx context.Context, m model.OutboxMessage) error
}

// LLM — вызов модели с учётом в `llm_runs`; реализует `app/llm.Service`. Пусто — заявки разбираются кодом, хроника шаблоном.
type LLM interface {
	Complete(ctx context.Context, call applm.Call, req model.LLMRequest) (model.LLMResponse, error)
}

// Rights — кто может управлять игрой; реализует `app/game.CanAdmin`.
type Rights interface {
	CanAdmin(ctx context.Context, game model.Game, user model.UserID) (bool, error)
}

// Clock — источник времени.
type Clock interface {
	Now() time.Time
}

// Ошибки сценариев; текст пригоден для ответа игроку.
var (
	ErrForbidden      = errors.New("нет прав: действие доступно администратору игры, владельцу или модератору чата")
	ErrAlreadyStarted = errors.New("игра уже запущена")
	ErrNotActive      = errors.New("игра не запущена")
	ErrPaused         = errors.New("игра на паузе")
	ErrFinished       = errors.New("игра завершена")
	ErrNoCharacters   = errors.New("в игре ещё нет персонажей: игроки входят по ссылке и создают персонажей")
	ErrNoOpenRound    = errors.New("сейчас нет открытого раунда")
	ErrEmpty          = errors.New("текст заявки пуст")
	ErrTooLong        = fmt.Errorf("заявка до %d символов", model.SubmissionMaxLen)
	ErrTooFrequent    = errors.New("заменить заявку можно не чаще раза в минуту")
	ErrDead           = errors.New("персонаж выбыл из игры до возвращения; заявки не принимаются")
	ErrNoRound        = errors.New("раунда с таким номером нет")
	ErrNotResolved    = errors.New("раунд ещё не разрешён")
	ErrAlreadyActed   = errors.New("заявка на этот раунд уже принята, изменить её нельзя")
)

// Service — сценарии раунда.
type Service struct {
	store  Store
	rights Rights
	clock  Clock
	logger *slog.Logger
	// AnnounceSkipped — слать в группу сообщение о пропуске раунда без кворума; `cmd` включает в dev.
	AnnounceSkipped bool
	// LLM — интерпретатор заявок (вызов 1 из 2, пункт 08) и рассказчик (вызов 2 из 2, пункт 09); nil — разбор типовых
	// команд кодом и шаблонная хроника.
	LLM  LLM
	seed func() int64
}

// New собирает сервис раунда.
func New(store Store, rights Rights, clock Clock, logger *slog.Logger) *Service {
	return &Service{store: store, rights: rights, clock: clock, logger: logger, seed: rand.Int64}
}

// Begin запускает кампанию: статус `active`, стартовая сцена из шаблона жанра в outbox, первый раунд
// (сразу внутри игрового окна, иначе в его начале). Нужен хотя бы один персонаж.
func (s *Service) Begin(ctx context.Context, game model.Game, actor model.UserID) (model.Game, error) {
	if err := s.canAdmin(ctx, game, actor); err != nil {
		return model.Game{}, err
	}
	switch game.Status {
	case model.GameDraft:
	case model.GameFinished:
		return model.Game{}, ErrFinished
	default:
		return model.Game{}, ErrAlreadyStarted
	}
	now := s.clock.Now()
	var out model.Game
	err := s.store.InTx(ctx, func(ctx context.Context) error {
		g, err := s.store.LockGame(ctx, game.ID)
		if err != nil {
			return err
		}
		if g.Status != model.GameDraft {
			return ErrAlreadyStarted
		}
		season, err := s.store.ActiveSeason(ctx, g.ID)
		if err != nil {
			return err
		}
		chars, err := s.store.ListCharacters(ctx, g.ID, season.ID)
		if err != nil {
			return err
		}
		if len(chars) == 0 {
			return ErrNoCharacters
		}
		genre, err := s.store.GetGenre(ctx, g.GenreID)
		if err != nil {
			return err
		}
		factions, err := s.store.ListFactions(ctx, g.ID)
		if err != nil {
			return err
		}
		chat, err := s.store.GetChat(ctx, g.ChatID)
		if err != nil {
			return err
		}
		g.Status = model.GameActive
		if err := s.enqueue(ctx, g, chat, nil, model.OutboxScene, openingScene(g, genre.Template.OpeningScene, factions)); err != nil {
			return err
		}
		opened, err := s.openNext(ctx, &g, chat, now, false)
		if err != nil {
			return err
		}
		if !opened && g.NextRoundAt != nil {
			if err := s.enqueue(ctx, g, chat, nil, model.OutboxSystem, firstRoundLaterText(g, *g.NextRoundAt, now)); err != nil {
				return err
			}
		}
		out = g
		return nil
	})
	if err != nil {
		return model.Game{}, wrap("round.Begin", err)
	}
	s.logger.Info("game started", slog.Int64("game_id", int64(out.ID)))
	return out, nil
}

// Pause переключает паузу: `active` → `paused` (раунды не открываются, открытый раунд ждёт),
// `paused` → `active` (открытый раунд получает новый дедлайн, иначе следующий раунд по окну). Возвращает игру и признак паузы.
func (s *Service) Pause(ctx context.Context, game model.Game, actor model.UserID) (model.Game, bool, error) {
	if err := s.canAdmin(ctx, game, actor); err != nil {
		return model.Game{}, false, err
	}
	now := s.clock.Now()
	var out model.Game
	err := s.store.InTx(ctx, func(ctx context.Context) error {
		g, err := s.store.LockGame(ctx, game.ID)
		if err != nil {
			return err
		}
		chat, err := s.store.GetChat(ctx, g.ChatID)
		if err != nil {
			return err
		}
		switch g.Status {
		case model.GameActive:
			g.Status = model.GamePaused
			g.NextRoundAt = nil
			if err := s.store.UpdateGame(ctx, g); err != nil {
				return err
			}
			if err := s.enqueue(ctx, g, chat, nil, model.OutboxSystem, pausedText(g)); err != nil {
				return err
			}
		case model.GamePaused:
			g.Status = model.GameActive
			open, err := s.store.OpenRound(ctx, g.ID)
			switch {
			case err == nil:
				open.DeadlineAt = now.Add(time.Duration(g.RoundMinutes) * time.Minute)
				if err := s.store.UpdateRound(ctx, open); err != nil {
					return err
				}
				g.NextRoundAt = &open.DeadlineAt
				if err := s.store.UpdateGame(ctx, g); err != nil {
					return err
				}
				if err := s.enqueue(ctx, g, chat, nil, model.OutboxSystem, resumedText(g, &open, nil, now)); err != nil {
					return err
				}
			case errors.Is(err, model.ErrNotFound):
				opened, err := s.openNext(ctx, &g, chat, now, false)
				if err != nil {
					return err
				}
				if !opened {
					if err := s.enqueue(ctx, g, chat, nil, model.OutboxSystem, resumedText(g, nil, g.NextRoundAt, now)); err != nil {
						return err
					}
				}
			default:
				return err
			}
		case model.GameFinished:
			return ErrFinished
		default:
			return ErrNotActive
		}
		out = g
		return nil
	})
	if err != nil {
		return model.Game{}, false, wrap("round.Pause", err)
	}
	return out, out.Status == model.GamePaused, nil
}

// Next — ручной шаг для тестов (dev): закрыть открытый раунд сейчас и открыть следующий независимо от окна.
func (s *Service) Next(ctx context.Context, game model.Game, actor model.UserID) (model.Game, error) {
	if err := s.canAdmin(ctx, game, actor); err != nil {
		return model.Game{}, err
	}
	now := s.clock.Now()
	var out model.Game
	err := s.store.InTx(ctx, func(ctx context.Context) error {
		g, err := s.store.LockGame(ctx, game.ID)
		if err != nil {
			return err
		}
		if g.Status != model.GameActive {
			return statusError(g.Status)
		}
		if open, err := s.store.OpenRound(ctx, g.ID); err == nil {
			open.DeadlineAt = now
			if err := s.store.UpdateRound(ctx, open); err != nil {
				return err
			}
		} else if !errors.Is(err, model.ErrNotFound) {
			return err
		}
		if err := s.advance(ctx, &g, now, true); err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return model.Game{}, wrap("round.Next", err)
	}
	return out, nil
}

// Submit создаёт заявку персонажа на открытый раунд. Повторная заявка отклоняется; при `settings.allow_resubmit`
// заменяет текст не чаще раза в минуту.
func (s *Service) Submit(ctx context.Context, game model.Game, c model.Character, text string) (model.Round, model.Submission, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return model.Round{}, model.Submission{}, ErrEmpty
	}
	if len([]rune(text)) > model.SubmissionMaxLen {
		return model.Round{}, model.Submission{}, ErrTooLong
	}
	if game.Status != model.GameActive {
		return model.Round{}, model.Submission{}, statusError(game.Status)
	}
	if c.Status == model.CharacterDead {
		return model.Round{}, model.Submission{}, fmt.Errorf("%w (ещё %d раундов)", ErrDead, c.DeathRoundsLeft)
	}
	now := s.clock.Now()
	r, err := s.store.OpenRound(ctx, game.ID)
	if errors.Is(err, model.ErrNotFound) || (err == nil && !r.DeadlineAt.After(now)) {
		return model.Round{}, model.Submission{}, ErrNoOpenRound
	}
	if err != nil {
		return model.Round{}, model.Submission{}, fmt.Errorf("round.Submit: %w", err)
	}
	if ex, err := s.store.GetSubmission(ctx, game.ID, r.ID, c.ID); err == nil {
		if !game.Settings.AllowResubmit {
			return model.Round{}, model.Submission{}, ErrAlreadyActed
		}
		if now.Sub(ex.UpdatedAt) < model.SubmissionReplaceWindow {
			return model.Round{}, model.Submission{}, ErrTooFrequent
		}
	} else if !errors.Is(err, model.ErrNotFound) {
		return model.Round{}, model.Submission{}, fmt.Errorf("round.Submit: %w", err)
	}
	sub, err := s.store.UpsertSubmission(ctx, model.Submission{GameID: game.ID, RoundID: r.ID, CharacterID: c.ID, RawText: text, UpdatedAt: now})
	if err != nil {
		return model.Round{}, model.Submission{}, fmt.Errorf("round.Submit: %w", err)
	}
	s.logger.Info("submission saved", slog.Int64("game_id", int64(game.ID)), slog.Int64("round_id", int64(r.ID)), slog.Int64("character_id", int64(c.ID)))
	return r, sub, nil
}

// Info — раунды для /status.
type Info struct {
	Open        *model.Round // открытый раунд
	Last        *model.Round // последний закрытый раунд (frozen или skipped)
	Submissions int          // заявок в открытом раунде
}

// Info собирает сведения о раундах игры.
func (s *Service) Info(ctx context.Context, game model.Game) (Info, error) {
	var info Info
	open, err := s.store.OpenRound(ctx, game.ID)
	switch {
	case err == nil:
		info.Open = &open
		subs, err := s.store.ListSubmissions(ctx, game.ID, open.ID)
		if err != nil {
			return Info{}, fmt.Errorf("round.Info: %w", err)
		}
		info.Submissions = len(subs)
	case !errors.Is(err, model.ErrNotFound):
		return Info{}, fmt.Errorf("round.Info: %w", err)
	}
	last, err := s.store.LastRound(ctx, game.ID)
	if errors.Is(err, model.ErrNotFound) {
		return info, nil
	}
	if err != nil {
		return Info{}, fmt.Errorf("round.Info: %w", err)
	}
	if last.Status == model.RoundOpen {
		if last.RoundNo == 1 {
			return info, nil
		}
		if last, err = s.store.RoundByNo(ctx, game.ID, last.RoundNo-1); err != nil {
			return Info{}, fmt.Errorf("round.Info: %w", err)
		}
	}
	info.Last = &last
	return info, nil
}

// Tick — шаг планировщика: закрыть раунды с истёкшим дедлайном и открыть следующие у игр, чьё `next_round_at` наступило.
// Каждая игра в своей транзакции под блокировкой строки; ошибка одной игры не останавливает остальные.
func (s *Service) Tick(ctx context.Context, now time.Time) error {
	ids, err := s.store.DueGameIDs(ctx, now)
	if err != nil {
		return fmt.Errorf("round.Tick: %w", err)
	}
	var errs []error
	for _, id := range ids {
		err := s.store.InTx(ctx, func(ctx context.Context) error {
			g, err := s.store.LockGame(ctx, id)
			if errors.Is(err, model.ErrNotFound) {
				return nil // занята другим воркером
			}
			if err != nil {
				return err
			}
			if g.Status != model.GameActive || g.NextRoundAt == nil || g.NextRoundAt.After(now) {
				return nil
			}
			return s.advance(ctx, &g, now, false)
		})
		if err != nil {
			s.logger.Error("round tick failed", slog.Int64("game_id", int64(id)), slog.Any("err", err))
			errs = append(errs, fmt.Errorf("game %d: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// advance закрывает открытый раунд, если дедлайн прошёл, и открывает следующий; force игнорирует игровое окно.
func (s *Service) advance(ctx context.Context, g *model.Game, now time.Time, force bool) error {
	chat, err := s.store.GetChat(ctx, g.ChatID)
	if err != nil {
		return err
	}
	open, err := s.store.OpenRound(ctx, g.ID)
	switch {
	case err == nil:
		if open.DeadlineAt.After(now) {
			g.NextRoundAt = &open.DeadlineAt
			return s.store.UpdateGame(ctx, *g)
		}
		if err := s.close(ctx, *g, chat, open, now); err != nil {
			return err
		}
	case !errors.Is(err, model.ErrNotFound):
		return err
	}
	_, err = s.openNext(ctx, g, chat, now, force)
	return err
}

// close замораживает раунд (снимок, seed, статусы персонажей по пропускам) или пропускает его без кворума.
func (s *Service) close(ctx context.Context, g model.Game, chat model.Chat, r model.Round, now time.Time) error {
	subs, err := s.store.ListSubmissions(ctx, g.ID, r.ID)
	if err != nil {
		return err
	}
	r.ClosedAt = &now
	quorum := g.Settings.MinRoundAnswers
	if quorum < 1 {
		quorum = 1
	}
	if len(subs) < quorum {
		r.Status = model.RoundSkipped
		r.SkipReason = fmt.Sprintf("заявок %d из %d", len(subs), quorum)
		if err := s.store.UpdateRound(ctx, r); err != nil {
			return err
		}
		s.logger.Info("round skipped", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.String("reason", r.SkipReason))
		if !s.AnnounceSkipped {
			return nil
		}
		return s.enqueue(ctx, g, chat, &r.ID, model.OutboxRoundClosed, roundSkippedText(g, r))
	}
	chars, err := s.store.ListCharacters(ctx, g.ID, r.SeasonID)
	if err != nil {
		return err
	}
	acted := map[model.CharacterID]bool{}
	for _, sub := range subs {
		acted[sub.CharacterID] = true
	}
	inactiveAfter := g.Settings.InactiveAfterRounds
	if inactiveAfter < 1 {
		inactiveAfter = model.DefaultGameSettings().InactiveAfterRounds
	}
	for i := range chars {
		c := &chars[i]
		if c.Status == model.CharacterDead {
			// Ненастоящая смерть: счётчик возврата уменьшается каждым замороженным раундом; возврат — событие движка.
			if c.DeathRoundsLeft > 0 {
				c.DeathRoundsLeft--
				if err := s.store.SaveCharacterState(ctx, g.ID, *c); err != nil {
					return err
				}
			}
			continue
		}
		status, missed := activity(*c, acted[c.ID], inactiveAfter)
		if status != c.Status || missed != c.MissedRounds {
			c.Status, c.MissedRounds = status, missed
			if err := s.store.UpdateCharacterActivity(ctx, g.ID, c.ID, status, missed); err != nil {
				return err
			}
		}
	}
	locations, err := s.store.ListLocations(ctx, g.ID)
	if err != nil {
		return err
	}
	genre, err := s.store.GetGenre(ctx, g.GenreID)
	if err != nil {
		return err
	}
	cm, err := s.store.GetCharacterModel(ctx, genre.CharacterModelID)
	if err != nil {
		return err
	}
	snap := snapshot(g, chars, locations, subs, cm.Definition)
	r.Status = model.RoundFrozen
	r.Seed = s.seed()
	r.Snapshot = &snap
	if err := s.store.UpdateRound(ctx, r); err != nil {
		return err
	}
	if err := s.store.SetSubmissionsStatus(ctx, g.ID, r.ID, model.SubmissionFrozen); err != nil {
		return err
	}
	s.logger.Info("round frozen", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.Int("submissions", len(subs)))
	return s.resolve(ctx, g, chat, r, genre, cm.Definition, chars, now)
}

// activity — статус персонажа после раунда: заявка возвращает в `active`, пропуски ведут в `waiting`, затем `inactive`
// (docs/game/character.md, «Статус»). События `character_sidelined` / `character_returned` — пункт 07.
func activity(c model.Character, acted bool, inactiveAfter int) (model.CharacterStatus, int) {
	if acted {
		return model.CharacterActive, 0
	}
	missed := c.MissedRounds + 1
	if missed >= inactiveAfter {
		return model.CharacterInactive, missed
	}
	return model.CharacterWaiting, missed
}

// openNext открывает следующий раунд, если момент внутри игрового окна (или force), иначе назначает `next_round_at`
// на начало окна и возвращает false. Заявки пропущенного раунда переносятся в новый.
func (s *Service) openNext(ctx context.Context, g *model.Game, chat model.Chat, now time.Time, force bool) (bool, error) {
	if !force && !inWindow(g.Timezone, g.PlayFrom, g.PlayTo, now) {
		next := nextWindowStart(g.Timezone, g.PlayFrom, now)
		g.NextRoundAt = &next
		return false, s.store.UpdateGame(ctx, *g)
	}
	season, err := s.store.ActiveSeason(ctx, g.ID)
	if err != nil {
		return false, err
	}
	last, err := s.store.LastRound(ctx, g.ID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return false, err
	}
	r, err := s.store.CreateRound(ctx, model.Round{
		GameID: g.ID, SeasonID: season.ID, RoundNo: last.RoundNo + 1, Status: model.RoundOpen,
		OpensAt: now, DeadlineAt: now.Add(time.Duration(g.RoundMinutes) * time.Minute),
	})
	if err != nil {
		return false, err
	}
	carried := 0
	if last.ID != 0 && last.Status == model.RoundSkipped {
		pending, err := s.store.ListSubmissions(ctx, g.ID, last.ID)
		if err != nil {
			return false, err
		}
		carried = len(pending)
		if err := s.store.MoveSubmissions(ctx, g.ID, last.ID, r.ID); err != nil {
			return false, err
		}
	}
	g.NextRoundAt = &r.DeadlineAt
	if err := s.store.UpdateGame(ctx, *g); err != nil {
		return false, err
	}
	s.logger.Info("round opened", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.Int("round_no", r.RoundNo))
	return true, s.enqueue(ctx, *g, chat, &r.ID, model.OutboxRoundOpen, roundOpenText(*g, r, carried, now))
}

func (s *Service) enqueue(ctx context.Context, g model.Game, chat model.Chat, round *model.RoundID, kind model.OutboxKind, text string) error {
	return s.store.EnqueueOutbox(ctx, model.OutboxMessage{GameID: g.ID, RoundID: round, TelegramChatID: chat.TelegramChatID, Kind: kind, Part: 1, Text: text, SendAfter: s.clock.Now()})
}

func (s *Service) canAdmin(ctx context.Context, game model.Game, actor model.UserID) error {
	ok, err := s.rights.CanAdmin(ctx, game, actor)
	if err != nil {
		return fmt.Errorf("round: rights: %w", err)
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

func statusError(st model.GameStatus) error {
	switch st {
	case model.GamePaused:
		return ErrPaused
	case model.GameFinished:
		return ErrFinished
	}
	return ErrNotActive
}

// wrap добавляет префикс только к неожиданным ошибкам; ошибки сценария возвращаются как есть.
func wrap(op string, err error) error {
	for _, ue := range []error{ErrForbidden, ErrAlreadyStarted, ErrNotActive, ErrPaused, ErrFinished, ErrNoCharacters, ErrNoOpenRound, ErrAlreadyActed, ErrNoRound, ErrNotResolved} {
		if errors.Is(err, ue) {
			return err
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}
