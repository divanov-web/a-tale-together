package game

import (
	"context"
	"fmt"

	"taletogether/internal/model"
	"taletogether/internal/service/party"
)

// PartyInfo — группа с участниками для ответов бота; лидер не раскрывается.
type PartyInfo struct {
	Party   model.Party
	Members []model.Character
}

// Parties возвращает активные группы активного сезона с участниками по порядку создания.
func (s *Service) Parties(ctx context.Context, game model.Game) ([]PartyInfo, error) {
	season, err := s.store.ActiveSeason(ctx, game.ID)
	if err != nil {
		return nil, fmt.Errorf("game.Parties: season: %w", err)
	}
	parties, err := s.store.ListParties(ctx, game.ID, season.ID)
	if err != nil {
		return nil, fmt.Errorf("game.Parties: %w", err)
	}
	chars, err := s.store.ListCharacters(ctx, game.ID, season.ID)
	if err != nil {
		return nil, fmt.Errorf("game.Parties: %w", err)
	}
	var out []PartyInfo
	for _, p := range parties {
		if p.Status != model.PartyActive {
			continue
		}
		info := PartyInfo{Party: p}
		for _, c := range chars {
			if c.PartyID != nil && *c.PartyID == p.ID {
				info.Members = append(info.Members, c)
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// assignParty определяет группу нового персонажа в идущей игре (ADR-0017: группа обязательна, команд игрока нет):
// группа его стартовой локации, иначе новая группа в пределах `max_parties` с ним как лидером, иначе ближайшая по
// выходам группа — персонаж входит в историю в её локации. Группы без присутствующих распускаются, освобождая место.
// В черновике группы собираются при `/begin`.
func (s *Service) assignParty(ctx context.Context, game model.Game, genre model.Genre, c *model.Character) error {
	if game.Status == model.GameDraft || c.LocationID == nil {
		return nil
	}
	parties, err := s.store.ListParties(ctx, game.ID, c.SeasonID)
	if err != nil {
		return err
	}
	chars, err := s.store.ListCharacters(ctx, game.ID, c.SeasonID)
	if err != nil {
		return err
	}
	for _, stale := range party.Stale(parties, chars) {
		stale.Status, stale.LeaderCharacterID = model.PartyDisbanded, nil
		if err := s.store.UpdateParty(ctx, stale); err != nil {
			return err
		}
		for i := range parties {
			if parties[i].ID == stale.ID {
				parties[i] = stale
			}
		}
	}
	locations, err := s.store.ListLocations(ctx, game.ID)
	if err != nil {
		return err
	}
	d, ok := party.Assign(parties, locations, *c.LocationID, game.Settings.MaxParties)
	if !ok {
		return nil
	}
	if d.Create {
		taken := make([]string, 0, len(parties))
		for _, p := range parties {
			if p.Status == model.PartyActive {
				taken = append(taken, p.Name)
			}
		}
		created, err := s.store.CreateParty(ctx, model.Party{GameID: game.ID, SeasonID: c.SeasonID, Name: party.NextName(genre.Template.PartyNames, taken), Status: model.PartyActive, LocationID: c.LocationID})
		if err != nil {
			return err
		}
		c.PartyID = &created.ID
		return nil
	}
	id := d.Join
	loc := d.Location
	c.PartyID, c.LocationID = &id, &loc
	return nil
}
