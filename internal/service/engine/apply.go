package engine

import (
	"encoding/json"
	"fmt"

	"taletogether/internal/model"
)

// Apply сворачивает события в состояние снимка: персонажи (локация, группа, HP, состояния, знания, статус), группы
// (`party_split` добавляет группу), объекты локаций, клетки (`character_moved` отмечает посещение, `cell_discovered`
// добавляет фракцию, `cell_generated` создаёт или детализирует клетку, `object_created` добавляет объект; пункт 27),
// награда этапа квеста (`quest_stage_done`: `reward.fact` в знания адресатов события; пункт 28).
// `party_held` состояние не меняет: переходы записаны отдельно. Состояние после раунда равно
// Apply(снимок до раунда, события) — критерий пункта 07.
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
				if l, ok := snap.Location(to); ok {
					l.Visited = true
				}
			}
		case model.EventCellDiscovered:
			if l, ok := snap.Location(model.LocationID(num(p, "location_id"))); ok {
				if f := model.FactionID(num(p, "faction_id")); f != 0 && !l.Discovered(f) {
					l.DiscoveredBy = append(l.DiscoveredBy, f)
				}
			}
		case model.EventCellGenerated:
			var cell struct {
				LocationID  model.LocationID    `json:"location_id"`
				X           int                 `json:"x"`
				Y           int                 `json:"y"`
				Name        string              `json:"name"`
				Description string              `json:"description"`
				Difficulty  int                 `json:"difficulty"`
				Terrain     string              `json:"terrain"`
				Stage       model.LocationStage `json:"stage"`
			}
			if err := json.Unmarshal(ev.Payload, &cell); err != nil {
				return fmt.Errorf("engine.Apply: event %d: %w", ev.Seq, err)
			}
			if l, ok := snap.Location(cell.LocationID); ok {
				l.Name, l.Description, l.Difficulty, l.Terrain, l.Stage = cell.Name, cell.Description, cell.Difficulty, cell.Terrain, cell.Stage
			} else {
				snap.Locations = append(snap.Locations, model.SnapshotLocation{ID: cell.LocationID, X: cell.X, Y: cell.Y, Name: cell.Name, Description: cell.Description, Difficulty: cell.Difficulty, Terrain: cell.Terrain, Stage: cell.Stage, Objects: []model.LocationObject{}})
			}
		case model.EventQuestStageDone:
			var done struct {
				Reward model.StageReward `json:"reward"`
			}
			if err := json.Unmarshal(ev.Payload, &done); err != nil {
				return fmt.Errorf("engine.Apply: event %d: %w", ev.Seq, err)
			}
			if done.Reward.Fact != "" {
				for _, id := range ev.VisibleTo {
					if c, ok := snap.Character(id); ok {
						c.Knowledge = appendUnique(c.Knowledge, done.Reward.Fact)
					}
				}
			}
		case model.EventObjectCreated:
			var created struct {
				LocationID model.LocationID     `json:"location_id"`
				Object     model.LocationObject `json:"object"`
			}
			if err := json.Unmarshal(ev.Payload, &created); err != nil {
				return fmt.Errorf("engine.Apply: event %d: %w", ev.Seq, err)
			}
			if l, ok := snap.Location(created.LocationID); ok {
				if _, _, exists := l.Object(created.Object.Key); !exists {
					l.Objects = append(l.Objects, created.Object)
				}
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
		case model.EventPartySplit:
			var sp struct {
				NewPartyID model.PartyID       `json:"new_party_id"`
				NewParty   string              `json:"new_party"`
				LeaderID   model.CharacterID   `json:"leader_id"`
				Members    []model.CharacterID `json:"members"`
			}
			if err := json.Unmarshal(ev.Payload, &sp); err != nil {
				return fmt.Errorf("engine.Apply: event %d: %w", ev.Seq, err)
			}
			if _, exists := snap.Party(sp.NewPartyID); !exists {
				applySplit(snap, sp.NewPartyID, sp.NewParty, sp.LeaderID, sp.Members)
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
