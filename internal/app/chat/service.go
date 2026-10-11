// Package chat — сценарии чатов с ботом: регистрация, владелец, модераторы, проверка прав (ADR-0007).
package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"taletogether/internal/model"
)

// Store — хранилище чатов и прав; реализует `adapter/postgres`.
type Store interface {
	GetChatByTelegramID(ctx context.Context, id model.TelegramChatID) (model.Chat, error)
	CreateChat(ctx context.Context, c model.Chat) (model.Chat, error)
	UpdateChatStatus(ctx context.Context, id model.ChatID, status model.BotStatus, title string) error
	SetChatOwner(ctx context.Context, id model.ChatID, owner model.UserID) error
	AddModerator(ctx context.Context, chat model.ChatID, user, grantedBy model.UserID) error
	RemoveModerator(ctx context.Context, chat model.ChatID, user model.UserID) error
	IsModerator(ctx context.Context, chat model.ChatID, user model.UserID) (bool, error)
	ListModerators(ctx context.Context, chat model.ChatID) ([]model.User, error)
	GetUser(ctx context.Context, id model.UserID) (model.User, error)
	UpsertUser(ctx context.Context, u model.TelegramUser) (model.User, error)
}

// Ошибки сценариев; текст пригоден для ответа в чат.
var (
	ErrForbidden = errors.New("нет прав: действие доступно владельцу чата")
	ErrUnknown   = errors.New("чат не зарегистрирован: добавьте бота в группу заново или выполните команду администратором группы")
)

// Service — сценарии чатов.
type Service struct {
	store  Store
	logger *slog.Logger
}

// New собирает сервис чатов.
func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger}
}

// BotStatusChanged обрабатывает my_chat_member: добавление бота регистрирует чат с владельцем — автором изменения,
// удаление помечает bot_status = left; повторное добавление возвращает статус без смены владельца.
func (s *Service) BotStatusChanged(ctx context.Context, ch model.ChatMemberChange) error {
	if ch.ChatType != model.ChatGroup && ch.ChatType != model.ChatSupergroup {
		return nil
	}
	existing, err := s.store.GetChatByTelegramID(ctx, ch.ChatID)
	switch {
	case err == nil:
		if err := s.store.UpdateChatStatus(ctx, existing.ID, ch.NewStatus, ch.ChatTitle); err != nil {
			return fmt.Errorf("chat.BotStatusChanged: %w", err)
		}
		s.logger.Info("chat status updated", slog.Int64("chat_id", int64(existing.ID)), slog.String("status", string(ch.NewStatus)))
		return nil
	case !errors.Is(err, model.ErrNotFound):
		return fmt.Errorf("chat.BotStatusChanged: %w", err)
	}
	if ch.NewStatus == model.BotLeft || ch.From.ID == 0 {
		return nil
	}
	owner, err := s.store.UpsertUser(ctx, ch.From)
	if err != nil {
		return fmt.Errorf("chat.BotStatusChanged: owner: %w", err)
	}
	created, err := s.store.CreateChat(ctx, model.Chat{
		TelegramChatID: ch.ChatID, Type: ch.ChatType, Title: ch.ChatTitle, OwnerUserID: owner.ID, BotStatus: ch.NewStatus,
	})
	if err != nil {
		return fmt.Errorf("chat.BotStatusChanged: %w", err)
	}
	s.logger.Info("chat registered", slog.Int64("chat_id", int64(created.ID)), slog.Int64("owner_user_id", int64(owner.ID)))
	return nil
}

// Ensure возвращает чат группы, регистрируя его по первой команде: владельцем становится автор,
// если он администратор группы Telegram (isAdmin); иначе ErrUnknown. created — чат зарегистрирован этим вызовом.
func (s *Service) Ensure(ctx context.Context, in model.Incoming, author model.User, isAdmin func(ctx context.Context) (bool, error)) (c model.Chat, created bool, err error) {
	c, err = s.store.GetChatByTelegramID(ctx, in.ChatID)
	if err == nil {
		return c, false, nil
	}
	if !errors.Is(err, model.ErrNotFound) {
		return model.Chat{}, false, fmt.Errorf("chat.Ensure: %w", err)
	}
	admin, err := isAdmin(ctx)
	if err != nil {
		return model.Chat{}, false, fmt.Errorf("chat.Ensure: admin check: %w", err)
	}
	if !admin {
		return model.Chat{}, false, ErrUnknown
	}
	c, err = s.store.CreateChat(ctx, model.Chat{TelegramChatID: in.ChatID, Type: in.ChatType, OwnerUserID: author.ID, BotStatus: model.BotMember})
	if err != nil {
		return model.Chat{}, false, fmt.Errorf("chat.Ensure: %w", err)
	}
	s.logger.Info("chat registered by command", slog.Int64("chat_id", int64(c.ID)), slog.Int64("owner_user_id", int64(author.ID)))
	return c, true, nil
}

// OfTelegram возвращает зарегистрированный чат по идентификатору Telegram.
func (s *Service) OfTelegram(ctx context.Context, id model.TelegramChatID) (model.Chat, error) {
	c, err := s.store.GetChatByTelegramID(ctx, id)
	if err != nil {
		return model.Chat{}, fmt.Errorf("chat.OfTelegram: %w", err)
	}
	return c, nil
}

// CanManage сообщает, может ли пользователь управлять играми чата: владелец или модератор из списка.
func (s *Service) CanManage(ctx context.Context, chat model.Chat, user model.UserID) (bool, error) {
	if chat.OwnerUserID == user {
		return true, nil
	}
	ok, err := s.store.IsModerator(ctx, chat.ID, user)
	if err != nil {
		return false, fmt.Errorf("chat.CanManage: %w", err)
	}
	return ok, nil
}

// SetOwner передаёт владение; доступно только текущему владельцу.
func (s *Service) SetOwner(ctx context.Context, chat model.Chat, actor model.UserID, newOwner model.User) error {
	if chat.OwnerUserID != actor {
		return ErrForbidden
	}
	if err := s.store.SetChatOwner(ctx, chat.ID, newOwner.ID); err != nil {
		return fmt.Errorf("chat.SetOwner: %w", err)
	}
	return nil
}

// AddModerator назначает модератора; доступно только владельцу.
func (s *Service) AddModerator(ctx context.Context, chat model.Chat, actor model.UserID, user model.User) error {
	if chat.OwnerUserID != actor {
		return ErrForbidden
	}
	if err := s.store.AddModerator(ctx, chat.ID, user.ID, actor); err != nil {
		return fmt.Errorf("chat.AddModerator: %w", err)
	}
	return nil
}

// RemoveModerator снимает модератора; доступно только владельцу.
func (s *Service) RemoveModerator(ctx context.Context, chat model.Chat, actor model.UserID, user model.User) error {
	if chat.OwnerUserID != actor {
		return ErrForbidden
	}
	if err := s.store.RemoveModerator(ctx, chat.ID, user.ID); err != nil {
		return fmt.Errorf("chat.RemoveModerator: %w", err)
	}
	return nil
}

// Info возвращает чат с владельцем и модераторами.
func (s *Service) Info(ctx context.Context, chat model.Chat) (model.ChatInfo, error) {
	owner, err := s.store.GetUser(ctx, chat.OwnerUserID)
	if err != nil {
		return model.ChatInfo{}, fmt.Errorf("chat.Info: owner: %w", err)
	}
	mods, err := s.store.ListModerators(ctx, chat.ID)
	if err != nil {
		return model.ChatInfo{}, fmt.Errorf("chat.Info: %w", err)
	}
	return model.ChatInfo{Chat: chat, Owner: owner, Moderators: mods}, nil
}
