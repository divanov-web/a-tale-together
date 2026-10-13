package model

import "time"

// GameFormat — долговечность истории (docs/game/game-formats.md).
type GameFormat string

// Форматы игры.
const (
	FormatGlobal GameFormat = "global"
	FormatNovel  GameFormat = "novel"
	FormatSeries GameFormat = "series"
)

// GameMode — пресет механик и стиля рассказчика (docs/game/game-modes.md).
type GameMode string

// Режимы игры.
const (
	ModeMechanics GameMode = "mechanics"
	ModeBalanced  GameMode = "balanced"
	ModeNarrative GameMode = "narrative"
)

// GameStatus — статус игры.
type GameStatus string

// Статусы игры: `setup` — администратор настраивает мир, персонажи не создаются (ADR-0023); `draft` — готова к набору.
const (
	GameSetup    GameStatus = "setup"
	GameDraft    GameStatus = "draft"
	GameActive   GameStatus = "active"
	GamePaused   GameStatus = "paused"
	GameFinished GameStatus = "finished"
)

// GameSettings — необязательные настройки мира в `games.settings` (jsonb).
type GameSettings struct {
	SchemaVersion       int        `json:"schema_version"`
	EndsAt              *time.Time `json:"ends_at,omitempty"`           // граница рассказа: дата
	MaxRounds           int        `json:"max_rounds,omitempty"`        // граница рассказа: число раундов; 0 — без границы
	MaxRoundPlayers     int        `json:"max_round_players"`           // сколько игроков обрабатывается за раунд; остальные — в очередь (этап 6 roadmap); админу не показывается
	MaxParties          int        `json:"max_parties"`                 // лимит групп в сезоне; группа держит одну локацию, поэтому это и лимит занятых локаций (ADR-0017)
	MaxStoryQuests      int        `json:"max_story_quests"`            // активных сюжетных квестов на фракцию (пункт 28, ADR-0020); 0 — по умолчанию
	MaxSideQuests       int        `json:"max_side_quests"`             // активных побочных квестов на фракцию; 0 — по умолчанию
	MaxActiveGoals      int        `json:"max_active_goals"`            // сюжетные цели (пункт 17)
	GoalCooldownRounds  int        `json:"goal_cooldown_rounds"`        // раундов до новой цели после отказа
	InactiveAfterRounds int        `json:"inactive_after_rounds"`       // пропусков подряд до статуса inactive
	MinRoundAnswers     int        `json:"min_round_answers"`           // кворум: меньше заявок — раунд пропускается
	AllowResubmit       bool       `json:"allow_resubmit"`              // разрешить менять заявку до дедлайна (не чаще раза в минуту); по умолчанию запрещено (решение руководителя 2026-10-10); настройка разработчика
	DeathMode           DeathMode  `json:"death_mode,omitempty"`        // режим смерти (docs/game/death.md); реализуется только fake; настройка разработчика
	DeathDays           int        `json:"death_days,omitempty"`        // срок выбытия при ненастоящей смерти, дней; настройка разработчика
	PauseAfterSkipped   int        `json:"pause_after_skipped"`         // пропущенных без кворума раундов подряд до автоматической паузы; 0 — выключено; настройка разработчика, админу не показывается (B11)
	ManualAttributes    bool       `json:"manual_attributes,omitempty"` // ручное распределение характеристик и навыков при создании персонажа; выключено: всегда автораспределение по архетипу в любом режиме (решение руководителя 2026-10-13, B30); настройка разработчика
}

// Кворум раунда по умолчанию и в режиме разработки.
const (
	MaxRoundPlayersDefault   = 20 // внутренняя настройка разработчика, не параметр мира
	MaxPartiesDefault        = 4  // групп в сезоне (решение руководителя 2026-10-11: 4) и, следовательно, занятых локаций (ADR-0017)
	MinRoundAnswersDefault   = 2
	PauseAfterSkippedDefault = 3 // автопауза после трёх пропущенных подряд раундов (B11)
	MinRoundAnswersDev       = 1
	MinRoundMinutes          = 5 // продуктовый минимум длительности раунда; TODO: на проде поднять до 10 (снижено до 5 для тестов, решение руководителя 2026-10-12)
	MinRoundMinutesDev       = 2 // песочница: короткие раунды для прогонов; не 1, дедлайн округляется вниз до минуты (B12)
	MaxRoundMinutes          = 60
)

// DefaultGameSettings — настройки нового мира по умолчанию.
func DefaultGameSettings() GameSettings {
	return GameSettings{
		SchemaVersion:       1,
		MaxRoundPlayers:     MaxRoundPlayersDefault,
		MaxParties:          MaxPartiesDefault,
		MaxStoryQuests:      MaxStoryQuestsDefault,
		MaxSideQuests:       MaxSideQuestsDefault,
		MaxActiveGoals:      1,
		GoalCooldownRounds:  3,
		InactiveAfterRounds: 3,
		MinRoundAnswers:     MinRoundAnswersDefault,
		DeathMode:           DeathFake,
		DeathDays:           DeathDaysDefault,
		PauseAfterSkipped:   PauseAfterSkippedDefault,
	}
}

// QuestLimits — лимиты активных квестов на фракцию; нули в настройках старых игр заменяются значениями по умолчанию.
func (s GameSettings) QuestLimits() (story, side int) {
	story, side = s.MaxStoryQuests, s.MaxSideQuests
	if story <= 0 {
		story = MaxStoryQuestsDefault
	}
	if side < 0 {
		side = MaxSideQuestsDefault
	}
	return story, side
}

// Game — запущенный мир, привязанный к чату.
type Game struct {
	ID              GameID
	ChatID          ChatID
	GenreID         GenreID
	Format          GameFormat
	Mode            GameMode
	Title           string
	Status          GameStatus
	Timezone        string
	PlayFrom        string // игровое окно, "HH:MM" по часовому поясу игры; пусто — без окна
	PlayTo          string
	RoundMinutes    int
	Settings        GameSettings
	RulesVersion    int
	LLMProfileID    *LLMProfileID // профиль LLM игры; nil — глобальный по умолчанию (ADR-0003)
	JoinCode        string
	CreatedByUserID UserID
	NextRoundAt     *time.Time
	SkippedStreak   int    // пропущенных без кворума раундов подряд; обнуляется заявками в раунде, возобновлением и сбросом (B11)
	OpeningScene    string // завязка истории при /begin: текст рассказчика или стартовая сцена шаблона жанра (ADR-0018); пусто до старта
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// MemberRole — роль участника игры.
type MemberRole string

// Роли участников.
const (
	RoleAdmin  MemberRole = "admin"
	RolePlayer MemberRole = "player"
)

// MemberStatus — статус участия.
type MemberStatus string

// Статусы участия.
const (
	MemberActive MemberStatus = "active"
	MemberLeft   MemberStatus = "left"
	MemberKicked MemberStatus = "kicked"
)

// GameMember — участие пользователя в игре.
type GameMember struct {
	GameID   GameID
	UserID   UserID
	Role     MemberRole
	Status   MemberStatus
	JoinedAt time.Time
}

// SeasonStatus — статус сезона.
type SeasonStatus string

// Статусы сезона.
const (
	SeasonActive   SeasonStatus = "active"
	SeasonFinished SeasonStatus = "finished"
)

// Season — сезон игры; у рассказа и глобальной игры единственный сезон 1.
type Season struct {
	ID         SeasonID
	GameID     GameID
	SeasonNo   int
	Title      string
	Status     SeasonStatus
	StartedAt  time.Time
	FinishedAt *time.Time
}

// FactionCode — стабильный ключ фракции.
type FactionCode string

// Стартовые коды фракций: две стороны конфликта. Число фракций администратору не показывается;
// новые фракции с произвольным кодом создаёт рассказчик по сюжету (поздний этап, план 13).
const (
	FactionGood FactionCode = "good"
	FactionEvil FactionCode = "evil"
)

// FactionCodes — стартовые фракции в порядке вывода; созданные по сюжету идут после них.
var FactionCodes = []FactionCode{FactionGood, FactionEvil}

// Faction — сторона конфликта в игре (docs/game/models/factions.md).
type Faction struct {
	ID          FactionID
	GameID      GameID
	Code        FactionCode
	Title       string
	Description string
	Tone        string
	Relations   map[FactionCode]string
	// ExploredSinceReset — счётчик исследования: клеток, открытых фракцией с последнего сброса (пункт 27; выдача квестов — пункт 28).
	ExploredSinceReset int
}

// FactionTemplate — фракция в шаблоне жанра.
type FactionTemplate struct {
	Code        FactionCode       `json:"code"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Tone        string            `json:"tone"`
	Relations   map[string]string `json:"relations"`
	Goal        string            `json:"goal,omitempty"` // цель фракции по умолчанию при сбое модели (пункт 28)
}

// ItemTemplate — предмет каталога жанра.
type ItemTemplate struct {
	Key   string     `json:"key"`
	Title string     `json:"title"`
	Tags  []string   `json:"tags"`
	Bonus *ItemBonus `json:"bonus,omitempty"`
}

// GenreTemplate — шаблон мира жанра (docs/game/models/genre.md).
type GenreTemplate struct {
	SchemaVersion  int                `json:"schema_version"`
	CharacterModel string             `json:"character_model"`
	Tone           string             `json:"tone"`
	Factions       []FactionTemplate  `json:"factions"`
	Locations      []LocationTemplate `json:"locations,omitempty"`
	OpeningScene   string             `json:"opening_scene"`
	Items          []ItemTemplate     `json:"items"`
	PartyNames     []string           `json:"party_names,omitempty"` // имена групп по умолчанию (пункт 19); исчерпаны — «Группа N»
	StoryQuest     *QuestTemplate     `json:"story_quest,omitempty"` // сюжетный квест при сбое модели (пункт 28)
}

// Genre — запись справочника жанров.
type Genre struct {
	ID               GenreID
	Code             string
	Title            string
	CharacterModelID CharacterModelID
	Template         GenreTemplate
	Enabled          bool
}

// Item возвращает предмет каталога по ключу.
func (t GenreTemplate) Item(key string) (ItemTemplate, bool) {
	for _, it := range t.Items {
		if it.Key == key {
			return it, true
		}
	}
	return ItemTemplate{}, false
}

// MemberInfo — участие вместе с данными пользователя для ответов бота.
type MemberInfo struct {
	Member GameMember
	User   User
}
