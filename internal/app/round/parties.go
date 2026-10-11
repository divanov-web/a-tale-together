package round

import (
	"context"
	"fmt"

	"taletogether/internal/model"
	"taletogether/internal/service/party"
)

// ensureDefaultParty собирает персонажей без группы при `/begin` (пункт 19): нет активных групп — создаётся группа
// по умолчанию с именем из шаблона жанра, лидер — персонаж администратора игры, иначе первый созданный; группы уже
// есть — персонажи без группы входят в старейшую. Группа получает локацию лидера.
func (s *Service) ensureDefaultParty(ctx context.Context, g model.Game, season model.SeasonID, chars []model.Character, genre model.Genre) error {
	parties, err := s.store.ListParties(ctx, g.ID, season)
	if err != nil {
		return err
	}
	var target *model.Party
	for i := range parties {
		if parties[i].Status == model.PartyActive {
			target = &parties[i]
			break
		}
	}
	var loose []model.Character
	for _, c := range chars {
		if c.PartyID == nil {
			loose = append(loose, c)
		}
	}
	if len(loose) == 0 {
		return nil
	}
	if target == nil {
		leader, _ := party.Leader(loose, g.CreatedByUserID)
		var location *model.LocationID
		for _, c := range loose {
			if c.ID == leader {
				location = c.LocationID
			}
		}
		created, err := s.store.CreateParty(ctx, model.Party{GameID: g.ID, SeasonID: season, Name: party.NextName(genre.Template.PartyNames, nil), Status: model.PartyActive, LeaderCharacterID: &leader, LocationID: location})
		if err != nil {
			return err
		}
		target = &created
	}
	for _, c := range loose {
		if err := s.store.SetCharacterParty(ctx, g.ID, c.ID, &target.ID); err != nil {
			return err
		}
	}
	return nil
}

// snapshotParties дополняет снимок группами, лимитами и резервом для новых групп: идентификаторы берутся из
// последовательности БД заранее (не больше свободных мест в лимите), чтобы `party_split` был воспроизводим по снимку.
func (s *Service) snapshotParties(ctx context.Context, g model.Game, season model.SeasonID, genre model.Genre, snap *model.RoundSnapshot) error {
	parties, err := s.store.ListParties(ctx, g.ID, season)
	if err != nil {
		return err
	}
	snap.Parties = nil
	for _, p := range parties {
		if p.Status == model.PartyActive {
			snap.Parties = append(snap.Parties, model.SnapshotParty{ID: p.ID, Name: p.Name, LeaderID: p.LeaderCharacterID})
		}
	}
	snap.MaxParties, snap.MaxActiveLocations, snap.PartyNames = g.Settings.MaxParties, g.Settings.MaxActiveLocations, genre.Template.PartyNames
	free := g.Settings.MaxParties - len(snap.Parties)
	if g.Settings.MaxParties <= 0 {
		free = len(snap.Characters) // без лимита: каждый персонаж может оказаться в своей группе
	}
	if free > 0 {
		if snap.NewPartyIDs, err = s.store.ReservePartyIDs(ctx, free); err != nil {
			return err
		}
	}
	return nil
}

// saveParties записывает группы после свёртки: новые группы из `party_split` (до персонажей: внешний ключ), затем
// локация и лидер каждой активной группы по правилу преемственности (лидер вышел, выбыл или умер → персонаж
// администратора, иначе первый созданный из присутствующих); группа без участников распускается.
func (s *Service) saveParties(ctx context.Context, g model.Game, season model.SeasonID, before, after *model.RoundSnapshot, chars []model.Character) error {
	known := map[model.PartyID]bool{}
	for _, p := range before.Parties {
		known[p.ID] = true
	}
	for _, p := range after.Parties {
		if known[p.ID] {
			continue
		}
		var location *model.LocationID
		if p.LeaderID != nil {
			if c, ok := after.Character(*p.LeaderID); ok {
				location = c.LocationID
			}
		}
		if _, err := s.store.CreateParty(ctx, model.Party{ID: p.ID, GameID: g.ID, SeasonID: season, Name: p.Name, Status: model.PartyActive, LeaderCharacterID: p.LeaderID, LocationID: location}); err != nil {
			return fmt.Errorf("party %d: %w", p.ID, err)
		}
	}
	return nil
}

// syncParties выравнивает группы с состоянием персонажей после раунда или команды: лидер, локация, роспуск пустых.
func (s *Service) syncParties(ctx context.Context, g model.Game, season model.SeasonID) error {
	parties, err := s.store.ListParties(ctx, g.ID, season)
	if err != nil {
		return err
	}
	chars, err := s.store.ListCharacters(ctx, g.ID, season)
	if err != nil {
		return err
	}
	for _, p := range parties {
		if p.Status != model.PartyActive {
			continue
		}
		var members []model.Character
		for _, c := range chars {
			if c.PartyID != nil && *c.PartyID == p.ID {
				members = append(members, c)
			}
		}
		updated := p
		if len(members) == 0 {
			updated.Status = model.PartyDisbanded
			updated.LeaderCharacterID = nil
		} else {
			valid := false
			for _, m := range members {
				if p.LeaderCharacterID != nil && m.ID == *p.LeaderCharacterID && !m.Status.Absent() {
					valid = true
				}
			}
			if !valid {
				if leader, ok := party.Leader(members, g.CreatedByUserID); ok {
					updated.LeaderCharacterID = &leader
				}
			}
			for _, m := range members {
				if updated.LeaderCharacterID != nil && m.ID == *updated.LeaderCharacterID {
					updated.LocationID = m.LocationID
				}
			}
		}
		if updated.Status != p.Status || !equalID(updated.LeaderCharacterID, p.LeaderCharacterID) || !equalLoc(updated.LocationID, p.LocationID) {
			if err := s.store.UpdateParty(ctx, updated); err != nil {
				return err
			}
		}
	}
	return nil
}

func equalID(a, b *model.CharacterID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func equalLoc(a, b *model.LocationID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
