// Package bot — сценарий обработки сообщений Telegram: дедупликация, лимит частоты, учёт пользователя,
// маршрутизация команд по контексту, диалоги в личке, ответы шаблонами.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"taletogether/internal/app/chat"
	"taletogether/internal/app/game"
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
}

// Dialogs — состояние диалогов в личке; реализует `adapter/postgres`.
type Dialogs interface {
	GetDialog(ctx context.Context, user model.UserID) (model.Dialog, error)
	SaveDialog(ctx context.Context, d model.Dialog) error
	DeleteDialog(ctx context.Context, user model.UserID) error
}

// Telegram — отправка ответов и запросы к Telegram; реализует `adapter/telegram`.
type Telegram interface {
	SendText(ctx context.Context, chat model.TelegramChatID, text string) error
	IsChatAdmin(ctx context.Context, chat model.TelegramChatID, user model.TelegramUserID) (bool, error)
	SetCommands(ctx context.Context, private, group []model.BotCommand) error
}

// Clock — источник времени; в тестах подменяется.
type Clock interface {
	Now() time.Time
}

// Settings — настройки сценария.
type Settings struct {
	Version     string // Версия сборки для ответа на /ping
	Env         string // dev|prod
	BotUsername string // Имя бота без @ для ссылок-приглашений
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
	clock    Clock
	logger   *slog.Logger
	limiter  *limiter
	commands []command
}

// New собирает сервис команд и реестр.
func New(s Settings, store Store, dialogs Dialogs, tg Telegram, chats *chat.Service, games *game.Service, rounds *round.Service, clock Clock, logger *slog.Logger) *Service {
	svc := &Service{settings: s, store: store, dialogs: dialogs, tg: tg, chats: chats, games: games, rounds: rounds, clock: clock, logger: logger,
		limiter: newLimiter(clock, rateCapacity, rateWindow)}
	svc.commands = svc.registry()
	return svc
}

// RegisterMenu отправляет меню команд по контекстам в Telegram.
func (s *Service) RegisterMenu(ctx context.Context) error {
	if err := s.tg.SetCommands(ctx, s.Commands(ctxPrivate), s.Commands(ctxGroup)); err != nil {
		return fmt.Errorf("bot.RegisterMenu: %w", err)
	}
	return nil
}

// HandleChatMember обрабатывает добавление и удаление бота в группе.
func (s *Service) HandleChatMember(ctx context.Context, ch model.ChatMemberChange) error {
	fresh, err := s.store.MarkUpdate(ctx, ch.UpdateID)
	if err != nil {
		return fmt.Errorf("bot.HandleChatMember: %w", err)
	}
	if !fresh {
		return nil
	}
	if err := s.chats.BotStatusChanged(ctx, ch); err != nil {
		return fmt.Errorf("bot.HandleChatMember: %w", err)
	}
	return nil
}

// Handle дедуплицирует update, проверяет лимиты, фиксирует пользователя и выполняет команду или шаг диалога.
func (s *Service) Handle(ctx context.Context, in model.Incoming) error {
	fresh, err := s.store.MarkUpdate(ctx, in.UpdateID)
	if err != nil {
		return fmt.Errorf("bot.Handle: %w", err)
	}
	if !fresh {
		s.logger.Debug("duplicate update skipped", slog.Int64("update_id", in.UpdateID))
		return nil
	}
	if in.From.ID == 0 {
		return nil
	}
	if !s.limiter.allow(in.From.ID) {
		s.logger.Warn("rate limit", slog.Int64("telegram_user_id", int64(in.From.ID)), slog.Int64("update_id", in.UpdateID))
		return nil
	}
	user, err := s.store.UpsertUser(ctx, in.From)
	if err != nil {
		return fmt.Errorf("bot.Handle: %w", err)
	}
	if len([]rune(in.Text)) > maxTextLen {
		if in.IsPrivate() {
			return s.reply(ctx, in, fmt.Sprintf("Слишком длинное сообщение: до %d символов.", maxTextLen))
		}
		return nil
	}
	req := &request{in: in, user: user}
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
		c, err := s.chats.Ensure(ctx, in, user, func(ctx context.Context) (bool, error) {
			return s.tg.IsChatAdmin(ctx, in.ChatID, in.From.ID)
		})
		if err != nil {
			return s.fail(ctx, req, err)
		}
		req.chat = c
	}
	if err := cmd.run(ctx, req); err != nil {
		return s.fail(ctx, req, err)
	}
	return nil
}

// request — разобранный контекст команды: сообщение, пользователь, чат группы (для группового контекста).
type request struct {
	in   model.Incoming
	user model.User
	chat model.Chat
}

// reply отправляет ответ в тот же чат.
func (s *Service) reply(ctx context.Context, in model.Incoming, text string) error {
	if err := s.tg.SendText(ctx, in.ChatID, text); err != nil {
		return fmt.Errorf("bot.reply: %w", err)
	}
	return nil
}

// replyPrivate отправляет сообщение пользователю в личку.
func (s *Service) replyPrivate(ctx context.Context, user model.User, text string) error {
	if err := s.tg.SendText(ctx, user.PrivateChat(), text); err != nil {
		return fmt.Errorf("bot.replyPrivate: %w", err)
	}
	return nil
}

// fail превращает ошибку сценария в ответ: ожидаемые ошибки ввода и прав — текстом игроку, сбои — общим сообщением и возвратом ошибки.
func (s *Service) fail(ctx context.Context, req *request, err error) error {
	if text, ok := userError(err); ok {
		return s.reply(ctx, req.in, text)
	}
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
	round.ErrNoOpenRound, round.ErrEmpty, round.ErrTooLong, round.ErrTooFrequent,
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
