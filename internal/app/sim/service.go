package sim

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
)

// Handler — обработчик входящих бота (`app/bot.Service`).
type Handler interface {
	Handle(ctx context.Context, in model.Incoming) error
	HandleChatMember(ctx context.Context, ch model.ChatMemberChange) error
}

// Transport — подмена Telegram: ящики сообщений и сборка входящих; реализует `adapter/telegram/fake`.
type Transport interface {
	Messages(chat model.TelegramChatID, after int64) []model.SentMessage
	Text(chat model.TelegramChatID, kind model.ChatType, from model.TelegramUser, text string) model.Incoming
	Press(chat model.TelegramChatID, kind model.ChatType, from model.TelegramUser, messageID int64, data string) model.Incoming
	ChatMember(chat model.TelegramChatID, kind model.ChatType, from model.TelegramUser, status model.BotStatus) model.ChatMemberChange
	SetAdmin(chat model.TelegramChatID, user model.TelegramUserID)
	SetTitle(chat model.TelegramChatID, title string)
}

// Store — чтение состояния игры и настройка профиля; реализует `adapter/postgres`.
type Store interface {
	SetAppSetting(ctx context.Context, key, value string) error
	ListLLMProfiles(ctx context.Context) ([]model.LLMProfile, error)
	GetChatByTelegramID(ctx context.Context, id model.TelegramChatID) (model.Chat, error)
	UnfinishedGameOfChat(ctx context.Context, chat model.ChatID) (model.Game, error)
	GetGame(ctx context.Context, id model.GameID) (model.Game, error)
	GetUserByTelegramID(ctx context.Context, id model.TelegramUserID) (model.User, error)
	ActiveSeason(ctx context.Context, game model.GameID) (model.Season, error)
	ListCharacters(ctx context.Context, game model.GameID, season model.SeasonID) ([]model.Character, error)
	OpenRound(ctx context.Context, game model.GameID) (model.Round, error)
	LastRound(ctx context.Context, game model.GameID) (model.Round, error)
	RoundByNo(ctx context.Context, game model.GameID, no int) (model.Round, error)
	ListSubmissions(ctx context.Context, game model.GameID, round model.RoundID) ([]model.Submission, error)
	ListOutbox(ctx context.Context, game model.GameID) ([]model.OutboxMessage, error)
	ListLLMRuns(ctx context.Context, limit int) ([]model.LLMRun, error)
	ListLocations(ctx context.Context, game model.GameID) ([]model.Location, error)
	ListQuests(ctx context.Context, game model.GameID) ([]model.Quest, error)
	ListFactionGoals(ctx context.Context, game model.GameID) ([]model.FactionGoal, error)
}

// LLM — вызовы модели с учётом (`app/llm.Service`).
type LLM interface {
	Complete(ctx context.Context, call applm.Call, req model.LLMRequest) (model.LLMResponse, error)
}

// Settings — параметры оркестратора.
type Settings struct {
	Poll         time.Duration            // период опроса состояния; 0 — 300 мс
	PhaseTimeout time.Duration            // ожидание фазы (варианты мира, завязка, закрытие раунда); 0 — 10 мин
	RateCapacity int                      // сообщений игрока в окно, на единицу меньше лимита бота; 0 — 19
	RateWindow   time.Duration            // окно лимита; 0 — минута
	Now          func() time.Time         // источник времени; nil — time.Now
	Models       map[model.LLMRole]string // подмена моделей ролей флагами (`RoleModels`): игрок берётся отсюда, остальные — для сводки
}

// Runner — оркестратор прогона.
type Runner struct {
	sc       Scenario
	settings Settings
	tg       Transport
	handler  Handler
	store    Store
	llm      LLM
	journal  *Journal
	logger   *slog.Logger

	group       model.TelegramChatID
	system      string
	profile     model.LLMProfile // профиль прогона из БД (`loadProfile`)
	playerModel string           // модель ИИ-игрока: флаг → `profile.Params.PlayerModel` → сценарий
	players     []*player
	game        model.Game

	mu      sync.Mutex
	entries []entry
	marks   []mark
	stopped string // причина досрочной остановки; пусто — доиграли до финала
}

// New собирает оркестратор; идентификаторы группы и игроков уникальны для прогона по времени запуска.
func New(sc Scenario, s Settings, tg Transport, h Handler, store Store, llm LLM, journal *Journal, logger *slog.Logger) *Runner {
	if s.Poll <= 0 {
		s.Poll = 300 * time.Millisecond
	}
	if s.PhaseTimeout <= 0 {
		s.PhaseTimeout = 10 * time.Minute
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.RateCapacity <= 0 {
		s.RateCapacity = 19
	}
	if s.RateWindow <= 0 {
		s.RateWindow = time.Minute
	}
	base := s.Now().Unix() % 1_000_000_000
	r := &Runner{sc: sc, settings: s, tg: tg, handler: h, store: store, llm: llm, journal: journal, logger: logger,
		group: model.TelegramChatID(-(1_000_000_000 + base))}
	for i, p := range sc.Players {
		id := model.TelegramUserID(base*10 + int64(i) + 1)
		r.players = append(r.players, &player{persona: p, tg: model.TelegramUser{ID: id, Username: p.Username, FirstName: p.Name, LanguageCode: "ru"},
			private: model.TelegramChatID(id), lastRead: map[model.TelegramChatID]int64{}})
	}
	return r
}

// Group — Telegram ID группы прогона.
func (r *Runner) Group() model.TelegramChatID { return r.group }

func (r *Runner) now() time.Time { return r.settings.Now() }

// Run ведёт партию от создания мира до финальной хроники или остановки; отчёт возвращается и при ошибке.
func (r *Runner) Run(ctx context.Context) (Report, error) {
	err := r.run(ctx)
	if err != nil && r.stopped == "" {
		r.stopped = "ошибка: " + err.Error()
	}
	rep := r.report(ctx)
	return rep, err
}

// loadProfile читает профиль прогона из БД (профили `sim-*` заводятся миграциями) и выбирает модель ИИ-игрока:
// флаг `-model-player`, иначе `params.player_model` профиля, иначе `player_model` сценария.
func (r *Runner) loadProfile(ctx context.Context) error {
	profiles, err := r.store.ListLLMProfiles(ctx)
	if err != nil {
		return fmt.Errorf("sim: профили LLM: %w", err)
	}
	found := false
	for _, p := range profiles {
		if p.Name == r.sc.Profile {
			r.profile, found = p, true
			break
		}
	}
	if !found {
		return fmt.Errorf("sim: профиль %q не найден в БД прогона (make migrate-sim; профили имитатора: sim-haiku, sim-sonnet, sim-opus-narrator)", r.sc.Profile)
	}
	if !r.profile.Enabled {
		return fmt.Errorf("sim: профиль %q выключен", r.sc.Profile)
	}
	switch {
	case r.settings.Models[model.RoleSimPlayer] != "":
		r.playerModel = r.settings.Models[model.RoleSimPlayer]
	case r.profile.Params.PlayerModel != "":
		r.playerModel = r.profile.Params.PlayerModel
	default:
		r.playerModel = r.sc.PlayerModel
	}
	if r.playerModel == "" {
		return fmt.Errorf("sim: модель ИИ-игрока не задана ни профилем %q (params.player_model), ни сценарием (player_model), ни флагом -model-player", r.sc.Profile)
	}
	r.logger.Info("sim profile", slog.String("profile", r.profile.Name), slog.String("provider", r.profile.Provider),
		slog.String("model_interpreter", r.roleModel(model.RoleInterpreter)), slog.String("model_narrator", r.roleModel(model.RoleNarrator)),
		slog.String("model_player", r.playerModel))
	return nil
}

// roleModel — фактическая модель роли: подмена флагом, иначе модель профиля; для игрока — `playerModel`.
func (r *Runner) roleModel(role model.LLMRole) string {
	if role == model.RoleSimPlayer {
		return r.playerModel
	}
	if m := r.settings.Models[role]; m != "" {
		return m
	}
	return r.profile.Model(role)
}

func (r *Runner) run(ctx context.Context) error {
	system, err := systemPrompt()
	if err != nil {
		return err
	}
	r.system = system
	if err := r.loadProfile(ctx); err != nil {
		return err
	}
	if err := r.store.SetAppSetting(ctx, model.SettingDefaultLLMProfile, r.sc.Profile); err != nil {
		return fmt.Errorf("sim: профиль прогона: %w", err)
	}
	admin := r.players[0]
	r.tg.SetAdmin(r.group, admin.tg.ID)
	r.tg.SetTitle(r.group, "Прогон "+r.sc.Name)
	r.note(nil, "бот добавлен в группу администратором "+admin.persona.Name)
	if err := r.handler.HandleChatMember(ctx, r.tg.ChatMember(r.group, model.ChatSupergroup, admin.tg, model.BotAdmin)); err != nil {
		return fmt.Errorf("sim: my_chat_member: %w", err)
	}
	if _, _, err := r.send(ctx, admin, admin.private, model.ChatPrivate, "/start"); err != nil {
		return err
	}
	if err := r.resolveUser(ctx, admin); err != nil {
		return err
	}
	if _, _, err := r.send(ctx, admin, r.group, model.ChatSupergroup, "/create"); err != nil {
		return err
	}
	if err := r.until(ctx, r.settings.PhaseTimeout, "создание мира", func(ctx context.Context) (bool, error) {
		chat, err := r.store.GetChatByTelegramID(ctx, r.group)
		if err != nil {
			return false, nil
		}
		g, err := r.store.UnfinishedGameOfChat(ctx, chat.ID)
		if err != nil {
			return false, nil
		}
		r.game = g
		return true, nil
	}); err != nil {
		return err
	}
	r.note(nil, fmt.Sprintf("мир создан: игра %d, код %s", r.game.ID, r.game.JoinCode))
	if err := r.dialog(ctx, admin, "настройка мира", func(ctx context.Context, g model.Game) (bool, error) {
		return g.Status != model.GameSetup, nil
	}); err != nil {
		return err
	}
	// Кворум 1: игроки входят по расписанию, и первые раунды играет один персонаж; сценарий может переопределить.
	setup := map[string]string{"round_minutes": strconv.Itoa(r.sc.RoundMinutes), "max_rounds": strconv.Itoa(r.sc.Rounds), "min_answers": "1"}
	if r.sc.Endless {
		// Бесконечная игра (ADR-0037): границы рассказа нет, после раунда `rounds` администратор завершает игру сам.
		setup["max_rounds"] = "0"
	}
	for k, v := range r.sc.Setup {
		setup[k] = v
	}
	for _, k := range (Scenario{Setup: setup}).SetupKeys() {
		if _, _, err := r.send(ctx, admin, admin.private, model.ChatPrivate, "/set "+k+" "+setup[k]); err != nil {
			return err
		}
	}
	if err := r.join(ctx, admin); err != nil {
		return err
	}
	if _, _, err := r.send(ctx, admin, r.group, model.ChatSupergroup, "/begin"); err != nil {
		return err
	}
	if err := r.until(ctx, r.settings.PhaseTimeout, "открытие первого раунда", func(ctx context.Context) (bool, error) {
		g, err := r.refresh(ctx)
		if err != nil {
			return false, err
		}
		if g.Status != model.GameActive {
			return false, nil
		}
		_, err = r.store.OpenRound(ctx, g.ID)
		return err == nil, nil
	}); err != nil {
		return err
	}
	return r.rounds(ctx)
}

// rounds — цикл раундов: вход игроков по расписанию, ходы, закрытие `/next`, бюджет, финал.
func (r *Runner) rounds(ctx context.Context) error {
	admin := r.players[0]
	maxActions := r.sc.MaxActions
	if maxActions <= 0 {
		maxActions = 4
	}
	for {
		g, err := r.refresh(ctx)
		if err != nil {
			return err
		}
		if g.Status == model.GameFinished {
			r.note(nil, "игра завершена")
			return r.drain(ctx, g.ID)
		}
		open, err := r.store.OpenRound(ctx, g.ID)
		if errors.Is(err, model.ErrNotFound) {
			if err := r.until(ctx, r.settings.PhaseTimeout, "открытие раунда", func(ctx context.Context) (bool, error) {
				g, err := r.refresh(ctx)
				if err != nil {
					return false, err
				}
				if g.Status == model.GameFinished {
					return true, nil
				}
				_, err = r.store.OpenRound(ctx, g.ID)
				return err == nil, nil
			}); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("sim: открытый раунд: %w", err)
		}
		n := open.RoundNo
		if n > r.sc.Rounds+1 {
			r.stopped = fmt.Sprintf("раунд %d за границей рассказа %d", n, r.sc.Rounds)
			return nil
		}
		r.mark(n)
		for _, p := range r.players {
			if !p.joined && p.persona.JoinsRound <= n {
				if err := r.join(ctx, p); err != nil {
					return err
				}
			}
		}
		for _, p := range r.players {
			if !p.joined {
				continue
			}
			if err := r.turn(ctx, p, phaseRound, &g, &open, maxActions); err != nil {
				return err
			}
			if err := r.ensureSubmission(ctx, p, g, open); err != nil {
				return err
			}
		}
		if r.sc.Endless && n >= r.sc.Rounds {
			// Бесконечная игра: вместо `/next` последнего раунда — `/finish yes`: тик разыгрывает раунд и публикует финал.
			r.note(nil, fmt.Sprintf("бесконечная игра: после раунда %d администратор завершает её командой /finish yes", n))
			if _, _, err := r.send(ctx, admin, r.group, model.ChatSupergroup, "/finish yes"); err != nil {
				return err
			}
			if err := r.until(ctx, r.settings.PhaseTimeout, "завершение бесконечной игры", func(ctx context.Context) (bool, error) {
				g, err := r.refresh(ctx)
				return err == nil && g.Status == model.GameFinished, nil
			}); err != nil {
				return err
			}
			continue // финал читает начало цикла: отметка и доставка outbox
		}
		if _, _, err := r.send(ctx, admin, r.group, model.ChatSupergroup, "/next"); err != nil {
			return err
		}
		if err := r.until(ctx, r.settings.PhaseTimeout, fmt.Sprintf("закрытие раунда %d", n), func(ctx context.Context) (bool, error) {
			g, err := r.refresh(ctx)
			if err != nil {
				return false, err
			}
			if g.Status == model.GameFinished || g.Status == model.GamePaused {
				return true, nil
			}
			next, err := r.store.OpenRound(ctx, g.ID)
			return err == nil && next.RoundNo > n, nil
		}); err != nil {
			return err
		}
		if g, err := r.refresh(ctx); err == nil && g.Status == model.GamePaused {
			r.stopped = "игра встала на паузу"
			return nil
		}
		if stop, err := r.overBudget(ctx); err != nil {
			return err
		} else if stop != "" {
			r.stopped = stop
			r.note(nil, stop+": игра завершается командой /finish yes")
			_, _, err := r.send(ctx, admin, r.group, model.ChatSupergroup, "/finish yes")
			if err != nil {
				return err
			}
			if err := r.until(ctx, r.settings.PhaseTimeout, "завершение игры", func(ctx context.Context) (bool, error) {
				g, err := r.refresh(ctx)
				return err == nil && g.Status == model.GameFinished, nil
			}); err != nil {
				return err
			}
			continue // финал читает начало цикла: отметка и доставка outbox
		}
	}
}

// join вводит игрока в игру: личка, вход по коду (администратор — `/create` персонажа), диалог персонажа.
func (r *Runner) join(ctx context.Context, p *player) error {
	admin := p == r.players[0]
	if !admin {
		if _, _, err := r.send(ctx, p, p.private, model.ChatPrivate, "/start join_"+r.game.JoinCode); err != nil {
			return err
		}
		if err := r.resolveUser(ctx, p); err != nil {
			return err
		}
	} else if _, _, err := r.send(ctx, p, p.private, model.ChatPrivate, "/create"); err != nil {
		return err
	}
	if err := r.dialog(ctx, p, "создание персонажа "+p.persona.Name, func(ctx context.Context, g model.Game) (bool, error) {
		return r.hasCharacter(ctx, p, g)
	}); err != nil {
		return err
	}
	p.joined = true
	r.note(p, "персонаж создан")
	return nil
}

// hasCharacter ищет персонажа игрока в активном сезоне и запоминает его идентификатор.
func (r *Runner) hasCharacter(ctx context.Context, p *player, g model.Game) (bool, error) {
	season, err := r.store.ActiveSeason(ctx, g.ID)
	if err != nil {
		return false, nil
	}
	chars, err := r.store.ListCharacters(ctx, g.ID, season.ID)
	if err != nil {
		return false, err
	}
	for _, c := range chars {
		if c.UserID == p.userID {
			p.charID = c.ID
			return true, nil
		}
	}
	return false, nil
}

// resolveUser запоминает идентификатор пользователя бота после первого сообщения игрока.
func (r *Runner) resolveUser(ctx context.Context, p *player) error {
	u, err := r.store.GetUserByTelegramID(ctx, p.tg.ID)
	if err != nil {
		return fmt.Errorf("sim: пользователь %s: %w", p.persona.Name, err)
	}
	p.userID = u.ID
	return nil
}

// dialog ведёт игрока по диалогу лички по одному действию за итерацию, пока не выполнится условие.
func (r *Runner) dialog(ctx context.Context, p *player, what string, done func(context.Context, model.Game) (bool, error)) error {
	for i := 0; i < 60; i++ {
		g, err := r.refresh(ctx)
		if err != nil {
			return err
		}
		if ok, err := done(ctx, g); err != nil {
			return err
		} else if ok {
			return nil
		}
		if err := r.drain(ctx, g.ID); err != nil {
			return err
		}
		if len(r.unread(p)) == 0 {
			if err := r.until(ctx, r.settings.PhaseTimeout, what+": ожидание сообщения бота", func(ctx context.Context) (bool, error) {
				if len(r.unread(p)) > 0 {
					return true, nil
				}
				g, err := r.refresh(ctx)
				if err != nil {
					return false, err
				}
				return done(ctx, g)
			}); err != nil {
				return err
			}
			continue
		}
		if err := r.turn(ctx, p, phaseDialog, &g, nil, 1); err != nil {
			return err
		}
	}
	return fmt.Errorf("sim: %s: диалог не завершён за 60 шагов", what)
}

// ensureSubmission подаёт запасную заявку, если игрок за ход её не подал, а персонаж в игре и не уходит.
func (r *Runner) ensureSubmission(ctx context.Context, p *player, g model.Game, open model.Round) error {
	subs, err := r.store.ListSubmissions(ctx, g.ID, open.ID)
	if err != nil {
		return fmt.Errorf("sim: заявки раунда: %w", err)
	}
	for _, s := range subs {
		if s.CharacterID == p.charID {
			return nil
		}
	}
	season, err := r.store.ActiveSeason(ctx, g.ID)
	if err != nil {
		return nil
	}
	chars, err := r.store.ListCharacters(ctx, g.ID, season.ID)
	if err != nil {
		return err
	}
	for _, c := range chars {
		if c.ID == p.charID && c.Status == model.CharacterActive && !c.Away {
			r.note(p, "заявки нет — запасной ход оркестратора")
			_, _, err := r.send(ctx, p, p.private, model.ChatPrivate, "/act Осматриваюсь по сторонам и жду, что будет дальше.")
			return err
		}
	}
	return nil
}

// overBudget — причина остановки по бюджету; пусто — в пределах.
func (r *Runner) overBudget(ctx context.Context) (string, error) {
	if r.sc.Budget.MaxCalls <= 0 && r.sc.Budget.MaxUSD <= 0 {
		return "", nil
	}
	calls, cost, err := r.spent(ctx)
	if err != nil {
		return "", err
	}
	if r.sc.Budget.MaxCalls > 0 && calls >= r.sc.Budget.MaxCalls {
		return fmt.Sprintf("бюджет вызовов исчерпан: %d из %d", calls, r.sc.Budget.MaxCalls), nil
	}
	if r.sc.Budget.MaxUSD > 0 && cost >= r.sc.Budget.MaxUSD {
		return fmt.Sprintf("бюджет исчерпан: $%.4f из $%.2f", cost, r.sc.Budget.MaxUSD), nil
	}
	return "", nil
}

// spent — вызовы и стоимость по игре прогона из `llm_runs`.
func (r *Runner) spent(ctx context.Context) (int, float64, error) {
	runs, err := r.store.ListLLMRuns(ctx, 10000)
	if err != nil {
		return 0, 0, fmt.Errorf("sim: llm_runs: %w", err)
	}
	calls, cost := 0, 0.0
	for _, run := range runs {
		if run.GameID != nil && *run.GameID == r.game.ID {
			calls++
			cost += run.CostUSD
		}
	}
	return calls, cost, nil
}

// refresh перечитывает игру прогона.
func (r *Runner) refresh(ctx context.Context) (model.Game, error) {
	g, err := r.store.GetGame(ctx, r.game.ID)
	if err != nil {
		return model.Game{}, fmt.Errorf("sim: игра %d: %w", r.game.ID, err)
	}
	r.game = g
	return g, nil
}

// drain ждёт, пока воркер outbox доставит все сообщения игры: игрок читает ящики только после доставки.
func (r *Runner) drain(ctx context.Context, game model.GameID) error {
	if game == 0 {
		return nil
	}
	return r.until(ctx, 2*time.Minute, "доставка outbox", func(ctx context.Context) (bool, error) {
		msgs, err := r.store.ListOutbox(ctx, game)
		if err != nil {
			return false, fmt.Errorf("sim: outbox: %w", err)
		}
		for _, m := range msgs {
			if m.Status == model.OutboxPending || m.Status == model.OutboxSending {
				return false, nil
			}
		}
		return true, nil
	})
}

// until опрашивает условие с периодом Poll до истечения timeout.
func (r *Runner) until(ctx context.Context, timeout time.Duration, what string, cond func(context.Context) (bool, error)) error {
	deadline := r.now().Add(timeout)
	for {
		ok, err := cond(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if !r.now().Before(deadline) {
			return fmt.Errorf("sim: %s: не дождались за %s", what, timeout)
		}
		if err := r.wait(ctx, r.settings.Poll); err != nil {
			return err
		}
	}
}

// wait ждёт d или отмены контекста.
func (r *Runner) wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// entry — строка транскрипта: действие игрока, его мысль, сообщение бота или заметка оркестратора.
type entry struct {
	At      time.Time
	Who     string // имя игрока, «бот» или «оркестратор»
	Where   string // группа | личка <имя> | мысль | заметка
	Text    string
	Player  bool
	Thought bool
}

// mark — открытие раунда по времени для заголовков транскрипта.
type mark struct {
	Round int
	At    time.Time
}

func (r *Runner) record(e entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

// note — заметка оркестратора в транскрипт и лог; p — игрок, к которому относится (nil — общая).
func (r *Runner) note(p *player, text string) {
	who := "оркестратор"
	if p != nil {
		text = p.persona.Name + ": " + text
	}
	r.logger.Info("sim", slog.String("note", text))
	r.record(entry{At: r.now(), Who: who, Where: "заметка", Text: text})
}

func (r *Runner) mark(round int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.marks) > 0 && r.marks[len(r.marks)-1].Round == round {
		return
	}
	r.marks = append(r.marks, mark{Round: round, At: r.now()})
	r.logger.Info("sim round", slog.Int("round", round))
}
