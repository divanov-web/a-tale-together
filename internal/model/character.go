package model

import "time"

// AttributeDef — характеристика в модели персонажа.
type AttributeDef struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Min   int    `json:"min"`
	Max   int    `json:"max"`
}

// SkillDef — навык в модели персонажа; Attribute — ключ характеристики, к которой он прибавляется.
type SkillDef struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Attribute string `json:"attribute"`
}

// ArchetypeDef — архетип: рекомендуемое распределение и стартовые предметы.
type ArchetypeDef struct {
	Key        string         `json:"key"`
	Title      string         `json:"title"`
	Attributes map[string]int `json:"attributes"`
	Skills     map[string]int `json:"skills"`
	Items      []string       `json:"items"`
}

// CharacterModelDef — модель персонажа как данные (docs/game/models/character-basic.md).
type CharacterModelDef struct {
	SchemaVersion   int            `json:"schema_version"`
	Attributes      []AttributeDef `json:"attributes"`
	AttributePoints int            `json:"attribute_points"`
	Skills          []SkillDef     `json:"skills"`
	SkillPoints     int            `json:"skill_points"`
	SkillMax        int            `json:"skill_max"`
	HPFormula       string         `json:"hp_formula"`
	InventoryLimit  int            `json:"inventory_limit"`
	Archetypes      []ArchetypeDef `json:"archetypes"`
}

// Attribute возвращает характеристику модели по ключу.
func (d CharacterModelDef) Attribute(key string) (AttributeDef, bool) {
	for _, a := range d.Attributes {
		if a.Key == key {
			return a, true
		}
	}
	return AttributeDef{}, false
}

// Skill возвращает навык модели по ключу.
func (d CharacterModelDef) Skill(key string) (SkillDef, bool) {
	for _, s := range d.Skills {
		if s.Key == key {
			return s, true
		}
	}
	return SkillDef{}, false
}

// Archetype возвращает архетип модели по ключу.
func (d CharacterModelDef) Archetype(key string) (ArchetypeDef, bool) {
	for _, a := range d.Archetypes {
		if a.Key == key {
			return a, true
		}
	}
	return ArchetypeDef{}, false
}

// CharacterModel — запись справочника моделей персонажа.
type CharacterModel struct {
	ID           CharacterModelID
	Code         string
	RulesVersion int
	Definition   CharacterModelDef
}

// CharacterStatus — статус персонажа по активности (docs/game/character.md).
type CharacterStatus string

// Статусы персонажа: waiting — 1–2 пропуска, присутствует без действий; leaving — ровно `inactive_after_rounds` пропусков,
// один раунд: рассказчик выводит персонажа из сюжета; inactive — дальше, в контекст рассказчика не попадает (B13);
// dead — выбыл по режиму смерти до возврата (docs/game/death.md).
const (
	CharacterActive   CharacterStatus = "active"
	CharacterWaiting  CharacterStatus = "waiting"
	CharacterLeaving  CharacterStatus = "leaving"
	CharacterInactive CharacterStatus = "inactive"
	CharacterDead     CharacterStatus = "dead"
)

// Absent — персонаж вне повествования: выведен за пропуски или выбыл по смерти; в сцены рассказчика не попадает.
func (s CharacterStatus) Absent() bool { return s == CharacterInactive || s == CharacterDead }

// ItemBonus — бонус предмета к навыку: очки к параметру проверки (суммарно не более 2, docs/game/checks.md).
type ItemBonus struct {
	Skill  string `json:"skill"`
	Points int    `json:"points"`
}

// Item — предмет инвентаря персонажа; запись каталога, не свободный текст.
type Item struct {
	Key   string     `json:"key"`
	Title string     `json:"title"`
	Tags  []string   `json:"tags,omitempty"`
	Bonus *ItemBonus `json:"bonus,omitempty"`
}

// Character — персонаж пользователя в сезоне игры.
type Character struct {
	ID              CharacterID
	GameID          GameID
	SeasonID        SeasonID
	UserID          UserID
	ModelID         CharacterModelID
	Nickname        string
	Archetype       string
	PublicBio       string
	SecretBio       string
	Status          CharacterStatus
	MissedRounds    int
	DeathRoundsLeft int // раундов до возврата после ненастоящей смерти
	DiedAt          *time.Time
	FactionID       FactionID
	PartyID         *PartyID
	HP              int
	Conditions      []string
	Inventory       []Item
	LocationID      *LocationID
	Knowledge       []string // факты, известные персонажу (события fact_learned)
	XP              int
	Attributes      map[string]int // значения по ключам модели (character_stats kind=attribute)
	Skills          map[string]int // значения по ключам модели (character_stats kind=skill)
	CreatedAt       time.Time
}

// CharacterDraft — данные для создания персонажа после диалога в личке.
type CharacterDraft struct {
	FactionCode FactionCode
	Nickname    string
	Archetype   string
	Attributes  map[string]int
	Skills      map[string]int
	PublicBio   string
	SecretBio   string
}
