// Package bot — сценарий обработки сообщений Telegram: дедупликация, лимит частоты, учёт пользователя,
// маршрутизация команд и нажатий кнопок по контексту (ADR-0015), диалоги в личке, ответы шаблонами.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"taletogether/internal/app/chat"
	"taletogether/internal/app/game"
	applm "taletogether/internal/app/llm"
	"taletogether/internal/app/round"
	"taletogether/internal/model"
	"taletogether/internal/service/rules"
)

// Store — хранилище уровня бота; реализует `adapter/postgres`.
type Store interface {
	MarkUpdate(ctx context.Context, updateID int64) (bool, error)
	UpsertUser(ctx context.Context, u model.TelegramUser) (model.User, error)
	MarkStarted(ctx context.Context, id model.UserID) error
	GetUserByUsername(ctx context.Context, username string) (model.User, error)
	ChatsOfUser(ctx context.Context, user model.UserID) ([]model.Chat, error)
	ListActiveChats(ctx context.Context) ([]model.Chat, error)
}

// Dialogs — состояние диалогов в личке; реализует `adapter/postgres`.
type Dialogs interface {
	GetDialog(ctx context.Context, user model.UserID) (model.Dialog, error)
	SaveDialog(ctx context.Context, d model.Dialog) error
	DeleteDialog(ctx context.Context, user model.UserID) error
}

// Telegram — всё, что сценарию нужно от Telegram; реализует `adapter/telegram`.
type Telegram interface {
	Messenger
	ChatAPI
}

// Messenger — ответы на команды (ADR-0006): текст, текст с кнопками и снятие кнопок после нажатия (ADR-0015).
type Messenger interface {
	SendText(ctx context.Context, chat model.TelegramChatID, text string) error
	SendKeyboard(ctx context.Context, chat model.TelegramChatID, text string, kb model.Keyboard) error
	ClearKeyboard(ctx context.Context, chat model.TelegramChatID, messageID int64) error
}

// ChatAPI — запросы о чате и меню команд (B20).
type ChatAPI interface {
	IsChatAdmin(ctx context.Context, chat model.TelegramChatID, user model.TelegramUserID) (bool, error)
	SetCommands(ctx context.Context, private, group []model.BotCommand) error
	SetMemberCommands(ctx context.Context, chat model.TelegramChatID, user model.TelegramUserID, cmds []model.BotCommand) error
}

// Clock — источник времени; в тестах подменяется.
type Clock interface {
	Now() time.Time
}

// Journal — журнал чатов для разбора ошибок (ADR-0012); реализует `adapter/chatlog`.
type Journal interface {
	Write(ctx context.Context, rec model.ChatLogRecord)
}

// Settings — настройки сценария.
type Settings struct {
	Version     string               // Версия сборки для ответа на /ping
	Env         string               // dev|prod
	BotUsername string               // Имя бота без @ для ссылок-приглашений
	OwnerID     model.TelegramUserID // Владелец бота: команды /llm; 0 — команды недоступны
	GuideURL    string               // Корень руководства игрока для кнопок-ссылок /help; пусто — без ссылок
	// AutoFaction — фракция, к которой персонаж прикрепляется без вопроса; пусто — игрок выбирает сам.
	AutoFaction model.FactionCode
}

// Service обрабатывает входящие сообщения. Ответы на команды отправляются синхронно (ADR-0006).
type Service struct {
	settings Settings
	store    Store
	dialogs  Dialogs
	tg       Telegram
	chats    *chat.Service
	games    *game.Service
	rounds   *round.Service
	llm      *applm.Service
	clock    Clock
	logger   *slog.Logger
	limiter  *limiter
	commands []command
	// Journal — журнал чатов: входящие, ответы и сбои; nil — выключен.
	Journal Journal
}

// journal пишет запись журнала чата с текущим временем; без журнала ничего не делает.
func (s *Service) journal(ctx context.Context, rec model.ChatLogRecord) {
	if s.Journal == nil {
		return
	}
	rec.TS = s.clock.Now()
	s.Journal.Write(ctx, rec)
}

// journalUser — автор записи журнала из данных Telegram.
func journalUser(u model.TelegramUser) *model.ChatLogUser {
	return &model.ChatLogUser{ID: u.ID, Username: u.Username, Name: u.FirstName}
}

// New собирает сервис команд и реестр.
func New(s Settings, store Store, dialogs Dialogs, tg Telegram, chats *chat.Service, games *game.Service, rounds *round.Service, llm *applm.Service, clock Clock, logger *slog.Logger) *Service {
	svc := &Service{settings: s, store: store, dialogs: dialogs, tg: tg, chats: chats, games: games, rounds: rounds, llm: llm, clock: clock, logger: logger,
		limiter: newLimiter(clock, rateCapacity, rateWindow)}
	svc.commands = svc.registry()
	return svc
}

// RegisterMenu отправляет в Telegram меню команд по контекстам: личка, группа для всех участников и меню владельца
// и модераторов каждого зарегистрированного чата (B20). Ошибки меню отдельных чатов только в лог.
func (s *Service) RegisterMenu(ctx context.Context) error {
	if err := s.tg.SetCommands(ctx, s.Menu(ctxPrivate, false), s.Menu(ctxGroup, false)); err != nil {
		return fmt.Errorf("bot.RegisterMenu: %w", err)
	}
	chats, err := s.store.ListActiveChats(ctx)
	if err != nil {
		return fmt.Errorf("bot.RegisterMenu: %w", err)
	}
	for _, c := range chats {
		s.syncChatMenus(ctx, c)
	}
	return nil
}

// syncChatMenus выставляет меню владельца и модераторов чата всем, у кого есть права (B20).
func (s *Service) syncChatMenus(ctx context.Context, c model.Chat) {
	info, err := s.chats.Info(ctx, c)
	if err != nil {
		s.logger.Warn("chat menu not synced", slog.Int64("chat_id", int64(c.ID)), slog.Any("err", err))
		return
	}
	s.syncMemberMenu(ctx, c, info.Owner)
	for _, m := range info.Moderators {
		s.syncMemberMenu(ctx, c, m)
	}
}

// syncMemberMenu по текущим правам пользователя в чате ставит ему меню владельца и модераторов или снимает его,
// возвращая меню группы для всех (B20). Ошибка Telegram только в лог: права проверяет код при выполнении команды.
func (s *Service) syncMemberMenu(ctx context.Context, c model.Chat, u model.User) {
	can, err := s.chats.CanManage(ctx, c, u.ID)
	if err != nil {
		s.logger.Warn("member menu not synced", slog.Int64("chat_id", int64(c.ID)), slog.Int64("user_id", int64(u.ID)), slog.Any("err", err))
		return
	}
	var cmds []model.BotCommand
	if can {
		cmds = s.Menu(ctxGroup, true)
	}
	if err := s.tg.SetMemberCommands(ctx, c.TelegramChatID, u.TelegramID, cmds); err != nil {
		s.logger.Warn("member menu not set", slog.Int64("chat_id", int64(c.ID)), slog.Int64("user_id", int64(u.ID)), slog.Any("err", err))
	}
}

// HandleChatMember обрабатывает добавление и удаление бота в группе.
func (s *Service) HandleChatMember(ctx context.Context, ch model.ChatMemberChange) error {
	fresh, err := s.store.MarkUpdate(ctx, ch.UpdateID)
	if err != nil {
		return fmt.Errorf("bot.HandleChatMember: %w", err)
	}
	s.journal(ctx, model.ChatLogRecord{Kind: model.ChatLogIn, Chat: ch.ChatID, UpdateID: ch.UpdateID, User: journalUser(ch.From), Event: "my_chat_member", Text: string(ch.NewStatus), Duplicate: !fresh})
	if !fresh {
		return nil
	}
	if err := s.chats.BotStatusChanged(ctx, ch); err != nil {
		return fmt.Errorf("bot.HandleChatMember: %w", err)
	}
	if ch.NewStatus != model.BotLeft {
		if c, err := s.chats.OfTelegram(ctx, ch.ChatID); err == nil {
			s.syncChatMenus(ctx, c)
		}
	}
	return nil
}

// Handle дедуплицирует update, проверяет лимиты, фиксирует пользователя и выполняет команду или шаг диалога.
// Нажатие кнопки (ADR-0015) идёт тем же путём: данные кнопки уже разобраны в команду или ответ диалога; после
// обработки кнопки с сообщения снимаются, кроме отказа в правах, чтобы чужое нажатие не гасило кнопку владельца.
func (s *Service) Handle(ctx context.Context, in model.Incoming) error {
	fresh, err := s.store.MarkUpdate(ctx, in.UpdateID)
	if err != nil {
		return fmt.Errorf("bot.Handle: %w", err)
	}
	rec := model.ChatLogRecord{Kind: model.ChatLogIn, Chat: in.ChatID, UpdateID: in.UpdateID, User: journalUser(in.From), Command: in.Command, Text: in.Text, Duplicate: !fresh}
	if in.Callback != nil {
		rec.Event = "callback"
	}
	if !fresh {
		s.logger.Debug("duplicate update skipped", slog.Int64("update_id", in.UpdateID))
		s.journal(ctx, rec)
		return nil
	}
	if in.From.ID == 0 {
		return nil
	}
	if !s.limiter.allow(in.From.ID) {
		s.logger.Warn("rate limit", slog.Int64("telegram_user_id", int64(in.From.ID)), slog.Int64("update_id", in.UpdateID))
		rec.Event = "rate_limited"
		s.journal(ctx, rec)
		return nil
	}
	user, err := s.store.UpsertUser(ctx, in.From)
	if err != nil {
		return fmt.Errorf("bot.Handle: %w", err)
	}
	if in.IsPrivate() && user.CurrentGameID != nil {
		rec.GameID = *user.CurrentGameID
	}
	s.journal(ctx, rec)
	if len([]rune(in.Text)) > maxTextLen {
		if in.IsPrivate() {
			return s.reply(ctx, in, fmt.Sprintf("Слишком длинное сообщение: до %d символов.", maxTextLen))
		}
		return nil
	}
	req := &request{in: in, user: user}
	err = s.dispatch(ctx, req)
	if in.Callback != nil && !isRightsError(err) {
		if cerr := s.tg.ClearKeyboard(ctx, in.ChatID, in.Callback.MessageID); cerr != nil {
			s.logger.Warn("keyboard not cleared", slog.Int64("chat_id", int64(in.ChatID)), slog.Int64("update_id", in.UpdateID), slog.Any("err", cerr))
		}
	}
	if err != nil {
		return s.fail(ctx, req, err)
	}
	return nil
}

// dispatch выполняет шаг диалога или команду; ошибки сценария возвращаются как есть, ответ на них даёт Handle.
func (s *Service) dispatch(ctx context.Context, req *request) error {
	in := req.in
	if in.Command == "" {
		if !in.IsPrivate() {
			return nil
		}
		return s.handleDialogAnswer(ctx, req)
	}
	s.logger.Info("command", slog.String("command", in.Command), slog.Int64("chat_id", int64(in.ChatID)), slog.Int64("update_id", in.UpdateID))
	cmd, ok := s.lookup(in.Command)
	if !ok {
		if in.IsPrivate() {
			return s.reply(ctx, in, "Неизвестная команда. Список: /help")
		}
		return nil // в группе могут быть команды других ботов
	}
	if !cmd.allows(in) {
		return s.reply(ctx, in, cmd.hint())
	}
	if !in.IsPrivate() && cmd.needsChat {
		c, created, err := s.chats.Ensure(ctx, in, req.user, func(ctx context.Context) (bool, error) {
			return s.tg.IsChatAdmin(ctx, in.ChatID, in.From.ID)
		})
		if err != nil {
			return err
		}
		if created {
			s.syncMemberMenu(ctx, c, req.user) // владелец, зарегистрировавший чат командой, сразу получает меню (B20)
		}
		req.chat = c
	}
	return cmd.run(ctx, req)
}

// isRightsError — отказ в правах: после такого нажатия кнопки остаются на сообщении.
func isRightsError(err error) bool {
	for _, e := range []error{chat.ErrForbidden, game.ErrForbidden, round.ErrForbidden, errNotOwner} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// request — разобранный контекст команды: сообщение, пользователь, чат группы (для группового контекста).
type request struct {
	in   model.Incoming
	user model.User
	chat model.Chat
}

// reply отправляет ответ в тот же чат.
func (s *Service) reply(ctx context.Context, in model.Incoming, text string) error {
	return s.replyKeyboard(ctx, in, text, nil)
}

// replyKeyboard отправляет ответ в тот же чат с кнопками под ним (nil — без кнопок).
func (s *Service) replyKeyboard(ctx context.Context, in model.Incoming, text string, kb model.Keyboard) error {
	if err := s.send(ctx, in.ChatID, in.UpdateID, text, kb); err != nil {
		return fmt.Errorf("bot.reply: %w", err)
	}
	return nil
}

// replyPrivate отправляет сообщение пользователю в личку.
func (s *Service) replyPrivate(ctx context.Context, user model.User, text string) error {
	return s.replyPrivateKeyboard(ctx, user, text, nil)
}

// replyPrivateKeyboard отправляет сообщение с кнопками пользователю в личку.
func (s *Service) replyPrivateKeyboard(ctx context.Context, user model.User, text string, kb model.Keyboard) error {
	if err := s.send(ctx, user.PrivateChat(), 0, text, kb); err != nil {
		return fmt.Errorf("bot.replyPrivate: %w", err)
	}
	return nil
}

// send отправляет текст с кнопками или без и пишет исходящее в журнал чата.
func (s *Service) send(ctx context.Context, chat model.TelegramChatID, updateID int64, text string, kb model.Keyboard) error {
	rec := model.ChatLogRecord{Kind: model.ChatLogOut, Chat: chat, UpdateID: updateID, Text: text}
	var err error
	if len(kb) > 0 {
		err = s.tg.SendKeyboard(ctx, chat, text, kb)
	} else {
		err = s.tg.SendText(ctx, chat, text)
	}
	if err != nil {
		rec.Error = err.Error()
	}
	s.journal(ctx, rec)
	return err
}

// fail превращает ошибку сценария в ответ: ожидаемые ошибки ввода и прав — текстом игроку, сбои — общим сообщением и возвратом ошибки.
func (s *Service) fail(ctx context.Context, req *request, err error) error {
	if text, ok := userError(err); ok {
		return s.reply(ctx, req.in, text)
	}
	s.journal(ctx, model.ChatLogRecord{Kind: model.ChatLogError, Chat: req.in.ChatID, UpdateID: req.in.UpdateID, Command: req.in.Command, Error: err.Error()})
	if sendErr := s.reply(ctx, req.in, "Что-то пошло не так. Попробуйте позже."); sendErr != nil {
		return fmt.Errorf("bot.Handle: %w (reply: %v)", err, sendErr)
	}
	return fmt.Errorf("bot.Handle: %w", err)
}

// userErrors — ошибки сценариев, текст которых показывается игроку как есть.
var userErrors = []error{
	chat.ErrForbidden, chat.ErrUnknown,
	game.ErrForbidden, game.ErrGameExists, game.ErrNoGame, game.ErrGameFinished, game.ErrNotMember,
	game.ErrHasCharacter, game.ErrNoCharacter, game.ErrNicknameTaken, game.ErrChooseGame, game.ErrBadValue,
	round.ErrForbidden, round.ErrAlreadyStarted, round.ErrNotActive, round.ErrPaused, round.ErrFinished, round.ErrNoCharacters,
	round.ErrNoOpenRound, round.ErrEmpty, round.ErrTooLong, round.ErrTooFrequent, round.ErrAlreadyActed, round.ErrDead, round.ErrNoRound, round.ErrNotResolved,
	round.ErrRoundClosed, round.ErrOptionGone,
	applm.ErrNoProfile, applm.ErrUnknownProfile, applm.ErrUnknownProvider, applm.ErrProfileExists, applm.ErrBadValue, applm.ErrNoGame, errNotOwner,
	rules.ErrInvalid, errUnknownUser,
}

// userError возвращает текст для игрока, если ошибка относится к вводу или правам.
func userError(err error) (string, bool) {
	for _, ue := range userErrors {
		if errors.Is(err, ue) {
			return upperFirst(err.Error()), true
		}
	}
	return "", false
}

func upperFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	if r[0] >= 'а' && r[0] <= 'я' {
		r[0] -= 'а' - 'А'
	}
	return string(r)
}

// joinLink — deep link для входа в игру.
func (s *Service) joinLink(g model.Game) string {
	return fmt.Sprintf("https://t.me/%s?start=join_%s", s.settings.BotUsername, g.JoinCode)
}

// setupLink — deep link для настройки игры администратором.
func (s *Service) setupLink(g model.Game) string {
	return fmt.Sprintf("https://t.me/%s?start=setup_%s", s.settings.BotUsername, g.JoinCode)
}
