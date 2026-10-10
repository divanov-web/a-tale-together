// Package fake — хранилище в памяти для тестов сценариев; повторяет контракты adapter/postgres.
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"taletogether/internal/model"
)

// Store — хранилище в памяти; безопасно для параллельных тестов внутри одного экземпляра.
type Store struct {
	mu      sync.Mutex
	now     func() time.Time
	updates map[int64]bool
	users   map[model.UserID]model.User
	chats   map[model.ChatID]model.Chat
	mods    map[model.ChatID][]moderator
	dialogs map[model.UserID]model.Dialog
	genres  map[model.GenreID]model.Genre
	cmodels map[model.CharacterModelID]model.CharacterModel
	games   map[model.GameID]model.Game
	seasons map[model.SeasonID]model.Season
	facts   map[model.FactionID]model.Faction
	members map[memberKey]model.GameMember
	chars   map[model.CharacterID]model.Character
	nextID  int64
	// Err, если задан, возвращается всеми методами: имитация сбоя БД.
	Err error
}

type moderator struct {
	user, by model.UserID
	at       time.Time
}

type memberKey struct {
	game model.GameID
	user model.UserID
}

// New создаёт пустое хранилище с жанром fantasy и базовой моделью персонажа.
func New(now func() time.Time) *Store {
	s := &Store{
		now: now, updates: map[int64]bool{}, users: map[model.UserID]model.User{}, chats: map[model.ChatID]model.Chat{},
		mods: map[model.ChatID][]moderator{}, dialogs: map[model.UserID]model.Dialog{}, genres: map[model.GenreID]model.Genre{},
		cmodels: map[model.CharacterModelID]model.CharacterModel{}, games: map[model.GameID]model.Game{}, seasons: map[model.SeasonID]model.Season{},
		facts: map[model.FactionID]model.Faction{}, members: map[memberKey]model.GameMember{}, chars: map[model.CharacterID]model.Character{},
	}
	cm := model.CharacterModel{ID: 1, Code: "basic_fantasy", Definition: BasicFantasy()}
	s.cmodels[cm.ID] = cm
	s.genres[1] = model.Genre{ID: 1, Code: "fantasy", Title: "Классическое фэнтези", CharacterModelID: cm.ID, Template: FantasyTemplate(), Enabled: true}
	s.nextID = 1
	return s
}

func (s *Store) id() int64 {
	s.nextID++
	return s.nextID
}

// InTx выполняет fn без реальной транзакции.
func (s *Store) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if s.Err != nil {
		return s.Err
	}
	return fn(ctx)
}

// MarkUpdate — см. postgres.Store.
func (s *Store) MarkUpdate(_ context.Context, id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return false, s.Err
	}
	if s.updates[id] {
		return false, nil
	}
	s.updates[id] = true
	return true, nil
}

// UpsertUser — см. postgres.Store.
func (s *Store) UpsertUser(_ context.Context, u model.TelegramUser) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return model.User{}, s.Err
	}
	for id, ex := range s.users {
		if ex.TelegramID == u.ID {
			ex.Username, ex.FirstName, ex.LanguageCode, ex.LastSeenAt = u.Username, u.FirstName, u.LanguageCode, s.now()
			s.users[id] = ex
			return ex, nil
		}
	}
	user := model.User{ID: model.UserID(s.id()), TelegramID: u.ID, Username: u.Username, FirstName: u.FirstName, LanguageCode: u.LanguageCode, LastSeenAt: s.now()}
	s.users[user.ID] = user
	return user, nil
}

// GetUser — см. postgres.Store.
func (s *Store) GetUser(_ context.Context, id model.UserID) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return model.User{}, model.ErrNotFound
	}
	return u, nil
}

// GetUserByUsername — см. postgres.Store.
func (s *Store) GetUserByUsername(_ context.Context, username string) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if strings.EqualFold(u.Username, username) && u.Username != "" {
			return u, nil
		}
	}
	return model.User{}, model.ErrNotFound
}

// MarkStarted — см. postgres.Store.
func (s *Store) MarkStarted(_ context.Context, id model.UserID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[id]
	if u.StartedAt == nil {
		t := s.now()
		u.StartedAt = &t
		s.users[id] = u
	}
	return nil
}

// SetCurrentGame — см. postgres.Store.
func (s *Store) SetCurrentGame(_ context.Context, id model.UserID, game *model.GameID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[id]
	u.CurrentGameID = game
	s.users[id] = u
	return nil
}

// GetChatByTelegramID — см. postgres.Store.
func (s *Store) GetChatByTelegramID(_ context.Context, id model.TelegramChatID) (model.Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return model.Chat{}, s.Err
	}
	for _, c := range s.chats {
		if c.TelegramChatID == id {
			return c, nil
		}
	}
	return model.Chat{}, model.ErrNotFound
}

// GetChat — см. postgres.Store.
func (s *Store) GetChat(_ context.Context, id model.ChatID) (model.Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[id]
	if !ok {
		return model.Chat{}, model.ErrNotFound
	}
	return c, nil
}

// CreateChat — см. postgres.Store.
func (s *Store) CreateChat(_ context.Context, c model.Chat) (model.Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.chats {
		if ex.TelegramChatID == c.TelegramChatID {
			return ex, nil
		}
	}
	c.ID = model.ChatID(s.id())
	c.AddedAt = s.now()
	s.chats[c.ID] = c
	return c, nil
}

// UpdateChatStatus — см. postgres.Store.
func (s *Store) UpdateChatStatus(_ context.Context, id model.ChatID, status model.BotStatus, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chats[id]
	c.BotStatus, c.Title, c.LeftAt = status, title, nil
	if status == model.BotLeft {
		t := s.now()
		c.LeftAt = &t
	}
	s.chats[id] = c
	return nil
}

// SetChatOwner — см. postgres.Store.
func (s *Store) SetChatOwner(_ context.Context, id model.ChatID, owner model.UserID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chats[id]
	c.OwnerUserID = owner
	s.chats[id] = c
	return nil
}

// AddModerator — см. postgres.Store.
func (s *Store) AddModerator(_ context.Context, chat model.ChatID, user, by model.UserID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.mods[chat] {
		if m.user == user {
			return nil
		}
	}
	s.mods[chat] = append(s.mods[chat], moderator{user: user, by: by, at: s.now()})
	return nil
}

// RemoveModerator — см. postgres.Store.
func (s *Store) RemoveModerator(_ context.Context, chat model.ChatID, user model.UserID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.mods[chat][:0]
	for _, m := range s.mods[chat] {
		if m.user != user {
			list = append(list, m)
		}
	}
	s.mods[chat] = list
	return nil
}

// IsModerator — см. postgres.Store.
func (s *Store) IsModerator(_ context.Context, chat model.ChatID, user model.UserID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.mods[chat] {
		if m.user == user {
			return true, nil
		}
	}
	return false, nil
}

// ListModerators — см. postgres.Store.
func (s *Store) ListModerators(_ context.Context, chat model.ChatID) ([]model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.User
	for _, m := range s.mods[chat] {
		out = append(out, s.users[m.user])
	}
	return out, nil
}

// ChatsOfUser — см. postgres.Store.
func (s *Store) ChatsOfUser(_ context.Context, user model.UserID) ([]model.Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Chat
	for _, c := range s.chats {
		if c.BotStatus == model.BotLeft {
			continue
		}
		if c.OwnerUserID == user {
			out = append(out, c)
			continue
		}
		for _, m := range s.mods[c.ID] {
			if m.user == user {
				out = append(out, c)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GetDialog — см. postgres.Store.
func (s *Store) GetDialog(_ context.Context, user model.UserID) (model.Dialog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.dialogs[user]
	if !ok {
		return model.Dialog{}, model.ErrNotFound
	}
	return d, nil
}

// SaveDialog — см. postgres.Store.
func (s *Store) SaveDialog(_ context.Context, d model.Dialog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d.UpdatedAt = s.now()
	if len(d.Data) == 0 {
		d.Data = json.RawMessage("{}")
	}
	s.dialogs[d.UserID] = d
	return nil
}

// DeleteDialog — см. postgres.Store.
func (s *Store) DeleteDialog(_ context.Context, user model.UserID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.dialogs, user)
	return nil
}

// GetGenre — см. postgres.Store.
func (s *Store) GetGenre(_ context.Context, id model.GenreID) (model.Genre, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.genres[id]
	if !ok {
		return model.Genre{}, model.ErrNotFound
	}
	return g, nil
}

// GetGenreByCode — см. postgres.Store.
func (s *Store) GetGenreByCode(_ context.Context, code string) (model.Genre, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.genres {
		if g.Code == code && g.Enabled {
			return g, nil
		}
	}
	return model.Genre{}, model.ErrNotFound
}

// ListGenres — см. postgres.Store.
func (s *Store) ListGenres(_ context.Context) ([]model.Genre, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Genre
	for _, g := range s.genres {
		if g.Enabled {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GetCharacterModel — см. postgres.Store.
func (s *Store) GetCharacterModel(_ context.Context, id model.CharacterModelID) (model.CharacterModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.cmodels[id]
	if !ok {
		return model.CharacterModel{}, model.ErrNotFound
	}
	return m, nil
}

// CreateGame — см. postgres.Store; проверяет уникальность незавершённой игры чата и кода.
func (s *Store) CreateGame(_ context.Context, g model.Game) (model.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.games {
		if ex.ChatID == g.ChatID && ex.Status != model.GameFinished {
			return model.Game{}, fmt.Errorf("fake.CreateGame: unique violation games_chat_unfinished_idx")
		}
		if ex.JoinCode == g.JoinCode {
			return model.Game{}, fmt.Errorf("fake.CreateGame: unique violation join_code")
		}
	}
	g.ID = model.GameID(s.id())
	g.CreatedAt, g.UpdatedAt = s.now(), s.now()
	s.games[g.ID] = g
	return g, nil
}

// UpdateGame — см. postgres.Store.
func (s *Store) UpdateGame(_ context.Context, g model.Game) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.games[g.ID]
	if !ok {
		return model.ErrNotFound
	}
	g.CreatedAt, g.UpdatedAt, g.ChatID, g.JoinCode, g.CreatedByUserID = ex.CreatedAt, s.now(), ex.ChatID, ex.JoinCode, ex.CreatedByUserID
	s.games[g.ID] = g
	return nil
}

// GetGame — см. postgres.Store.
func (s *Store) GetGame(_ context.Context, id model.GameID) (model.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.games[id]
	if !ok {
		return model.Game{}, model.ErrNotFound
	}
	return g, nil
}

// GetGameByJoinCode — см. postgres.Store.
func (s *Store) GetGameByJoinCode(_ context.Context, code string) (model.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.games {
		if strings.EqualFold(g.JoinCode, code) {
			return g, nil
		}
	}
	return model.Game{}, model.ErrNotFound
}

// UnfinishedGameOfChat — см. postgres.Store.
func (s *Store) UnfinishedGameOfChat(_ context.Context, chat model.ChatID) (model.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.games {
		if g.ChatID == chat && g.Status != model.GameFinished {
			return g, nil
		}
	}
	return model.Game{}, model.ErrNotFound
}

// GamesOfUser — см. postgres.Store.
func (s *Store) GamesOfUser(_ context.Context, user model.UserID) ([]model.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ms []model.GameMember
	for _, m := range s.members {
		if m.UserID == user && m.Status == model.MemberActive && s.games[m.GameID].Status != model.GameFinished {
			ms = append(ms, m)
		}
	}
	sort.Slice(ms, func(i, j int) bool {
		return ms[i].JoinedAt.Before(ms[j].JoinedAt) || (ms[i].JoinedAt.Equal(ms[j].JoinedAt) && ms[i].GameID < ms[j].GameID)
	})
	out := make([]model.Game, 0, len(ms))
	for _, m := range ms {
		out = append(out, s.games[m.GameID])
	}
	return out, nil
}

// CreateSeason — см. postgres.Store.
func (s *Store) CreateSeason(_ context.Context, se model.Season) (model.Season, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	se.ID = model.SeasonID(s.id())
	se.StartedAt = s.now()
	s.seasons[se.ID] = se
	return se, nil
}

// ActiveSeason — см. postgres.Store.
func (s *Store) ActiveSeason(_ context.Context, game model.GameID) (model.Season, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best model.Season
	for _, se := range s.seasons {
		if se.GameID == game && se.Status == model.SeasonActive && se.SeasonNo > best.SeasonNo {
			best = se
		}
	}
	if best.ID == 0 {
		return model.Season{}, model.ErrNotFound
	}
	return best, nil
}

// CreateFaction — см. postgres.Store.
func (s *Store) CreateFaction(_ context.Context, f model.Faction) (model.Faction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f.ID = model.FactionID(s.id())
	s.facts[f.ID] = f
	return f, nil
}

// UpdateFaction — см. postgres.Store.
func (s *Store) UpdateFaction(_ context.Context, f model.Faction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.facts[f.ID]
	if !ok || ex.GameID != f.GameID {
		return model.ErrNotFound
	}
	ex.Title, ex.Description, ex.Tone = f.Title, f.Description, f.Tone
	s.facts[f.ID] = ex
	return nil
}

// ListFactions — см. postgres.Store.
func (s *Store) ListFactions(_ context.Context, game model.GameID) ([]model.Faction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Faction
	for _, code := range model.FactionCodes {
		for _, f := range s.facts {
			if f.GameID == game && f.Code == code {
				out = append(out, f)
			}
		}
	}
	return out, nil
}

// AddMember — см. postgres.Store.
func (s *Store) AddMember(_ context.Context, m model.GameMember) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{m.GameID, m.UserID}
	if ex, ok := s.members[k]; ok {
		ex.Status = model.MemberActive
		s.members[k] = ex
		return nil
	}
	m.JoinedAt = s.now()
	s.members[k] = m
	return nil
}

// GetMember — см. postgres.Store.
func (s *Store) GetMember(_ context.Context, game model.GameID, user model.UserID) (model.GameMember, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.members[memberKey{game, user}]
	if !ok {
		return model.GameMember{}, model.ErrNotFound
	}
	return m, nil
}

// ListMembers — см. postgres.Store.
func (s *Store) ListMembers(_ context.Context, game model.GameID) ([]model.MemberInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.MemberInfo
	for _, m := range s.members {
		if m.GameID == game && m.Status == model.MemberActive {
			out = append(out, model.MemberInfo{Member: m, User: s.users[m.UserID]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Member, out[j].Member
		return a.JoinedAt.Before(b.JoinedAt) || (a.JoinedAt.Equal(b.JoinedAt) && a.UserID < b.UserID)
	})
	return out, nil
}

// CreateCharacter — см. postgres.Store; проверяет уникальность (season, user) и никнейма.
func (s *Store) CreateCharacter(_ context.Context, c model.Character) (model.Character, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.chars {
		if ex.SeasonID == c.SeasonID && (ex.UserID == c.UserID || strings.EqualFold(ex.Nickname, c.Nickname)) {
			return model.Character{}, fmt.Errorf("fake.CreateCharacter: unique violation")
		}
	}
	if _, ok := s.members[memberKey{c.GameID, c.UserID}]; !ok {
		return model.Character{}, fmt.Errorf("fake.CreateCharacter: foreign key game_members")
	}
	c.ID = model.CharacterID(s.id())
	c.CreatedAt = s.now()
	s.chars[c.ID] = c
	return c, nil
}

// GetCharacter — см. postgres.Store.
func (s *Store) GetCharacter(_ context.Context, game model.GameID, season model.SeasonID, user model.UserID) (model.Character, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.chars {
		if c.GameID == game && c.SeasonID == season && c.UserID == user {
			return c, nil
		}
	}
	return model.Character{}, model.ErrNotFound
}

// ListCharacters — см. postgres.Store.
func (s *Store) ListCharacters(_ context.Context, game model.GameID, season model.SeasonID) ([]model.Character, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Character
	for _, c := range s.chars {
		if c.GameID == game && c.SeasonID == season {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// UpdateCharacterBio — см. postgres.Store.
func (s *Store) UpdateCharacterBio(_ context.Context, game model.GameID, id model.CharacterID, public, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chars[id]
	if !ok || c.GameID != game {
		return model.ErrNotFound
	}
	c.PublicBio, c.SecretBio = public, secret
	s.chars[id] = c
	return nil
}

// NicknameTaken — см. postgres.Store.
func (s *Store) NicknameTaken(_ context.Context, game model.GameID, season model.SeasonID, nickname string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.chars {
		if c.GameID == game && c.SeasonID == season && strings.EqualFold(c.Nickname, nickname) {
			return true, nil
		}
	}
	return false, nil
}
