package model

import "time"

// CooldownAction — действие с ограничением частоты (docs/game/character.md «Перезарядки», ADR-0033).
type CooldownAction string

// Действия с перезарядкой: у персонажа — описания и смена имени (команды смены имени пока нет, счётчик заведён);
// у фракции — просьба о подсказке рассказчика (пункт 35).
const (
	CooldownBio    CooldownAction = "bio"
	CooldownSecret CooldownAction = "secret"
	CooldownRename CooldownAction = "rename"
	CooldownHint   CooldownAction = "hint"
)

// CooldownOwner — владелец перезарядки.
type CooldownOwner string

// Владельцы перезарядки: персонаж (`owner_id` = `characters.id`) или фракция (`owner_id` = `factions.id`).
const (
	CooldownCharacter CooldownOwner = "character"
	CooldownFaction   CooldownOwner = "faction"
)

// Cooldown — последнее выполнение действия владельцем: номер раунда игры, в котором оно выполнено (`cooldowns`).
type Cooldown struct {
	GameID    GameID
	Owner     CooldownOwner
	OwnerID   int64
	Action    CooldownAction
	LastRound int
	UpdatedAt time.Time
}
