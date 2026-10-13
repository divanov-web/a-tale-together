package round

import (
	"errors"
	"fmt"
	"strings"

	"taletogether/internal/model"
)

// ErrNoLocation — персонаж ещё не стоит в клетке мира: разведка возможна после старта игры.
var ErrNoLocation = errors.New("персонаж ещё не в мире: разведка доступна после старта игры")

// ScoutText — ответ `/scout` (B36, ADR-0025): клетка персонажа и четыре направления. Видимый сосед — имя (у карточки
// ещё её предложение), скрытый — «неизведанная территория» с видом издалека, направление без клетки — «неизведанная
// территория». Вид преграды непроходимых границ добавит пункт 36.
func ScoutText(c model.Character, locations []model.Location) (string, error) {
	if c.LocationID == nil {
		return "", ErrNoLocation
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
		return "", ErrNoLocation
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Разведка: %s (%d, %d)\n", here.Name, here.X, here.Y)
	for _, d := range model.Directions {
		dx, dy := d.Offset()
		n, ok := byXY[[2]int{here.X + dx, here.Y + dy}]
		line := d.Toward() + ": " + model.UnknownTerritory
		if ok {
			line = d.Toward() + ": " + n.Glimpse()
			if !n.Hidden && n.Stage == model.StageCard && n.Description != "" {
				line += " — " + n.Description
			}
		}
		b.WriteString("\n• " + line)
	}
	b.WriteString("\n\nПереход: напишите, куда идёт персонаж, — направление или название места.")
	return b.String(), nil
}
