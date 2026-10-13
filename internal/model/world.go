package model

import (
	"strings"
	"time"
)

// Direction — сторона света на сетке клеток (docs/game/world.md): север `(x, y+1)`, юг `(x, y−1)`, запад `(x−1, y)`,
// восток `(x+1, y)`. Геометрию, выходы и достижимость считает код; отдельного запроса «картограф» нет.
type Direction string

// Четыре направления в порядке вывода.
const (
	North Direction = "north"
	South Direction = "south"
	West  Direction = "west"
	East  Direction = "east"
)

// Directions — направления в порядке вывода игроку и модели.
var Directions = []Direction{North, South, West, East}

// Offset — смещение координат на один шаг в направлении.
func (d Direction) Offset() (dx, dy int) {
	switch d {
	case North:
		return 0, 1
	case South:
		return 0, -1
	case West:
		return -1, 0
	case East:
		return 1, 0
	}
	return 0, 0
}

// Opposite — обратное направление.
func (d Direction) Opposite() Direction {
	switch d {
	case North:
		return South
	case South:
		return North
	case West:
		return East
	case East:
		return West
	}
	return d
}

// Title — направление словом: «север», «юг», «запад», «восток».
func (d Direction) Title() string {
	switch d {
	case North:
		return "север"
	case South:
		return "юг"
	case West:
		return "запад"
	case East:
		return "восток"
	}
	return string(d)
}

// Toward — направление с предлогом: «на север», «на юг», «на запад», «на восток».
func (d Direction) Toward() string {
	return "на " + d.Title()
}

// directionWords — слова, по которым код узнаёт направление в цели заявки (типовые команды и кнопки).
var directionWords = map[string]Direction{
	"north": North, "n": North, "север": North, "севернее": North, "северу": North, "с": North,
	"south": South, "s": South, "юг": South, "южнее": South, "югу": South, "ю": South,
	"west": West, "w": West, "запад": West, "западнее": West, "западу": West, "з": West,
	"east": East, "e": East, "восток": East, "восточнее": East, "востоку": East, "в": East,
}

// ParseDirection находит направление в тексте цели: «north», «на север», «идти на север: Лес». Сравнение без
// учёта регистра по словам; однобуквенные сокращения принимаются только одним словом целиком («с», «n»).
func ParseDirection(target string) (Direction, bool) {
	fields := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(target)), func(r rune) bool {
		return r == ' ' || r == ':' || r == ',' || r == '—' || r == '-' || r == '.' || r == '«' || r == '»' || r == '(' || r == ')'
	})
	for _, w := range fields {
		d, ok := directionWords[w]
		if !ok {
			continue
		}
		if len([]rune(w)) == 1 && len(fields) != 1 {
			continue
		}
		return d, true
	}
	return "", false
}

// LocationStage — стадия клетки сетки: `card` — карточка соседа (тип местности, имя, одно предложение),
// `detailed` — описание, объекты и подходы. Клетка без строки в БД — стадия «none», только координаты.
type LocationStage string

// Стадии клетки.
const (
	StageCard     LocationStage = "card"
	StageDetailed LocationStage = "detailed"
)

// Лимиты генерации за раунд (docs/game/world.md, «Лимиты генерации за раунд»).
const (
	MaxCellsPerRound      = 4 // клеток `detailed` за раунд; сверх лимита — шаблонная клетка
	MaxObjectsPerCell     = 2
	MaxApproachesPerObj   = 3
	MaxCellNameRunes      = 60
	MaxCellDescRunes      = 600
	MaxCardRunes          = 200
	MaxObjectTitleRunes   = 60
	MaxFactRunes          = 200
	MaxApproachTitleRunes = 80
)

// Шаблонная клетка «дорога» и карточка соседа при сбое модели или сверх лимита (docs/game/world.md).
const (
	TemplateCellName    = "Дорога"
	TemplateCellTerrain = "road"
	TemplateCellDesc    = "Пыльная дорога уходит дальше; по сторонам ничего примечательного, но путь открыт во все стороны."
	TemplateCardName    = "Неизведанные земли"
	TemplateCardTerrain = "unknown"
	TemplateCardDesc    = "Что там, отсюда не разглядеть."
)

// UnknownTerritory — подпись скрытого соседа и направления без клетки для игрока и интерпретатора (ADR-0025).
const UnknownTerritory = "неизведанная территория"

// GenerationJob — сохранённый ответ модели по ключу задания (ADR-0019): игра, раунд (нет у завязки), роль и хэш
// входа. Пишется вне транзакции раунда, поэтому повтор раунда после отката берёт готовый ответ и не платит дважды.
type GenerationJob struct {
	ID        int64
	GameID    GameID
	RoundID   *RoundID
	Role      LLMRole
	InputHash string
	Response  LLMResponse
	CreatedAt time.Time
}
