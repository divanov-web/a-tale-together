package model

// UserID — идентификатор пользователя в таблице users.
type UserID int64

// ChatID — идентификатор чата в таблице chats.
type ChatID int64

// GameID — идентификатор игры.
type GameID int64

// SeasonID — идентификатор сезона игры.
type SeasonID int64

// GenreID — идентификатор жанра из справочника.
type GenreID int64

// CharacterModelID — идентификатор модели персонажа из справочника.
type CharacterModelID int64

// FactionID — идентификатор фракции игры.
type FactionID int64

// PartyID — идентификатор группы персонажей.
type PartyID int64

// CharacterID — идентификатор персонажа.
type CharacterID int64

// LocationID — идентификатор локации.
type LocationID int64

// RoundID — идентификатор раунда.
type RoundID int64

// SubmissionID — идентификатор заявки на раунд.
type SubmissionID int64

// OutboxID — идентификатор исходящего сообщения в messages_outbox.
type OutboxID int64
