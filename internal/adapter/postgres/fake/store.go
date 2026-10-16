// Package fake — хранилище в памяти для тестов сценариев; повторяет контракты adapter/postgres.
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"taletogether/internal/model"
)

// Store — хранилище в памяти; безопасно для параллельных тестов внутри одного экземпляра.
type Store struct {
	mu             sync.Mutex
	now            func() time.Time
	updates        map[int64]bool
	users          map[model.UserID]model.User
	chats          map[model.ChatID]model.Chat
	mods           map[model.ChatID][]moderator
	dialogs        map[model.UserID]model.Dialog
	genres         map[model.GenreID]model.Genre
	cmodels        map[model.CharacterModelID]model.CharacterModel
	games          map[model.GameID]model.Game
	seasons        map[model.SeasonID]model.Season
	facts          map[model.FactionID]model.Faction
	members        map[memberKey]model.GameMember
	chars          map[model.CharacterID]model.Character
	rounds         map[model.RoundID]model.Round
	subs           map[model.SubmissionID]model.Submission
	speeches       map[model.SpeechID]model.Speech
	outbox         []model.OutboxMessage
	locations      map[model.LocationID]model.Location
	discoveries    map[discoveryKey]bool
	quests         map[model.QuestID]model.Quest
	goals          map[model.FactionGoalID]model.FactionGoal
	cooldowns      map[cooldownKey]model.Cooldown // перезарядки действий (ADR-0033)
	jobs           map[jobKey]model.GenerationJob // вне снимка транзакции: пишется автономно, как в Postgres
	parties        map[model.PartyID]model.Party
	events         []model.Event
	resolved       []model.ActionResult
	resolvedRounds []resolvedKey
	llmProfiles    map[model.LLMProfileID]model.LLMProfile
	llmRuns        []model.LLMRun
	LLMPrices      []model.LLMPrice // тарифы моделей (ADR-0032); пусто — все модели неизвестны, цена 0
	settings       map[string]string
	nextID         int64
	txDepth        int
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

type cooldownKey struct {
	game    model.GameID
	owner   model.CooldownOwner
	ownerID int64
	action  model.CooldownAction
}

// New создаёт пустое хранилище с жанром fantasy и базовой моделью персонажа.
func New(now func() time.Time) *Store {
	s := &Store{
		now: now, updates: map[int64]bool{}, users: map[model.UserID]model.User{}, chats: map[model.ChatID]model.Chat{},
		mods: map[model.ChatID][]moderator{}, dialogs: map[model.UserID]model.Dialog{}, genres: map[model.GenreID]model.Genre{},
		cmodels: map[model.CharacterModelID]model.CharacterModel{}, games: map[model.GameID]model.Game{}, seasons: map[model.SeasonID]model.Season{},
		facts: map[model.FactionID]model.Faction{}, members: map[memberKey]model.GameMember{}, chars: map[model.CharacterID]model.Character{},
		rounds: map[model.RoundID]model.Round{}, subs: map[model.SubmissionID]model.Submission{}, speeches: map[model.SpeechID]model.Speech{},
		llmProfiles: map[model.LLMProfileID]model.LLMProfile{}, settings: map[string]string{}, locations: map[model.LocationID]model.Location{},
		parties: map[model.PartyID]model.Party{}, discoveries: map[discoveryKey]bool{}, jobs: map[jobKey]model.GenerationJob{},
		quests: map[model.QuestID]model.Quest{}, goals: map[model.FactionGoalID]model.FactionGoal{}, cooldowns: map[cooldownKey]model.Cooldown{},
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

// InTx выполняет fn как транзакцию: при ошибке или панике состояние откатывается к снимку на входе
// (как в Postgres), паника пробрасывается дальше. Вложенный вызов идёт в той же транзакции.
func (s *Store) InTx(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if s.Err != nil {
		return s.Err
	}
	s.mu.Lock()
	if s.txDepth > 0 {
		s.txDepth++
		s.mu.Unlock()
		defer func() { s.mu.Lock(); s.txDepth--; s.mu.Unlock() }()
		return fn(ctx)
	}
	s.txDepth++
	snap := s.snapshot()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.txDepth--
		if r := recover(); r != nil {
			s.restore(snap)
			panic(r)
		}
		if err != nil {
			s.restore(snap)
		}
	}()
	return fn(ctx)
}

// state — копия изменяемых таблиц для отката транзакции.
type state struct {
	updates     map[int64]bool
	users       map[model.UserID]model.User
	chats       map[model.ChatID]model.Chat
	mods        map[model.ChatID][]moderator
	dialogs     map[model.UserID]model.Dialog
	games       map[model.GameID]model.Game
	seasons     map[model.SeasonID]model.Season
	facts       map[model.FactionID]model.Faction
	members     map[memberKey]model.GameMember
	chars       map[model.CharacterID]model.Character
	rounds      map[model.RoundID]model.Round
	subs        map[model.SubmissionID]model.Submission
	speeches    map[model.SpeechID]model.Speech
	outbox      []model.OutboxMessage
	locations   map[model.LocationID]model.Location
	discoveries map[discoveryKey]bool
	parties     map[model.PartyID]model.Party
	events      []model.Event
	resolved    []model.ActionResult
	resolvedRnd []resolvedKey
	llmProfiles map[model.LLMProfileID]model.LLMProfile
	llmRuns     []model.LLMRun
	settings    map[string]string
	quests      map[model.QuestID]model.Quest
	goals       map[model.FactionGoalID]model.FactionGoal
	cooldowns   map[cooldownKey]model.Cooldown
	nextID      int64
}

func (s *Store) snapshot() state {
	mods := make(map[model.ChatID][]moderator, len(s.mods))
	for k, v := range s.mods {
		mods[k] = slices.Clone(v)
	}
	return state{
		updates: maps.Clone(s.updates), users: maps.Clone(s.users), chats: maps.Clone(s.chats), mods: mods, dialogs: maps.Clone(s.dialogs),
		games: maps.Clone(s.games), seasons: maps.Clone(s.seasons), facts: maps.Clone(s.facts), members: maps.Clone(s.members),
		chars: maps.Clone(s.chars), rounds: maps.Clone(s.rounds), subs: maps.Clone(s.subs), speeches: maps.Clone(s.speeches), outbox: slices.Clone(s.outbox),
		locations: maps.Clone(s.locations), discoveries: maps.Clone(s.discoveries), parties: maps.Clone(s.parties), events: slices.Clone(s.events), resolved: slices.Clone(s.resolved), resolvedRnd: slices.Clone(s.resolvedRounds),
		llmProfiles: maps.Clone(s.llmProfiles), llmRuns: slices.Clone(s.llmRuns), settings: maps.Clone(s.settings), nextID: s.nextID,
		quests: maps.Clone(s.quests), goals: maps.Clone(s.goals), cooldowns: maps.Clone(s.cooldowns),
	}
}

func (s *Store) restore(st state) {
	s.updates, s.users, s.chats, s.mods, s.dialogs = st.updates, st.users, st.chats, st.mods, st.dialogs
	s.games, s.seasons, s.facts, s.members, s.chars = st.games, st.seasons, st.facts, st.members, st.chars
	s.rounds, s.subs, s.outbox, s.locations, s.parties = st.rounds, st.subs, st.outbox, st.locations, st.parties
	s.speeches = st.speeches
	s.discoveries = st.discoveries
	s.events, s.resolved, s.resolvedRounds = st.events, st.resolved, st.resolvedRnd
	s.llmProfiles, s.llmRuns, s.settings, s.nextID = st.llmProfiles, st.llmRuns, st.settings, st.nextID
	s.quests, s.goals, s.cooldowns = st.quests, st.goals, st.cooldowns
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

// GetUserByTelegramID — см. postgres.Store.
func (s *Store) GetUserByTelegramID(_ context.Context, id model.TelegramUserID) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.TelegramID == id {
			return u, nil
		}
	}
	return model.User{}, model.ErrNotFound
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

// SetChatInviteLink — см. postgres.Store.
func (s *Store) SetChatInviteLink(_ context.Context, id model.ChatID, link string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chats[id]
	c.InviteLink = link
	s.chats[id] = c
	return nil
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
	c.BotStatus, c.Title, c.LeftAt, c.InviteLink = status, title, nil, ""
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

// ListActiveChats — см. postgres.Store.
func (s *Store) ListActiveChats(_ context.Context) ([]model.Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Chat
	for _, c := range s.chats {
		if c.BotStatus != model.BotLeft {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
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
	ex.Title, ex.Description, ex.Tone, ex.Lair = f.Title, f.Description, f.Tone, f.Lair
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

// UpdateCharacterAppearance — см. postgres.Store.
func (s *Store) UpdateCharacterAppearance(_ context.Context, game model.GameID, id model.CharacterID, appearance string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chars[id]
	if !ok || c.GameID != game {
		return model.ErrNotFound
	}
	c.Appearance = appearance
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

// ListCooldowns — см. postgres.Store.
func (s *Store) ListCooldowns(_ context.Context, game model.GameID, owner model.CooldownOwner, ownerID int64) ([]model.Cooldown, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	var out []model.Cooldown
	for k, c := range s.cooldowns {
		if k.game == game && k.owner == owner && k.ownerID == ownerID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Action < out[j].Action })
	return out, nil
}

// SetCooldown — см. postgres.Store.
func (s *Store) SetCooldown(_ context.Context, c model.Cooldown) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	c.UpdatedAt = s.now()
	s.cooldowns[cooldownKey{game: c.GameID, owner: c.Owner, ownerID: c.OwnerID, action: c.Action}] = c
	return nil
}
