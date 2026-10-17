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
	"runtime/debug"
	"strings"
	"time"

	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
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
	CreateLocation(ctx context.Context, l model.Location) (model.Location, error)
	UpdateLocation(ctx context.Context, l model.Location) error
	CreateObjects(ctx context.Context, game model.GameID, loc model.LocationID, objects []model.LocationObject) error
	SaveObjectState(ctx context.Context, game model.GameID, loc model.LocationID, o model.LocationObject) error
	ReplaceObjects(ctx context.Context, game model.GameID, loc model.LocationID, objects []model.LocationObject) error
	AddDiscovery(ctx context.Context, game model.GameID, loc model.LocationID, faction model.FactionID, round model.RoundID, character model.CharacterID) (bool, error)
	ListDiscoveries(ctx context.Context, game model.GameID) (map[model.LocationID][]model.FactionID, error)
	ResetWorld(ctx context.Context, game model.GameID) error
	GetGenerationJob(ctx context.Context, game model.GameID, round *model.RoundID, role model.LLMRole, hash string) (model.GenerationJob, error)
	SaveGenerationJob(ctx context.Context, j model.GenerationJob) error
	CreateQuest(ctx context.Context, q model.Quest) (model.Quest, error)
	UpdateQuest(ctx context.Context, q model.Quest) error
	UpdateQuestStage(ctx context.Context, st model.QuestStage) error
	ListQuests(ctx context.Context, game model.GameID) ([]model.Quest, error)
	CreateFactionGoal(ctx context.Context, g model.FactionGoal) (model.FactionGoal, error)
	ListFactionGoals(ctx context.Context, game model.GameID) ([]model.FactionGoal, error)
	SetFactionExplored(ctx context.Context, game model.GameID, faction model.FactionID, n int) error
	SetFactionHint(ctx context.Context, game model.GameID, faction model.FactionID, round *int) error
	ListCooldowns(ctx context.Context, game model.GameID, owner model.CooldownOwner, ownerID int64) ([]model.Cooldown, error)
	SetCooldown(ctx context.Context, c model.Cooldown) error
	SaveCharacterState(ctx context.Context, game model.GameID, c model.Character) error
	SaveCharacterKnowledge(ctx context.Context, game model.GameID, id model.CharacterID, knowledge []string) error
	NextEventSeq(ctx context.Context, game model.GameID) (int64, error)
	AppendEvents(ctx context.Context, events []model.Event) error
	ListEvents(ctx context.Context, game model.GameID, round model.RoundID) ([]model.Event, error)
	CreateResolvedActions(ctx context.Context, game model.GameID, round model.RoundID, results []model.ActionResult) error
	ListResolvedActions(ctx context.Context, game model.GameID, round model.RoundID) ([]model.ActionResult, error)
	ListFactions(ctx context.Context, game model.GameID) ([]model.Faction, error)
	ListCharacters(ctx context.Context, game model.GameID, season model.SeasonID) ([]model.Character, error)
	ListParties(ctx context.Context, game model.GameID, season model.SeasonID) ([]model.Party, error)
	CreateParty(ctx context.Context, p model.Party) (model.Party, error)
	UpdateParty(ctx context.Context, p model.Party) error
	SetCharacterParty(ctx context.Context, game model.GameID, id model.CharacterID, party *model.PartyID) error
	ReservePartyIDs(ctx context.Context, n int) ([]model.PartyID, error)
	UpdateCharacterActivity(ctx context.Context, game model.GameID, id model.CharacterID, status model.CharacterStatus, missed int) error
	SetCharacterAway(ctx context.Context, game model.GameID, id model.CharacterID, away bool) error
	CreateRound(ctx context.Context, r model.Round) (model.Round, error)
	UpdateRound(ctx context.Context, r model.Round) error
	OpenRound(ctx context.Context, game model.GameID) (model.Round, error)
	LastRound(ctx context.Context, game model.GameID) (model.Round, error)
	RoundByNo(ctx context.Context, game model.GameID, no int) (model.Round, error)
	RecentNarrations(ctx context.Context, game model.GameID, limit int) ([]model.Narration, error)
	UpsertSubmission(ctx context.Context, s model.Submission) (model.Submission, error)
	GetSubmission(ctx context.Context, game model.GameID, round model.RoundID, character model.CharacterID) (model.Submission, error)
	ListSubmissions(ctx context.Context, game model.GameID, round model.RoundID) ([]model.Submission, error)
	DeleteSubmission(ctx context.Context, game model.GameID, round model.RoundID, character model.CharacterID) error
	MoveSubmissions(ctx context.Context, game model.GameID, from, to model.RoundID) error
	SetSubmissionsStatus(ctx context.Context, game model.GameID, round model.RoundID, status model.SubmissionStatus) error
	CreateSpeech(ctx context.Context, sp model.Speech) (model.Speech, error)
	GetSpeech(ctx context.Context, game model.GameID, round model.RoundID, character model.CharacterID) (model.Speech, error)
	ListSpeeches(ctx context.Context, game model.GameID, round model.RoundID) ([]model.Speech, error)
	MoveSpeeches(ctx context.Context, game model.GameID, from, to model.RoundID) error
	EnqueueOutbox(ctx context.Context, m model.OutboxMessage) error
	DeleteGameProgress(ctx context.Context, game model.GameID) error
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

// Journal — журнал чатов (ADR-0012): события раунда в папку группы игры; реализует `adapter/chatlog`.
type Journal interface {
	Write(ctx context.Context, rec model.ChatLogRecord)
}

// Ошибки сценариев; текст пригоден для ответа игроку.
var (
	ErrForbidden      = errors.New("нет прав: действие доступно администратору игры, владельцу или модератору чата")
	ErrAlreadyStarted = errors.New("игра уже запущена")
	ErrStarting       = errors.New("кампания уже запускается: стартовая сцена скоро придёт в группу")
	ErrFinishing      = errors.New("игра завершается: последний раунд и финальная хроника скоро придут в группу")
	ErrSetup          = errors.New("мир ещё не настроен: администратор завершает настройку в личке (/set)")
	ErrNotActive      = errors.New("игра не запущена")
	ErrPaused         = errors.New("игра на паузе")
	ErrFinished       = errors.New("игра завершена")
	ErrNoCharacters   = errors.New("в игре ещё нет персонажей: игроки входят по ссылке и создают персонажей")
	ErrNoOpenRound    = errors.New("сейчас нет открытого раунда")
	ErrEmpty          = errors.New("текст заявки пуст")
	ErrTooLong        = fmt.Errorf("заявка до %d символов", model.SubmissionMaxLen)
	ErrSpeechTooLong  = fmt.Errorf("реплика до %d символов", model.SpeechMaxLen)
	ErrTooFrequent    = errors.New("заменить заявку можно не чаще раза в минуту")
	ErrDead           = errors.New("персонаж выбыл из игры до возвращения; заявки не принимаются")
	ErrAlreadyAway    = errors.New("персонаж уже уходит из истории; вернуться — /back")
	ErrNotAway        = errors.New("персонаж в истории; действие в раунде — /act")
	ErrNoRound        = errors.New("раунда с таким номером нет")
	ErrNotResolved    = errors.New("раунд ещё не разрешён")
	ErrAlreadyActed   = errors.New("заявка на этот раунд уже принята, изменить её нельзя")
	ErrAlreadySaid    = errors.New("реплика на этот раунд уже принята, изменить её нельзя")
	ErrAbsent         = errors.New("персонаж вне истории; вернуть его может заявка /act")
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
	// Journal — журнал чатов; nil — выключен.
	Journal Journal
	// MaxAttempts — попыток закрыть раунд до статуса `failed` (ADR-0013); по умолчанию model.MaxRoundAttempts.
	MaxAttempts int
}

// journal пишет событие раунда в журнал группы игры с номером попытки; без журнала ничего не делает.
func (s *Service) journal(ctx context.Context, g model.Game, chat model.TelegramChatID, r model.Round, event string, data any) {
	if s.Journal == nil {
		return
	}
	s.Journal.Write(ctx, model.ChatLogRecord{TS: s.clock.Now(), Kind: model.ChatLogRound, Chat: chat, GameID: g.ID, RoundID: r.ID, RoundNo: r.RoundNo, Attempt: r.Attempts + 1, Event: event, Data: data})
}

// New собирает сервис раунда.
func New(store Store, rights Rights, clock Clock, logger *slog.Logger) *Service {
	return &Service{store: store, rights: rights, clock: clock, logger: logger, seed: rand.Int64, MaxAttempts: model.MaxRoundAttempts}
}

// Begin принимает запуск кампании (ADR-0024): короткая транзакция под блокировкой игры — проверка статуса и наличия
// персонажей, статус `starting`, `next_round_at = now`. Сам старт (`start`: завязка и стартовый пакет мира от
// рассказчика, сцена в outbox, первый раунд) делает ближайший тик планировщика, поэтому ответ на команду не ждёт модели,
// а перезапуск бота старт не теряет.
func (s *Service) Begin(ctx context.Context, game model.Game, actor model.UserID) (model.Game, error) {
	if err := s.canAdmin(ctx, game, actor); err != nil {
		return model.Game{}, err
	}
	if err := beginable(game.Status); err != nil {
		return model.Game{}, err
	}
	now := s.clock.Now()
	var out model.Game
	err := s.store.InTx(ctx, func(ctx context.Context) error {
		g, err := s.store.LockGame(ctx, game.ID)
		if err != nil {
			return err
		}
		if err := beginable(g.Status); err != nil {
			return err
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
		g.Status = model.GameStarting
		g.NextRoundAt = &now
		if err := s.store.UpdateGame(ctx, g); err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return model.Game{}, wrap("round.Begin", err)
	}
	s.logger.Info("game starting", slog.Int64("game_id", int64(out.ID)))
	return out, nil
}

// start запускает кампанию из `starting` в транзакции тика (ADR-0024): группа по умолчанию, завязка истории и
// стартовый пакет мира от рассказчика (`opening.go`, ADR-0018, ADR-0019; при сбое — стартовая сцена шаблона жанра и
// соседи «неизведанные земли») в outbox и у игры, статус `active`, первый раунд (сразу внутри игрового окна, иначе в
// его начале). Откат повторяется следующим тиком; ответ модели сохранён по ключу задания, второй сцены не будет.
func (s *Service) start(ctx context.Context, g *model.Game, now time.Time) error {
	season, err := s.store.ActiveSeason(ctx, g.ID)
	if err != nil {
		return err
	}
	chars, err := s.store.ListCharacters(ctx, g.ID, season.ID)
	if err != nil {
		return err
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
	if err := s.ensureDefaultParty(ctx, *g, season.ID, chars, genre); err != nil {
		return err
	}
	locs, err := s.store.ListLocations(ctx, g.ID)
	if err != nil {
		return err
	}
	if g.OpeningScene, err = s.opening(ctx, *g, chat, genre, factions, chars, locs); err != nil {
		return err
	}
	g.Status = model.GameActive
	g.SkippedStreak = 0
	if err := s.enqueue(ctx, *g, chat, nil, model.OutboxScene, openingScene(*g, g.OpeningScene)); err != nil {
		return err
	}
	opened, err := s.openNext(ctx, g, chat, now, false)
	if err != nil {
		return err
	}
	if !opened && g.NextRoundAt != nil {
		if err := s.enqueue(ctx, *g, chat, nil, model.OutboxSystem, firstRoundLaterText(*g, *g.NextRoundAt, now)); err != nil {
			return err
		}
		if err := s.notifyStartLater(ctx, *g, chars, *g.NextRoundAt); err != nil {
			return err
		}
	}
	s.logger.Info("game started", slog.Int64("game_id", int64(g.ID)))
	return nil
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
			s.journal(ctx, g, chat.TelegramChatID, model.Round{}, "paused", map[string]any{"reason": "manual"})
			if err := s.enqueue(ctx, g, chat, nil, model.OutboxSystem, pausedText()); err != nil {
				return err
			}
		case model.GamePaused:
			g.Status = model.GameActive
			g.SkippedStreak = 0 // после возобновления пропуски считаются заново (B11)
			s.journal(ctx, g, chat.TelegramChatID, model.Round{}, "resumed", nil)
			open, err := s.store.OpenRound(ctx, g.ID)
			switch {
			case err == nil:
				open.DeadlineAt = deadline(now, g.RoundMinutes)
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
		default:
			return statusError(g.Status)
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
// заменяет текст не чаще раза в минуту. Заявка ожидания `BackSubmission` (её ставит `/back`) заменяется настоящей
// заявкой того же раунда всегда (B51): вернувшийся игрок не теряет ход.
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
		switch {
		case ex.RawText == BackSubmission:
			// Заявка ожидания (её ставит `/back`, B51) — заглушка до настоящего хода: заменяется без ограничений.
		case !game.Settings.AllowResubmit:
			return model.Round{}, model.Submission{}, ErrAlreadyActed
		case now.Sub(ex.UpdatedAt) < model.SubmissionReplaceWindow:
			return model.Round{}, model.Submission{}, ErrTooFrequent
		}
	} else if !errors.Is(err, model.ErrNotFound) {
		return model.Round{}, model.Submission{}, fmt.Errorf("round.Submit: %w", err)
	}
	sub, err := s.store.UpsertSubmission(ctx, model.Submission{GameID: game.ID, RoundID: r.ID, CharacterID: c.ID, RawText: text, UpdatedAt: now})
	if err != nil {
		return model.Round{}, model.Submission{}, fmt.Errorf("round.Submit: %w", err)
	}
	if c.Away {
		// Любая заявка возвращает ушедшего по своему слову (ADR-0026).
		if err := s.store.SetCharacterAway(ctx, game.ID, c.ID, false); err != nil {
			return model.Round{}, model.Submission{}, fmt.Errorf("round.Submit: %w", err)
		}
	}
	s.logger.Info("submission saved", slog.Int64("game_id", int64(game.ID)), slog.Int64("round_id", int64(r.ID)), slog.Int64("character_id", int64(c.ID)))
	if s.Journal != nil {
		if chat, err := s.store.GetChat(ctx, game.ChatID); err == nil {
			s.journal(ctx, game, chat.TelegramChatID, r, "submission", map[string]any{"character_id": c.ID, "character": c.Nickname, "text": text})
		}
	}
	return r, sub, nil
}

// Say записывает реплику персонажа на открытый раунд (пункт 32, ADR-0027) по правилам заявки: текст до
// `SpeechMaxLen`, игра `active`, персонаж не выбыл, одна реплика на раунд. Реплика не заявка: кворум, пропуски и
// пометку `away` не затрагивает; выведенному (`inactive`) — `ErrAbsent`, вернуть его может только заявка.
func (s *Service) Say(ctx context.Context, game model.Game, c model.Character, text string) (model.Round, model.Speech, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return model.Round{}, model.Speech{}, ErrEmpty
	}
	if len([]rune(text)) > model.SpeechMaxLen {
		return model.Round{}, model.Speech{}, ErrSpeechTooLong
	}
	if game.Status != model.GameActive {
		return model.Round{}, model.Speech{}, statusError(game.Status)
	}
	switch c.Status {
	case model.CharacterDead:
		return model.Round{}, model.Speech{}, fmt.Errorf("%w (ещё %d раундов)", ErrDead, c.DeathRoundsLeft)
	case model.CharacterInactive:
		return model.Round{}, model.Speech{}, ErrAbsent
	}
	now := s.clock.Now()
	r, err := s.store.OpenRound(ctx, game.ID)
	if errors.Is(err, model.ErrNotFound) || (err == nil && !r.DeadlineAt.After(now)) {
		return model.Round{}, model.Speech{}, ErrNoOpenRound
	}
	if err != nil {
		return model.Round{}, model.Speech{}, fmt.Errorf("round.Say: %w", err)
	}
	sp, err := s.store.CreateSpeech(ctx, model.Speech{GameID: game.ID, RoundID: r.ID, CharacterID: c.ID, Text: text, SubmittedAt: now})
	if errors.Is(err, model.ErrAlreadyExists) {
		return model.Round{}, model.Speech{}, ErrAlreadySaid
	}
	if err != nil {
		return model.Round{}, model.Speech{}, fmt.Errorf("round.Say: %w", err)
	}
	s.logger.Info("speech saved", slog.Int64("game_id", int64(game.ID)), slog.Int64("round_id", int64(r.ID)), slog.Int64("character_id", int64(c.ID)))
	if s.Journal != nil {
		if chat, err := s.store.GetChat(ctx, game.ChatID); err == nil {
			s.journal(ctx, game, chat.TelegramChatID, r, "speech", map[string]any{"character_id": c.ID, "character": c.Nickname, "text": text})
		}
	}
	return r, sp, nil
}

// BackSubmission — текст заявки возвращения по `/back`: ожидание; возврат объясняет рассказчик по `entry = returned`.
// Заглушка до настоящего хода: `Submit` в том же раунде заменяет её (B51).
const BackSubmission = "жду"

// Away — уход персонажа по слову игрока (пункт 20, ADR-0026): пометка `away`, заявка открытого раунда удаляется
// (возвращает true, если была). Статус сменит заморозка: `leaving` на один раунд, затем `inactive`. Выбывший по смерти
// не уходит (`ErrDead`), повторный уход — `ErrAlreadyAway`; статус игры не проверяется — пометка ждёт заморозки.
func (s *Service) Away(ctx context.Context, game model.Game, c model.Character) (bool, error) {
	if c.Status == model.CharacterDead {
		return false, fmt.Errorf("%w (ещё %d раундов)", ErrDead, c.DeathRoundsLeft)
	}
	if c.Away {
		return false, ErrAlreadyAway
	}
	if err := s.store.SetCharacterAway(ctx, game.ID, c.ID, true); err != nil {
		return false, fmt.Errorf("round.Away: %w", err)
	}
	cancelled := false
	r, err := s.store.OpenRound(ctx, game.ID)
	if err == nil {
		if _, err := s.store.GetSubmission(ctx, game.ID, r.ID, c.ID); err == nil {
			if err := s.store.DeleteSubmission(ctx, game.ID, r.ID, c.ID); err != nil {
				return false, fmt.Errorf("round.Away: %w", err)
			}
			cancelled = true
		} else if !errors.Is(err, model.ErrNotFound) {
			return false, fmt.Errorf("round.Away: %w", err)
		}
	} else if !errors.Is(err, model.ErrNotFound) {
		return false, fmt.Errorf("round.Away: %w", err)
	}
	s.logger.Info("character away", slog.Int64("game_id", int64(game.ID)), slog.Int64("character_id", int64(c.ID)), slog.Bool("cancelled", cancelled))
	if s.Journal != nil {
		if chat, err := s.store.GetChat(ctx, game.ChatID); err == nil {
			s.journal(ctx, game, chat.TelegramChatID, r, "away", map[string]any{"character_id": c.ID, "character": c.Nickname, "cancelled": cancelled})
		}
	}
	return cancelled, nil
}

// Back — возвращение по `/back` (ADR-0026): пометка снимается; если персонаж уже выведен (`leaving` / `inactive`) и
// раунд открыт — заявка `BackSubmission`, возврат в `active` с `entry = returned` при заморозке. Без открытого раунда
// возвращает `ErrNoOpenRound` после снятия пометки (вернёт первая заявка); до заморозки — только отмена ухода
// (nil раунд). Персонаж в истории без пометки — `ErrNotAway`.
func (s *Service) Back(ctx context.Context, game model.Game, c model.Character) (*model.Round, error) {
	if c.Status == model.CharacterDead {
		return nil, fmt.Errorf("%w (ещё %d раундов)", ErrDead, c.DeathRoundsLeft)
	}
	gone := c.Status == model.CharacterLeaving || c.Status == model.CharacterInactive
	if !c.Away && !gone {
		return nil, ErrNotAway
	}
	if c.Away {
		if err := s.store.SetCharacterAway(ctx, game.ID, c.ID, false); err != nil {
			return nil, fmt.Errorf("round.Back: %w", err)
		}
		c.Away = false
	}
	if !gone {
		return nil, nil
	}
	r, _, err := s.Submit(ctx, game, c, BackSubmission)
	if err != nil {
		return nil, err
	}
	return &r, nil
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

// Tick — шаг планировщика: закрыть раунды с истёкшим дедлайном и открыть следующие у игр, чьё `next_round_at` наступило;
// запустить игры `starting` и завершить `finishing` (ADR-0024); у игр `setup` сделать нулевой вызов «генерация мира» (ADR-0028).
// Каждая игра в своей транзакции под блокировкой строки; ошибка или паника одной игры не останавливает остальные.
// Откат транзакции закрытия считается неудачной попыткой раунда (ADR-0013): после MaxAttempts раунд получает `failed`,
// заявки переносятся в следующий.
func (s *Service) Tick(ctx context.Context, now time.Time) error {
	ids, err := s.store.DueGameIDs(ctx, now)
	if err != nil {
		return fmt.Errorf("round.Tick: %w", err)
	}
	var errs []error
	for _, id := range ids {
		if err := s.tickGame(ctx, id, now); err != nil {
			s.logger.Error("round tick failed", slog.Int64("game_id", int64(id)), slog.Any("err", err))
			errs = append(errs, fmt.Errorf("game %d: %w", id, err))
			if noteErr := s.noteFailure(ctx, id, now, err); noteErr != nil {
				s.logger.Error("round failure not recorded", slog.Int64("game_id", int64(id)), slog.Any("err", noteErr))
			}
		}
	}
	return errors.Join(errs...)
}

// tickGame продвигает одну игру в транзакции; паника конвейера превращается в ошибку, транзакция откатывается.
func (s *Service) tickGame(ctx context.Context, id model.GameID, now time.Time) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	return s.store.InTx(ctx, func(ctx context.Context) error {
		g, err := s.store.LockGame(ctx, id)
		if errors.Is(err, model.ErrNotFound) {
			return nil // занята другим воркером
		}
		if err != nil {
			return err
		}
		if g.NextRoundAt == nil || g.NextRoundAt.After(now) {
			return nil
		}
		switch g.Status {
		case model.GameSetup:
			return s.worldgen(ctx, &g, now)
		case model.GameStarting:
			return s.start(ctx, &g, now)
		case model.GameFinishing:
			return s.finishNow(ctx, &g, now)
		case model.GameActive:
			return s.advance(ctx, &g, now, false)
		}
		return nil
	})
}

// noteFailure в отдельной транзакции увеличивает счётчик попыток открытого раунда с истёкшим дедлайном;
// при MaxAttempts раунд закрывается со статусом `failed`, группа получает сообщение, заявки переносятся в следующий раунд.
func (s *Service) noteFailure(ctx context.Context, id model.GameID, now time.Time, cause error) error {
	return s.store.InTx(ctx, func(ctx context.Context) error {
		g, err := s.store.LockGame(ctx, id)
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		open, err := s.store.OpenRound(ctx, g.ID)
		if errors.Is(err, model.ErrNotFound) || (err == nil && open.DeadlineAt.After(now)) {
			return nil // сбой не относится к закрытию раунда
		}
		if err != nil {
			return err
		}
		chat, err := s.store.GetChat(ctx, g.ChatID)
		if err != nil {
			return err
		}
		open.Attempts++
		if open.Attempts < s.MaxAttempts {
			if err := s.store.UpdateRound(ctx, open); err != nil {
				return err
			}
			s.journal(ctx, g, chat.TelegramChatID, open, "attempt_failed", map[string]any{"attempt": open.Attempts, "error": cause.Error()})
			return nil
		}
		open.Status = model.RoundFailed
		open.ClosedAt = &now
		open.SkipReason = "сбой разрешения: " + truncateText(cause.Error(), 200)
		if err := s.store.UpdateRound(ctx, open); err != nil {
			return err
		}
		s.logger.Error("round failed", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(open.ID)), slog.Int("attempts", open.Attempts))
		s.journal(ctx, g, chat.TelegramChatID, open, "failed", map[string]any{"attempts": open.Attempts, "error": cause.Error()})
		if err := s.enqueue(ctx, g, chat, &open.ID, model.OutboxRoundClosed, roundFailedText(g, open)); err != nil {
			return err
		}
		g.SkippedStreak = 0
		if g.Status == model.GameFinishing {
			// Завершение игры (ADR-0024): после неудачных попыток последний раунд остаётся `failed`, новый не открывается.
			return s.finish(ctx, &g, chat, now, open.RoundNo, "игру завершил администратор")
		}
		return s.afterClose(ctx, &g, chat, open.RoundNo, false)
	})
}

// advance закрывает открытый раунд, если дедлайн прошёл, и открывает следующий; force игнорирует игровое окно.
func (s *Service) advance(ctx context.Context, g *model.Game, now time.Time, force bool) error {
	chat, err := s.store.GetChat(ctx, g.ChatID)
	if err != nil {
		return err
	}
	closed := 0 // номер последнего закрытого раунда для границ рассказа
	open, err := s.store.OpenRound(ctx, g.ID)
	switch {
	case err == nil:
		if open.DeadlineAt.After(now) {
			g.NextRoundAt = &open.DeadlineAt
			return s.store.UpdateGame(ctx, *g)
		}
		skipped, err := s.close(ctx, *g, chat, open, now, quorum(*g))
		if err != nil {
			return err
		}
		if skipped {
			g.SkippedStreak++
		} else {
			g.SkippedStreak = 0
		}
		closed = open.RoundNo
	case errors.Is(err, model.ErrNotFound):
		last, err := s.store.LastRound(ctx, g.ID)
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return err
		}
		closed = last.RoundNo
	default:
		return err
	}
	return s.afterClose(ctx, g, chat, closed, force)
}

// afterClose открывает следующий раунд от текущего момента (игроки получают полный раунд после долгого разрешения),
// завершает игру по границе рассказа (`max_rounds`, `ends_at`) или ставит её на паузу после
// `settings.pause_after_skipped` пропущенных подряд раундов (B11).
func (s *Service) afterClose(ctx context.Context, g *model.Game, chat model.Chat, closed int, force bool) error {
	now := s.clock.Now()
	if reason := boundary(*g, closed, now); reason != "" {
		if err := s.announceSkipped(ctx, g, chat); err != nil {
			return err
		}
		return s.finish(ctx, g, chat, now, closed, reason)
	}
	if n := g.Settings.PauseAfterSkipped; n > 0 && g.SkippedStreak >= n {
		return s.pauseAfterSkipped(ctx, g, chat, closed)
	}
	_, err := s.openNext(ctx, g, chat, now, force)
	return err
}

// pauseAfterSkipped — автоматическая пауза (B11): статус `paused`, таймер снят, сообщение о пропуске (dev) и о паузе
// в группу, журнал `paused` с причиной `skipped_rounds`. Снимается как ручная: `/pause`, счётчик обнуляется.
func (s *Service) pauseAfterSkipped(ctx context.Context, g *model.Game, chat model.Chat, closed int) error {
	if err := s.announceSkipped(ctx, g, chat); err != nil {
		return err
	}
	g.Status = model.GamePaused
	g.NextRoundAt = nil
	if err := s.store.UpdateGame(ctx, *g); err != nil {
		return err
	}
	s.logger.Info("game paused after skipped rounds", slog.Int64("game_id", int64(g.ID)), slog.Int("skipped", g.SkippedStreak))
	s.journal(ctx, *g, chat.TelegramChatID, model.Round{RoundNo: closed}, "paused", map[string]any{"reason": "skipped_rounds", "skipped": g.SkippedStreak})
	return s.enqueue(ctx, *g, chat, nil, model.OutboxSystem, autoPausedText(g.SkippedStreak))
}

// skippedNote — сообщение о пропуске последнего раунда для группы; пусто вне dev (`AnnounceSkipped`) или если
// последний раунд не пропущен. Присоединяется к анонсу следующего раунда (замечание руководителя B4).
func (s *Service) skippedNote(g model.Game, last model.Round) string {
	if !s.AnnounceSkipped || last.ID == 0 || last.Status != model.RoundSkipped {
		return ""
	}
	return roundSkippedText(g, last)
}

// announceSkipped шлёт сообщение о пропуске отдельно, когда следующий раунд не открывается сразу (окно, завершение).
func (s *Service) announceSkipped(ctx context.Context, g *model.Game, chat model.Chat) error {
	last, err := s.store.LastRound(ctx, g.ID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return err
	}
	if note := s.skippedNote(*g, last); note != "" {
		return s.enqueue(ctx, *g, chat, &last.ID, model.OutboxRoundClosed, note)
	}
	return nil
}

// boundary — причина завершения игры по границам рассказа после раунда closed; пусто, если игра продолжается.
func boundary(g model.Game, closed int, now time.Time) string {
	if g.Settings.MaxRounds > 0 && closed >= g.Settings.MaxRounds {
		return "достигнут предел раундов" // число раундов печатает финал (B48)
	}
	if g.Settings.EndsAt != nil && !now.Before(*g.Settings.EndsAt) {
		return "наступила дата окончания " + g.Settings.EndsAt.In(location(g.Timezone)).Format("02.01.2006")
	}
	return ""
}

// quorum — минимум заявок для разрешения раунда, не меньше 1.
func quorum(g model.Game) int {
	if g.Settings.MinRoundAnswers < 1 {
		return 1
	}
	return g.Settings.MinRoundAnswers
}

// close замораживает раунд (снимок, seed, статусы персонажей по пропускам) или пропускает его без кворума
// (возвращает true); затем разрешает в той же транзакции. Длительность шагов попадает в метрики раунда.
func (s *Service) close(ctx context.Context, g model.Game, chat model.Chat, r model.Round, now time.Time, quorum int) (bool, error) {
	started := s.clock.Now()
	subs, err := s.store.ListSubmissions(ctx, g.ID, r.ID)
	if err != nil {
		return false, err
	}
	r.ClosedAt = &now
	if len(subs) < quorum {
		r.Status = model.RoundSkipped
		r.SkipReason = fmt.Sprintf("заявок %d из %d", len(subs), quorum)
		if err := s.store.UpdateRound(ctx, r); err != nil {
			return false, err
		}
		s.logger.Info("round skipped", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.String("reason", r.SkipReason))
		s.journal(ctx, g, chat.TelegramChatID, r, "skipped", map[string]any{"reason": r.SkipReason})
		return true, nil // сообщение о пропуске (dev) уходит вместе с анонсом следующего раунда, см. openNext
	}
	return false, s.freeze(ctx, g, chat, r, subs, now, started)
}

// freeze — заморозка раунда при кворуме: статусы персонажей, снимок, seed, затем разрешение.
func (s *Service) freeze(ctx context.Context, g model.Game, chat model.Chat, r model.Round, subs []model.Submission, now, started time.Time) error {
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
	seen, err := s.seenCharacters(ctx, g.ID, r.RoundNo)
	if err != nil {
		return err
	}
	entries := map[model.CharacterID]string{}
	for i := range chars {
		c := &chars[i]
		if !seen[c.ID] {
			entries[c.ID] = model.EntryNew
		} else if (c.Status == model.CharacterLeaving || c.Status == model.CharacterInactive) && acted[c.ID] {
			entries[c.ID] = model.EntryReturned
		}
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
	if err := s.ensureParties(ctx, g, r.SeasonID, chars, locations, genre); err != nil {
		return err
	}
	discoveries, err := s.store.ListDiscoveries(ctx, g.ID)
	if err != nil {
		return err
	}
	speeches, err := s.store.ListSpeeches(ctx, g.ID, r.ID)
	if err != nil {
		return err
	}
	factions, err := s.store.ListFactions(ctx, g.ID)
	if err != nil {
		return err
	}
	snap := snapshot(g, chars, locations, discoveries, subs, speeches, cm.Definition)
	snap.Hints = snapshotHints(factions)
	for i := range snap.Characters {
		snap.Characters[i].Entry = entries[snap.Characters[i].ID]
	}
	if err := s.snapshotParties(ctx, g, r.SeasonID, genre, &snap); err != nil {
		return err
	}
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
	s.journal(ctx, g, chat.TelegramChatID, r, "frozen", frozenData(r, snap.Characters, subs, speeches))
	m := &model.RoundMetrics{Attempt: r.Attempts + 1, Submissions: len(subs), FreezeMS: s.clock.Now().Sub(started).Milliseconds()}
	if err := s.resolve(ctx, g, chat, r, genre, cm.Definition, chars, now, m); err != nil {
		return err
	}
	if d := time.Duration(m.TotalMS) * time.Millisecond; d > time.Duration(g.RoundMinutes)*time.Minute {
		s.logger.Warn("round resolution exceeded round interval", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.Duration("took", d))
	}
	return nil
}

// seenCharacters — персонажи, уже бывшие в снимке какого-либо раунда до roundNo; остальные входят в историю впервые
// (B1). Пропущенные раунды снимка не имеют, поэтому ищется последний замороженный.
func (s *Service) seenCharacters(ctx context.Context, game model.GameID, roundNo int) (map[model.CharacterID]bool, error) {
	seen := map[model.CharacterID]bool{}
	for no := roundNo - 1; no >= 1; no-- {
		prev, err := s.store.RoundByNo(ctx, game, no)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if prev.Snapshot == nil {
			continue
		}
		for _, c := range prev.Snapshot.Characters {
			seen[c.ID] = true
		}
		return seen, nil
	}
	return seen, nil
}

// frozenData — содержимое записи журнала о заморозке: seed, заявки и реплики с никнеймами, статусы и отметки входа
// персонажей.
func frozenData(r model.Round, chars []model.SnapshotCharacter, subs []model.Submission, speeches []model.Speech) map[string]any {
	names := map[model.CharacterID]string{}
	statuses := make([]map[string]any, 0, len(chars))
	for _, c := range chars {
		names[c.ID] = c.Nickname
		statuses = append(statuses, map[string]any{"character_id": c.ID, "character": c.Nickname, "status": c.Status, "entry": c.Entry, "missed": c.MissedRounds, "away": c.Away, "location_id": c.LocationID, "party_id": c.PartyID})
	}
	list := make([]map[string]any, 0, len(subs))
	for _, sub := range subs {
		list = append(list, map[string]any{"submission_id": sub.ID, "character_id": sub.CharacterID, "character": names[sub.CharacterID], "text": sub.RawText})
	}
	said := make([]map[string]any, 0, len(speeches))
	for _, sp := range speeches {
		said = append(said, map[string]any{"speech_id": sp.ID, "character_id": sp.CharacterID, "character": names[sp.CharacterID], "text": sp.Text})
	}
	return map[string]any{"seed": r.Seed, "submissions": list, "speeches": said, "characters": statuses}
}

// activity — статус персонажа после раунда: заявка возвращает в `active`, пропуски ведут в `waiting`, на пороге
// `inactive_after_rounds` — один раунд `leaving` (рассказчик выводит персонажа из сюжета), дальше `inactive`
// (docs/game/character.md, «Статус»; B13). Уход по слову игрока (`away`, ADR-0026) ведёт туда же без ожидания порога:
// `leaving` сразу, затем `inactive`; пропуски считаются как обычно. Пропущенный без кворума раунд сюда не доходит,
// статусы не меняет.
func activity(c model.Character, acted bool, inactiveAfter int) (model.CharacterStatus, int) {
	if acted {
		return model.CharacterActive, 0
	}
	missed := c.MissedRounds + 1
	if c.Away {
		if c.Status == model.CharacterLeaving || c.Status == model.CharacterInactive {
			return model.CharacterInactive, missed
		}
		return model.CharacterLeaving, missed
	}
	switch {
	case missed > inactiveAfter:
		return model.CharacterInactive, missed
	case missed == inactiveAfter:
		return model.CharacterLeaving, missed
	}
	return model.CharacterWaiting, missed
}

// deadline — дедлайн раунда: `now` + длительность, округлённые вниз до целой минуты, чтобы раунд заканчивался
// в :00 секунд (B7), а следующий раунд, открытый через несколько секунд после разрешения предыдущего, держал
// ровный шаг в `round_minutes` между дедлайнами (решение руководителя 2026-10-11, B12). Планировщик с шагом 10 с
// закрывает раунд в пределах шага.
func deadline(now time.Time, minutes int) time.Time {
	return now.Add(time.Duration(minutes) * time.Minute).Truncate(time.Minute)
}

// openNext открывает следующий раунд, если момент внутри игрового окна (или force), иначе назначает `next_round_at`
// на начало окна и возвращает false. Заявки пропущенного раунда переносятся в новый; сообщение о пропуске (dev)
// идёт в одном сообщении с анонсом, а если раунд не открывается — отдельно.
func (s *Service) openNext(ctx context.Context, g *model.Game, chat model.Chat, now time.Time, force bool) (bool, error) {
	last, err := s.store.LastRound(ctx, g.ID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return false, err
	}
	if !force && !inWindow(g.Timezone, g.PlayFrom, g.PlayTo, now) {
		if note := s.skippedNote(*g, last); note != "" {
			if err := s.enqueue(ctx, *g, chat, &last.ID, model.OutboxRoundClosed, note); err != nil {
				return false, err
			}
		}
		next := nextWindowStart(g.Timezone, g.PlayFrom, now)
		g.NextRoundAt = &next
		return false, s.store.UpdateGame(ctx, *g)
	}
	season, err := s.store.ActiveSeason(ctx, g.ID)
	if err != nil {
		return false, err
	}
	r, err := s.store.CreateRound(ctx, model.Round{
		GameID: g.ID, SeasonID: season.ID, RoundNo: last.RoundNo + 1, Status: model.RoundOpen,
		OpensAt: now, DeadlineAt: deadline(now, g.RoundMinutes),
	})
	if err != nil {
		return false, err
	}
	carried := 0
	if last.ID != 0 && (last.Status == model.RoundSkipped || last.Status == model.RoundFailed) {
		pending, err := s.store.ListSubmissions(ctx, g.ID, last.ID)
		if err != nil {
			return false, err
		}
		carried = len(pending)
		if err := s.store.MoveSubmissions(ctx, g.ID, last.ID, r.ID); err != nil {
			return false, err
		}
		if err := s.store.MoveSpeeches(ctx, g.ID, last.ID, r.ID); err != nil {
			return false, err
		}
	}
	g.NextRoundAt = &r.DeadlineAt
	if err := s.store.UpdateGame(ctx, *g); err != nil {
		return false, err
	}
	s.logger.Info("round opened", slog.Int64("game_id", int64(g.ID)), slog.Int64("round_id", int64(r.ID)), slog.Int("round_no", r.RoundNo))
	s.journal(ctx, *g, chat.TelegramChatID, r, "opened", map[string]any{"deadline_at": r.DeadlineAt, "carried": carried})
	text := roundOpenText(*g, r, carried, now)
	if note := s.skippedNote(*g, last); note != "" {
		var b model.TextBuilder // одно сообщение: пропуск и открытие (B4)
		b.WriteString(note + "\n\n")
		b.Append(text)
		text = b.Build()
	}
	if err := s.enqueueFormatted(ctx, *g, chat, &r.ID, model.OutboxRoundOpen, text); err != nil {
		return false, err
	}
	return true, s.notifyOpen(ctx, *g, last, r, now)
}

// notifyOpen — личное «раунд N открыт» с вариантами по локации (B15) персонажам, которые не получили уведомление
// о заявке только что опубликованного раунда: без заявки в нём, после пропуска, на старте игры или после паузы.
// Выбывшие (`inactive`, `dead`), уходящие по своему слову (`away`, ADR-0026) и игроки без открытой лички пропускаются.
func (s *Service) notifyOpen(ctx context.Context, g model.Game, last, r model.Round, now time.Time) error {
	notified := map[model.CharacterID]bool{}
	if last.ID != 0 && last.Status == model.RoundPublished {
		actions, err := s.store.ListResolvedActions(ctx, g.ID, last.ID)
		if err != nil {
			return err
		}
		for _, a := range actions {
			notified[a.CharacterID] = true
		}
	}
	chars, err := s.store.ListCharacters(ctx, g.ID, r.SeasonID)
	if err != nil {
		return err
	}
	locations, err := s.store.ListLocations(ctx, g.ID)
	if err != nil {
		return err
	}
	def, err := s.characterDef(ctx, g)
	if err != nil {
		return err
	}
	state := snapshot(g, chars, locations, nil, nil, nil, def)
	opts := narrator.Options(&state, def, g.Mode)
	for _, c := range state.Characters {
		if notified[c.ID] || c.Status.Absent() || c.Away {
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
		text := roundOpenPersonalText(g, r, optionsFor(c, &state, opts, "Варианты для вас"), now)
		if err := s.store.EnqueueOutbox(ctx, model.OutboxMessage{GameID: g.ID, RoundID: &r.ID, TelegramChatID: user.PrivateChat(), Kind: model.OutboxPrivateOpen, Part: 1, Text: text.Text, Entities: text.Entities, Keyboard: optionsKeyboard(c, opts, r.RoundNo), SendAfter: s.clock.Now()}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) enqueue(ctx context.Context, g model.Game, chat model.Chat, round *model.RoundID, kind model.OutboxKind, text string) error {
	return s.enqueueFormatted(ctx, g, chat, round, kind, model.Plain(text))
}

// enqueueFormatted ставит в outbox одно сообщение с разметкой (ADR-0030).
func (s *Service) enqueueFormatted(ctx context.Context, g model.Game, chat model.Chat, round *model.RoundID, kind model.OutboxKind, text model.FormattedText) error {
	return s.store.EnqueueOutbox(ctx, model.OutboxMessage{GameID: g.ID, RoundID: round, TelegramChatID: chat.TelegramChatID, Kind: kind, Part: 1, Text: text.Text, Entities: text.Entities, SendAfter: s.clock.Now()})
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

// beginable — можно ли запустить кампанию из статуса: только `draft`; `setup` — мир ещё не настроен (ADR-0023).
func beginable(st model.GameStatus) error {
	switch st {
	case model.GameDraft:
		return nil
	case model.GameSetup, model.GameStarting, model.GameFinishing, model.GameFinished:
		return statusError(st)
	}
	return ErrAlreadyStarted
}

func statusError(st model.GameStatus) error {
	switch st {
	case model.GameSetup:
		return ErrSetup
	case model.GameStarting:
		return ErrStarting
	case model.GamePaused:
		return ErrPaused
	case model.GameFinishing:
		return ErrFinishing
	case model.GameFinished:
		return ErrFinished
	}
	return ErrNotActive
}

// wrap добавляет префикс только к неожиданным ошибкам; ошибки сценария возвращаются как есть.
func wrap(op string, err error) error {
	for _, ue := range []error{ErrForbidden, ErrAlreadyStarted, ErrStarting, ErrFinishing, ErrSetup, ErrNotActive, ErrPaused, ErrFinished, ErrNoCharacters, ErrNoOpenRound, ErrAlreadyActed, ErrAlreadySaid, ErrAbsent, ErrSpeechTooLong, ErrNoRound, ErrNotResolved} {
		if errors.Is(err, ue) {
			return err
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}
