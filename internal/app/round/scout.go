package round

import (
	"errors"

	"taletogether/internal/model"
)

// ErrNoLocation — персонаж ещё не стоит в клетке мира: разведка возможна после старта игры.
var ErrNoLocation = errors.New("персонаж ещё не в мире: разведка доступна после старта игры")

// ScoutText — ответ `/scout` (B36, ADR-0025): клетка персонажа и четыре направления, направления и ключи строк жирным
// (B39, B40, ADR-0029). Видимый сосед — имя (у карточки ещё её предложение), скрытый — «неизведанная территория» с видом издалека,
// направление без клетки — «неизведанная территория». Вид преграды непроходимых границ добавит пункт 36.
func ScoutText(c model.Character, locations []model.Location) (model.FormattedText, error) {
	if c.LocationID == nil {
		return model.FormattedText{}, ErrNoLocation
	}
	var here *model.Location
	byXY := map[[2]int]model.Location{}
	for i := range locations {
		byXY[[2]int{locations[i].X, locations[i].Y}] = locations[i]
		if locations[i].ID == *c.LocationID {
			here = &locations[i]
		}
	}
	if here == nil {
		return model.FormattedText{}, ErrNoLocation
	}
	var b model.TextBuilder
	b.Key("Разведка")
	b.Printf("%s (%d, %d)\n", here.Name, here.X, here.Y)
	for _, d := range model.Directions {
		dx, dy := d.Offset()
		n, ok := byXY[[2]int{here.X + dx, here.Y + dy}]
		line := model.UnknownTerritory
		if ok {
			line = n.Glimpse()
			if !n.Hidden && n.Stage == model.StageCard && n.Description != "" {
				line += " — " + n.Description
			}
		}
		b.WriteString("\n• ")
		b.Bold(d.Toward())
		b.WriteString(": " + line)
	}
	b.WriteString("\n\n")
	b.Key("Переход")
	b.WriteString("напишите, куда идёт персонаж, — направление или название места.")
	return b.Build(), nil
}
