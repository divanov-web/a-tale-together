package fake

import (
	"context"
	"fmt"
	"sort"

	"taletogether/internal/model"
)

// CreateLocation — см. postgres.Store.
func (s *Store) CreateLocation(_ context.Context, l model.Location) (model.Location, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.locations {
		if ex.GameID == l.GameID && ex.Name == l.Name {
			return model.Location{}, fmt.Errorf("fake.CreateLocation: unique violation")
		}
	}
	l.ID = model.LocationID(s.id())
	l.CreatedAt = s.now()
	if l.Links == nil {
		l.Links = []model.LocationLink{}
	}
	if l.Objects == nil {
		l.Objects = []model.LocationObject{}
	}
	s.locations[l.ID] = l
	return l, nil
}

// ListLocations — см. postgres.Store.
func (s *Store) ListLocations(_ context.Context, game model.GameID) ([]model.Location, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Location
	for _, l := range s.locations {
		if l.GameID == game {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// UpdateLocationObjects — см. postgres.Store.
func (s *Store) UpdateLocationObjects(_ context.Context, game model.GameID, id model.LocationID, objects []model.LocationObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locations[id]
	if !ok || l.GameID != game {
		return model.ErrNotFound
	}
	l.Objects = objects
	s.locations[id] = l
	return nil
}

// NextEventSeq — см. postgres.Store.
func (s *Store) NextEventSeq(_ context.Context, game model.GameID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var max int64
	for _, ev := range s.events {
		if ev.GameID == game && ev.Seq > max {
			max = ev.Seq
		}
	}
	return max + 1, nil
}

// AppendEvents — см. postgres.Store.
func (s *Store) AppendEvents(_ context.Context, events []model.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range events {
		for _, ex := range s.events {
			if ex.GameID == ev.GameID && ex.Seq == ev.Seq {
				return fmt.Errorf("fake.AppendEvents: unique violation seq %d", ev.Seq)
			}
		}
		ev.ID = s.id()
		ev.CreatedAt = s.now()
		s.events = append(s.events, ev)
	}
	return nil
}

// ListEvents — см. postgres.Store.
func (s *Store) ListEvents(_ context.Context, game model.GameID, round model.RoundID) ([]model.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Event
	for _, ev := range s.events {
		if ev.GameID == game && ev.RoundID == round {
			out = append(out, ev)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// CreateResolvedActions — см. postgres.Store.
func (s *Store) CreateResolvedActions(_ context.Context, game model.GameID, round model.RoundID, results []model.ActionResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range results {
		dup := false
		for _, ex := range s.resolved {
			if ex.SubmissionID == r.SubmissionID {
				dup = true
			}
		}
		if dup {
			continue
		}
		s.resolved = append(s.resolved, r)
		s.resolvedRounds = append(s.resolvedRounds, resolvedKey{game, round})
	}
	return nil
}

type resolvedKey struct {
	game  model.GameID
	round model.RoundID
}

// ListResolvedActions — см. postgres.Store.
func (s *Store) ListResolvedActions(_ context.Context, game model.GameID, round model.RoundID) ([]model.ActionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.ActionResult
	for i, r := range s.resolved {
		if s.resolvedRounds[i] == (resolvedKey{game, round}) {
			out = append(out, r)
		}
	}
	return out, nil
}
