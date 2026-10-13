// Package game — сценарии управления игрой: создание мира, настройки, вход игроков, персонажи.
package game

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"taletogether/internal/model"
)

// Store — хранилище игры; реализует `adapter/postgres`. Транзакция передаётся через контекст (ADR-0008).
type Store interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
	GetGenre(ctx context.Context, id model.GenreID) (model.Genre, error)
	GetGenreByCode(ctx context.Context, code string) (model.Genre, error)
	ListGenres(ctx context.Context) ([]model.Genre, error)
	GetCharacterModel(ctx context.Context, id model.CharacterModelID) (model.CharacterModel, error)
	CreateGame(ctx context.Context, g model.Game) (model.Game, error)
	UpdateGame(ctx context.Context, g model.Game) error
	GetGame(ctx context.Context, id model.GameID) (model.Game, error)
	GetGameByJoinCode(ctx context.Context, code string) (model.Game, error)
	UnfinishedGameOfChat(ctx context.Context, chat model.ChatID) (model.Game, error)
	GamesOfUser(ctx context.Context, user model.UserID) ([]model.Game, error)
	CreateSeason(ctx context.Context, s model.Season) (model.Season, error)
	ActiveSeason(ctx context.Context, game model.GameID) (model.Season, error)
	CreateFaction(ctx context.Context, f model.Faction) (model.Faction, error)
	UpdateFaction(ctx context.Context, f model.Faction) error
	ListFactions(ctx context.Context, game model.GameID) ([]model.Faction, error)
	ListQuests(ctx context.Context, game model.GameID) ([]model.Quest, error)
	ListFactionGoals(ctx context.Context, game model.GameID) ([]model.FactionGoal, error)
	AddMember(ctx context.Context, m model.GameMember) error
	GetMember(ctx context.Context, game model.GameID, user model.UserID) (model.GameMember, error)
	ListMembers(ctx context.Context, game model.GameID) ([]model.MemberInfo, error)
	CreateCharacter(ctx context.Context, c model.Character) (model.Character, error)
	GetCharacter(ctx context.Context, game model.GameID, season model.SeasonID, user model.UserID) (model.Character, error)
	ListCharacters(ctx context.Context, game model.GameID, season model.SeasonID) ([]model.Character, error)
	UpdateCharacterBio(ctx context.Context, game model.GameID, id model.CharacterID, public, secret string) error
	ListParties(ctx context.Context, game model.GameID, season model.SeasonID) ([]model.Party, error)
	CreateParty(ctx context.Context, p model.Party) (model.Party, error)
	UpdateParty(ctx context.Context, p model.Party) error
	SetCharacterParty(ctx context.Context, game model.GameID, id model.CharacterID, party *model.PartyID) error
	NicknameTaken(ctx context.Context, game model.GameID, season model.SeasonID, nickname string) (bool, error)
	GetChat(ctx context.Context, id model.ChatID) (model.Chat, error)
	EnqueueOutbox(ctx context.Context, m model.OutboxMessage) error
	SetCurrentGame(ctx context.Context, user model.UserID, game *model.GameID) error
	CreateLocation(ctx context.Context, l model.Location) (model.Location, error)
	ListLocations(ctx context.Context, game model.GameID) ([]model.Location, error)
}

// Rights — права в чате; реализует `app/chat`.
type Rights interface {
	CanManage(ctx context.Context, chat model.Chat, user model.UserID) (bool, error)
}

// Clock — источник времени.
type Clock interface {
	Now() time.Time
}

// Ошибки сценариев; текст пригоден для ответа игроку.
var (
	ErrForbidden     = errors.New("нет прав: действие доступно администратору игры, владельцу или модератору чата")
	ErrGameExists    = errors.New("в чате уже есть незавершённая игра")
	ErrNoGame        = errors.New("игры нет")
	ErrGameFinished  = errors.New("игра завершена")
	ErrNotMember     = errors.New("вы не участник этой игры")
	ErrHasCharacter  = errors.New("персонаж в этом сезоне уже есть")
	ErrNoCharacter   = errors.New("персонажа ещё нет")
	ErrNicknameTaken = errors.New("никнейм уже занят в этой игре")
	ErrChooseGame    = errors.New("вы участвуете в нескольких играх: выберите игру командой /games")
	ErrBadValue      = errors.New("недопустимое значение")
)

// Service — сценарии игры.
type Service struct {
	store   Store
	rights  Rights
	clock   Clock
	logger  *slog.Logger
	newCode func() string
	// Defaults — настройки нового мира; `cmd` меняет их по окружению (кворум 1 в dev).
	Defaults model.GameSettings
	// MinRoundMinutes — нижняя граница `round_minutes` для `/set`; в dev `cmd` опускает её до минуты.
	MinRoundMinutes int
}

// New собирает сервис игры.
func New(store Store, rights Rights, clock Clock, logger *slog.Logger) *Service {
	return &Service{store: store, rights: rights, clock: clock, logger: logger, newCode: joinCode, Defaults: model.DefaultGameSettings(), MinRoundMinutes: model.MinRoundMinutes}
}

// DefaultGenre — код жанра для новой игры, пока жанр не выбран в настройках.
const DefaultGenre = "fantasy"

// Create создаёт мир в чате: игра `draft` с настройками по умолчанию, сезон 1, фракции из шаблона жанра,
// автор — администратор игры; все записи в одной транзакции.
func (s *Service) Create(ctx context.Context, chat model.Chat, actor model.User) (model.Game, error) {
	ok, err := s.rights.CanManage(ctx, chat, actor.ID)
	if err != nil {
		return model.Game{}, fmt.Errorf("game.Create: %w", err)
	}
	if !ok {
		return model.Game{}, ErrForbidden
	}
	if _, err := s.store.UnfinishedGameOfChat(ctx, chat.ID); err == nil {
		return model.Game{}, ErrGameExists
	} else if !errors.Is(err, model.ErrNotFound) {
		return model.Game{}, fmt.Errorf("game.Create: %w", err)
	}
	genre, err := s.store.GetGenreByCode(ctx, DefaultGenre)
	if err != nil {
		return model.Game{}, fmt.Errorf("game.Create: genre: %w", err)
	}
	title := chat.Title
	if title == "" {
		title = "Общая история"
	}
	var game model.Game
	err = s.store.InTx(ctx, func(ctx context.Context) error {
		var err error
		game, err = s.store.CreateGame(ctx, model.Game{
			ChatID: chat.ID, GenreID: genre.ID, Format: model.FormatNovel, Mode: model.ModeBalanced, Title: title,
			Status: model.GameDraft, Timezone: "Europe/Moscow", RoundMinutes: 15, Settings: s.Defaults,
			JoinCode: s.newCode(), CreatedByUserID: actor.ID,
		})
		if err != nil {
			return err
		}
		if _, err := s.store.CreateSeason(ctx, model.Season{GameID: game.ID, SeasonNo: 1, Status: model.SeasonActive}); err != nil {
			return err
		}
		if err := s.createFactions(ctx, game.ID, genre.Template); err != nil {
			return err
		}
		if err := s.createLocations(ctx, game.ID, genre.Template); err != nil {
			return err
		}
		if err := s.store.AddMember(ctx, model.GameMember{GameID: game.ID, UserID: actor.ID, Role: model.RoleAdmin, Status: model.MemberActive}); err != nil {
			return err
		}
		return s.store.SetCurrentGame(ctx, actor.ID, &game.ID)
	})
	if err != nil {
		return model.Game{}, fmt.Errorf("game.Create: %w", err)
	}
	s.logger.Info("game created", slog.Int64("game_id", int64(game.ID)), slog.Int64("chat_id", int64(chat.ID)))
	return game, nil
}

func (s *Service) createFactions(ctx context.Context, game model.GameID, t model.GenreTemplate) error {
	for _, code := range model.FactionCodes {
		f := model.Faction{GameID: game, Code: code, Title: string(code), Relations: map[model.FactionCode]string{}}
		for _, ft := range t.Factions {
			if ft.Code != code {
				continue
			}
			f.Title, f.Description, f.Tone = ft.Title, ft.Description, ft.Tone
			for k, v := range ft.Relations {
				f.Relations[model.FactionCode(k)] = v
			}
		}
		if _, err := s.store.CreateFaction(ctx, f); err != nil {
			return err
		}
	}
	return nil
}

// createLocations создаёт стартовую клетку `(0,0)` из шаблона жанра (пункт 27: остальной мир достраивает рассказчик
// при `/begin` и по ходу игры); стартовой считается помеченная `start`, иначе первая локация шаблона.
func (s *Service) createLocations(ctx context.Context, game model.GameID, t model.GenreTemplate) error {
	if len(t.Locations) == 0 {
		return nil
	}
	lt := t.Locations[0]
	for _, candidate := range t.Locations {
		if candidate.Start {
			lt = candidate
		}
	}
	l := model.Location{GameID: game, Name: lt.Name, Description: lt.Description, Difficulty: lt.Difficulty, Terrain: lt.Terrain, Stage: model.StageDetailed, Objects: lt.Objects, Start: true}
	if _, err := s.store.CreateLocation(ctx, l); err != nil {
		return err
	}
	return nil
}

// Locations возвращает локации игры.
func (s *Service) Locations(ctx context.Context, game model.GameID) ([]model.Location, error) {
	l, err := s.store.ListLocations(ctx, game)
	if err != nil {
		return nil, fmt.Errorf("game.Locations: %w", err)
	}
	return l, nil
}

// CanAdmin сообщает, может ли пользователь менять игру: администратор игры либо владелец или модератор чата.
func (s *Service) CanAdmin(ctx context.Context, game model.Game, user model.UserID) (bool, error) {
	m, err := s.store.GetMember(ctx, game.ID, user)
	if err == nil && m.Role == model.RoleAdmin && m.Status == model.MemberActive {
		return true, nil
	}
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return false, fmt.Errorf("game.CanAdmin: %w", err)
	}
	chat, err := s.store.GetChat(ctx, game.ChatID)
	if err != nil {
		return false, fmt.Errorf("game.CanAdmin: chat: %w", err)
	}
	ok, err := s.rights.CanManage(ctx, chat, user)
	if err != nil {
		return false, fmt.Errorf("game.CanAdmin: %w", err)
	}
	return ok, nil
}

// Chat возвращает групповой чат игры.
func (s *Service) Chat(ctx context.Context, game model.Game) (model.Chat, error) {
	c, err := s.store.GetChat(ctx, game.ChatID)
	if err != nil {
		return model.Chat{}, fmt.Errorf("game.Chat: %w", err)
	}
	return c, nil
}

// Get возвращает игру по id.
func (s *Service) Get(ctx context.Context, id model.GameID) (model.Game, error) {
	g, err := s.store.GetGame(ctx, id)
	if errors.Is(err, model.ErrNotFound) {
		return model.Game{}, ErrNoGame
	}
	if err != nil {
		return model.Game{}, fmt.Errorf("game.Get: %w", err)
	}
	return g, nil
}

// OfChat возвращает незавершённую игру чата.
func (s *Service) OfChat(ctx context.Context, chat model.ChatID) (model.Game, error) {
	g, err := s.store.UnfinishedGameOfChat(ctx, chat)
	if errors.Is(err, model.ErrNotFound) {
		return model.Game{}, ErrNoGame
	}
	if err != nil {
		return model.Game{}, fmt.Errorf("game.OfChat: %w", err)
	}
	return g, nil
}

// ByCode возвращает игру по коду приглашения.
func (s *Service) ByCode(ctx context.Context, code string) (model.Game, error) {
	g, err := s.store.GetGameByJoinCode(ctx, code)
	if errors.Is(err, model.ErrNotFound) {
		return model.Game{}, ErrNoGame
	}
	if err != nil {
		return model.Game{}, fmt.Errorf("game.ByCode: %w", err)
	}
	return g, nil
}

// Current возвращает игру, к которой относятся команды пользователя в личке: выбранная через /games,
// иначе единственная незавершённая; при нескольких — ErrChooseGame.
func (s *Service) Current(ctx context.Context, user model.User) (model.Game, error) {
	games, err := s.store.GamesOfUser(ctx, user.ID)
	if err != nil {
		return model.Game{}, fmt.Errorf("game.Current: %w", err)
	}
	if user.CurrentGameID != nil {
		for _, g := range games {
			if g.ID == *user.CurrentGameID {
				return g, nil
			}
		}
	}
	switch len(games) {
	case 0:
		return model.Game{}, ErrNoGame
	case 1:
		return games[0], nil
	default:
		return model.Game{}, ErrChooseGame
	}
}

// Of возвращает незавершённые игры пользователя.
func (s *Service) Of(ctx context.Context, user model.UserID) ([]model.Game, error) {
	games, err := s.store.GamesOfUser(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("game.Of: %w", err)
	}
	return games, nil
}

// Select делает игру текущей для пользователя в личке.
func (s *Service) Select(ctx context.Context, user model.User, game model.Game) error {
	if _, err := s.store.GetMember(ctx, game.ID, user.ID); err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return ErrNotMember
		}
		return fmt.Errorf("game.Select: %w", err)
	}
	if err := s.store.SetCurrentGame(ctx, user.ID, &game.ID); err != nil {
		return fmt.Errorf("game.Select: %w", err)
	}
	return nil
}

// Join добавляет пользователя в игру по коду: участник `player`, игра становится текущей.
// Повторный вход не ошибка. Нужна открытая личка (users.started_at).
func (s *Service) Join(ctx context.Context, user model.User, code string) (model.Game, error) {
	game, err := s.ByCode(ctx, code)
	if err != nil {
		return model.Game{}, err
	}
	if game.Status == model.GameFinished {
		return model.Game{}, ErrGameFinished
	}
	if _, err := s.store.GetMember(ctx, game.ID, user.ID); err == nil {
		if err := s.store.SetCurrentGame(ctx, user.ID, &game.ID); err != nil {
			return model.Game{}, fmt.Errorf("game.Join: %w", err)
		}
		return game, nil
	} else if !errors.Is(err, model.ErrNotFound) {
		return model.Game{}, fmt.Errorf("game.Join: %w", err)
	}
	err = s.store.InTx(ctx, func(ctx context.Context) error {
		if err := s.store.AddMember(ctx, model.GameMember{GameID: game.ID, UserID: user.ID, Role: model.RolePlayer, Status: model.MemberActive}); err != nil {
			return err
		}
		return s.store.SetCurrentGame(ctx, user.ID, &game.ID)
	})
	if err != nil {
		return model.Game{}, fmt.Errorf("game.Join: %w", err)
	}
	s.logger.Info("member joined", slog.Int64("game_id", int64(game.ID)), slog.Int64("user_id", int64(user.ID)))
	return game, nil
}

// Status — сводка игры для /status.
type Status struct {
	Game       model.Game
	Genre      model.Genre
	Season     model.Season
	Factions   []model.Faction
	Members    []model.MemberInfo
	Characters []model.Character
	Chat       model.Chat
}

// Status собирает сводку игры.
func (s *Service) Status(ctx context.Context, game model.Game) (Status, error) {
	st := Status{Game: game}
	var err error
	if st.Genre, err = s.store.GetGenre(ctx, game.GenreID); err != nil {
		return Status{}, fmt.Errorf("game.Status: genre: %w", err)
	}
	if st.Season, err = s.store.ActiveSeason(ctx, game.ID); err != nil {
		return Status{}, fmt.Errorf("game.Status: season: %w", err)
	}
	if st.Factions, err = s.store.ListFactions(ctx, game.ID); err != nil {
		return Status{}, fmt.Errorf("game.Status: %w", err)
	}
	if st.Members, err = s.store.ListMembers(ctx, game.ID); err != nil {
		return Status{}, fmt.Errorf("game.Status: %w", err)
	}
	if st.Characters, err = s.store.ListCharacters(ctx, game.ID, st.Season.ID); err != nil {
		return Status{}, fmt.Errorf("game.Status: %w", err)
	}
	if st.Chat, err = s.store.GetChat(ctx, game.ChatID); err != nil {
		return Status{}, fmt.Errorf("game.Status: chat: %w", err)
	}
	return st, nil
}

// Quests возвращает квесты игры с этапами (пункт 28).
func (s *Service) Quests(ctx context.Context, game model.GameID) ([]model.Quest, error) {
	q, err := s.store.ListQuests(ctx, game)
	if err != nil {
		return nil, fmt.Errorf("game.Quests: %w", err)
	}
	return q, nil
}

// Goals возвращает текущие цели фракций игры (пункт 28).
func (s *Service) Goals(ctx context.Context, game model.GameID) ([]model.FactionGoal, error) {
	g, err := s.store.ListFactionGoals(ctx, game)
	if err != nil {
		return nil, fmt.Errorf("game.Goals: %w", err)
	}
	return g, nil
}

// Factions возвращает фракции игры.
func (s *Service) Factions(ctx context.Context, game model.GameID) ([]model.Faction, error) {
	f, err := s.store.ListFactions(ctx, game)
	if err != nil {
		return nil, fmt.Errorf("game.Factions: %w", err)
	}
	return f, nil
}

// Genre возвращает жанр игры.
func (s *Service) Genre(ctx context.Context, game model.Game) (model.Genre, error) {
	g, err := s.store.GetGenre(ctx, game.GenreID)
	if err != nil {
		return model.Genre{}, fmt.Errorf("game.Genre: %w", err)
	}
	return g, nil
}

// joinCode — код приглашения из 8 символов без похожих знаков.
func joinCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("game.joinCode: crypto/rand: " + err.Error())
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// Genres возвращает включённые жанры справочника.
func (s *Service) Genres(ctx context.Context) ([]model.Genre, error) {
	g, err := s.store.ListGenres(ctx)
	if err != nil {
		return nil, fmt.Errorf("game.Genres: %w", err)
	}
	return g, nil
}
