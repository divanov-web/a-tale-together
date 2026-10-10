// Package visibility — какие события доступны адресату: публичные всем, локационные присутствующим, приватные по списку.
package visibility

import "taletogether/internal/model"

// Viewer — адресат: персонаж и его локация на момент раунда (для location-событий).
type Viewer struct {
	CharacterID model.CharacterID
	LocationID  *model.LocationID
}

// Filter возвращает события, доступные адресату, в исходном порядке.
func Filter(events []model.Event, v Viewer) []model.Event {
	out := make([]model.Event, 0, len(events))
	for _, ev := range events {
		if Visible(ev, v) {
			out = append(out, ev)
		}
	}
	return out
}

// Visible сообщает, доступно ли событие адресату.
func Visible(ev model.Event, v Viewer) bool {
	switch ev.Visibility {
	case model.VisibilityPublic:
		return true
	case model.VisibilityLocation:
		return v.LocationID != nil && ev.LocationID != nil && *v.LocationID == *ev.LocationID
	case model.VisibilityPrivate:
		for _, id := range ev.VisibleTo {
			if id == v.CharacterID {
				return true
			}
		}
	}
	return false
}

// Public возвращает события для хроники: публичные и локационные (сводно по локациям), без приватных.
func Public(events []model.Event) []model.Event {
	out := make([]model.Event, 0, len(events))
	for _, ev := range events {
		if ev.Visibility == model.VisibilityPublic || ev.Visibility == model.VisibilityLocation {
			out = append(out, ev)
		}
	}
	return out
}
