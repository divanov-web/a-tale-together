package model

import "time"

// PartyStatus — статус группы персонажей (docs/game/models/party.md).
type PartyStatus string

// Статусы группы: распущенная в лимите `max_parties` не считается.
const (
	PartyActive    PartyStatus = "active"
	PartyDisbanded PartyStatus = "disbanded"
)

// Лимиты имени группы.
const (
	PartyNameMin = 2
	PartyNameMax = 30
)

// LeaderAttribute — характеристика, по которой движок выбирает лидера отделившейся группы (наибольшая Воля; пункт 19).
const LeaderAttribute = "will"

// Party — группа персонажей сезона с общей сюжетной линией (docs/game/parties.md). Лидер на этапе пункта 19 скрыт
// от игроков: не показывается в `/party`, `/status`, `/character` и хронике; движок засчитывает его направление,
// когда лимит групп или локаций не даёт участникам разойтись.
type Party struct {
	ID                PartyID
	GameID            GameID
	SeasonID          SeasonID
	Name              string
	Status            PartyStatus
	LeaderCharacterID *CharacterID
	LocationID        *LocationID // текущая локация группы; у участников совпадает
	Motto             string
	Summary           string
	CreatedAt         time.Time
}

// SnapshotParty — группа в снимке раунда: имя для сцен рассказчика и лидер для приоритета направления.
type SnapshotParty struct {
	ID       PartyID      `json:"id"`
	Name     string       `json:"name"`
	LeaderID *CharacterID `json:"leader_id,omitempty"`
}

// Причины удержания группы (`party_held`).
const (
	HeldPartyLimit    = "party_limit"    // активных групп уже `max_parties`
	HeldLocationLimit = "location_limit" // разделение заняло бы больше `max_active_locations` локаций
)

// OutcomeHeld — исход заявки на перемещение участника, которого группа удержала: он идёт туда же, куда группа.
const OutcomeHeld = "held"
