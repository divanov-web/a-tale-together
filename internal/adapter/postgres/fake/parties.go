package fake

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"taletogether/internal/model"
)

// CreateParty — см. postgres.Store; воспроизводит уникальность имени активной группы в сезоне и явный ID.
func (s *Store) CreateParty(_ context.Context, p model.Party) (model.Party, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return model.Party{}, s.Err
	}
	if p.Status == "" {
		p.Status = model.PartyActive
	}
	for _, ex := range s.parties {
		if ex.ID == p.ID || (ex.SeasonID == p.SeasonID && ex.Status == model.PartyActive && p.Status == model.PartyActive && strings.EqualFold(ex.Name, p.Name)) {
			return model.Party{}, fmt.Errorf("fake.CreateParty: unique violation")
		}
	}
	if p.ID == 0 {
		p.ID = model.PartyID(s.id())
	}
	p.CreatedAt = s.now()
	s.parties[p.ID] = p
	return p, nil
}

// UpdateParty — см. postgres.Store.
func (s *Store) UpdateParty(_ context.Context, p model.Party) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	ex, ok := s.parties[p.ID]
	if !ok || ex.GameID != p.GameID {
		return model.ErrNotFound
	}
	ex.Name, ex.Status, ex.LeaderCharacterID, ex.LocationID, ex.Motto, ex.Summary = p.Name, p.Status, p.LeaderCharacterID, p.LocationID, p.Motto, p.Summary
	s.parties[p.ID] = ex
	return nil
}

// ListParties — см. postgres.Store.
func (s *Store) ListParties(_ context.Context, game model.GameID, season model.SeasonID) ([]model.Party, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	var out []model.Party
	for _, p := range s.parties {
		if p.GameID == game && p.SeasonID == season {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SetCharacterParty — см. postgres.Store.
func (s *Store) SetCharacterParty(_ context.Context, game model.GameID, id model.CharacterID, party *model.PartyID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	c, ok := s.chars[id]
	if !ok || c.GameID != game {
		return model.ErrNotFound
	}
	if party != nil {
		if p, ok := s.parties[*party]; !ok || p.GameID != game {
			return fmt.Errorf("fake.SetCharacterParty: foreign key parties")
		}
	}
	c.PartyID = party
	s.chars[id] = c
	return nil
}

// ReservePartyIDs — см. postgres.Store.
func (s *Store) ReservePartyIDs(_ context.Context, n int) ([]model.PartyID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	var out []model.PartyID
	for i := 0; i < n; i++ {
		out = append(out, model.PartyID(s.id()))
	}
	return out, nil
}
