package engine

import (
	"encoding/json"
	"fmt"

	"taletogether/internal/model"
)

// Apply сворачивает события в состояние снимка: персонажи (локация, HP, состояния, знания, статус) и объекты локаций.
// Состояние после раунда равно Apply(снимок до раунда, события) — критерий пункта 07.
func Apply(snap *model.RoundSnapshot, events []model.Event) error {
	for _, ev := range events {
		var p map[string]any
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return fmt.Errorf("engine.Apply: event %d: %w", ev.Seq, err)
		}
		switch ev.Type {
		case model.EventCharacterMoved:
			if c := char(snap, p, "character_id"); c != nil {
				to := model.LocationID(num(p, "to"))
				c.LocationID = &to
			}
		case model.EventConditionAdded:
			if c := char(snap, p, "character_id"); c != nil && !c.HasCondition(str(p, "condition")) {
				c.Conditions = append(c.Conditions, str(p, "condition"))
			}
		case model.EventConditionRemoved:
			if c := char(snap, p, "character_id"); c != nil {
				c.Conditions = without(c.Conditions, str(p, "condition"))
			}
		case model.EventHPChanged:
			if c := char(snap, p, "character_id"); c != nil {
				c.HP = int(num(p, "to"))
			}
		case model.EventFactLearned:
			if c := char(snap, p, "character_id"); c != nil {
				c.Knowledge = appendUnique(c.Knowledge, str(p, "fact"))
			}
		case model.EventCharacterDied:
			if c := char(snap, p, "character_id"); c != nil {
				c.Status = model.CharacterDead
			}
		case model.EventCharacterReturned:
			if c := char(snap, p, "character_id"); c != nil {
				c.Status, c.HP, c.Conditions, c.DeathRoundsLeft = model.CharacterActive, int(num(p, "hp")), []string{}, 0
			}
		case model.EventObjectChanged:
			loc, ok := snap.Location(model.LocationID(num(p, "location_id")))
			if !ok {
				continue
			}
			if _, i, ok := loc.Object(str(p, "object")); ok {
				if v, has := p["to"]; has {
					loc.Objects[i].State = fmt.Sprint(v)
				}
				if v, has := p["revealed"]; has && v == true {
					loc.Objects[i].Revealed = true
				}
			}
		}
	}
	return nil
}

func char(snap *model.RoundSnapshot, p map[string]any, key string) *model.SnapshotCharacter {
	c, _ := snap.Character(model.CharacterID(num(p, key)))
	return c
}

func num(p map[string]any, key string) float64 {
	v, _ := p[key].(float64)
	return v
}

func str(p map[string]any, key string) string {
	v, _ := p[key].(string)
	return v
}

func without(list []string, v string) []string {
	out := list[:0:0]
	for _, it := range list {
		if it != v {
			out = append(out, it)
		}
	}
	return out
}
