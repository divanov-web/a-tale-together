package fake

import (
	"context"
	"fmt"
	"sort"

	"taletogether/internal/model"
)

// CreateLocation — см. postgres.Store: повтор координат в игре — ошибка.
func (s *Store) CreateLocation(_ context.Context, l model.Location) (model.Location, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.locations {
		if ex.GameID == l.GameID && ex.X == l.X && ex.Y == l.Y {
			return model.Location{}, fmt.Errorf("fake.CreateLocation: unique violation (game_id, x, y)")
		}
	}
	l.ID = model.LocationID(s.id())
	l.CreatedAt = s.now()
	if l.Stage == "" {
		l.Stage = model.StageDetailed
	}
	if l.Objects == nil {
		l.Objects = []model.LocationObject{}
	}
	for i := range l.Objects {
		if l.Objects[i].Approaches == nil {
			l.Objects[i].Approaches = []model.Approach{}
		}
	}
	s.locations[l.ID] = l
	return l, nil
}

// UpdateLocation — см. postgres.Store.
func (s *Store) UpdateLocation(_ context.Context, l model.Location) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.locations[l.ID]
	if !ok || ex.GameID != l.GameID {
		return model.ErrNotFound
	}
	ex.Name, ex.Description, ex.Difficulty, ex.Stage, ex.Terrain, ex.VisitedAt = l.Name, l.Description, l.Difficulty, l.Stage, l.Terrain, l.VisitedAt
	s.locations[l.ID] = ex
	return nil
}

// ListLocations — см. postgres.Store.
func (s *Store) ListLocations(_ context.Context, game model.GameID) ([]model.Location, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Location
	for _, l := range s.locations {
		if l.GameID == game {
			l.Objects = append([]model.LocationObject{}, l.Objects...)
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// CreateObjects — см. postgres.Store.
func (s *Store) CreateObjects(_ context.Context, game model.GameID, id model.LocationID, objects []model.LocationObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locations[id]
	if !ok || l.GameID != game {
		return model.ErrNotFound
	}
	for _, o := range objects {
		for _, ex := range l.Objects {
			if ex.Key == o.Key {
				return fmt.Errorf("fake.CreateObjects: unique violation %s", o.Key)
			}
		}
		if o.Approaches == nil {
			o.Approaches = []model.Approach{}
		}
		l.Objects = append(l.Objects, o)
	}
	s.locations[id] = l
	return nil
}

// SaveObjectState — см. postgres.Store.
func (s *Store) SaveObjectState(_ context.Context, game model.GameID, id model.LocationID, o model.LocationObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locations[id]
	if !ok || l.GameID != game {
		return model.ErrNotFound
	}
	objs := append([]model.LocationObject{}, l.Objects...)
	for i := range objs {
		if objs[i].Key == o.Key {
			objs[i].State, objs[i].Revealed, objs[i].Searched = o.State, o.Revealed, o.Searched
		}
	}
	l.Objects = objs
	s.locations[id] = l
	return nil
}

// ReplaceObjects — см. postgres.Store.
func (s *Store) ReplaceObjects(ctx context.Context, game model.GameID, id model.LocationID, objects []model.LocationObject) error {
	s.mu.Lock()
	l, ok := s.locations[id]
	if !ok || l.GameID != game {
		s.mu.Unlock()
		return model.ErrNotFound
	}
	l.Objects = []model.LocationObject{}
	s.locations[id] = l
	s.mu.Unlock()
	return s.CreateObjects(ctx, game, id, objects)
}

// AddDiscovery — см. postgres.Store.
func (s *Store) AddDiscovery(_ context.Context, game model.GameID, loc model.LocationID, faction model.FactionID, _ model.RoundID, _ model.CharacterID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := discoveryKey{loc, faction}
	if s.discoveries[key] {
		return false, nil
	}
	s.discoveries[key] = true
	if f, ok := s.facts[faction]; ok && f.GameID == game {
		f.ExploredSinceReset++
		s.facts[faction] = f
	}
	return true, nil
}

type discoveryKey struct {
	location model.LocationID
	faction  model.FactionID
}

// ListDiscoveries — см. postgres.Store.
func (s *Store) ListDiscoveries(_ context.Context, game model.GameID) (map[model.LocationID][]model.FactionID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[model.LocationID][]model.FactionID{}
	var keys []discoveryKey
	for k := range s.discoveries {
		if l, ok := s.locations[k.location]; ok && l.GameID == game {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].location != keys[j].location {
			return keys[i].location < keys[j].location
		}
		return keys[i].faction < keys[j].faction
	})
	for _, k := range keys {
		out[k.location] = append(out[k.location], k.faction)
	}
	return out, nil
}

// ResetWorld — см. postgres.Store.
func (s *Store) ResetWorld(_ context.Context, game model.GameID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var start *model.LocationID
	for id, l := range s.locations {
		if l.GameID != game {
			continue
		}
		if l.Start {
			sid := id
			start = &sid
			l.Objects, l.VisitedAt, l.Stage = []model.LocationObject{}, nil, model.StageDetailed
			s.locations[id] = l
			continue
		}
		delete(s.locations, id)
	}
	for k := range s.discoveries {
		if _, ok := s.locations[k.location]; !ok {
			delete(s.discoveries, k)
		} else if s.locations[k.location].GameID == game {
			delete(s.discoveries, k)
		}
	}
	for id, f := range s.facts {
		if f.GameID == game {
			f.ExploredSinceReset = 0
			s.facts[id] = f
		}
	}
	for id, c := range s.chars {
		if c.GameID == game {
			c.LocationID = start
			s.chars[id] = c
		}
	}
	for id, p := range s.parties {
		if p.GameID == game {
			p.LocationID = nil
			s.parties[id] = p
		}
	}
	for k := range s.jobs {
		if k.game == game {
			delete(s.jobs, k)
		}
	}
	s.resetQuests(game)
	return nil
}

type jobKey struct {
	game  model.GameID
	round model.RoundID
	role  model.LLMRole
	hash  string
}

// GetGenerationJob — см. postgres.Store.
func (s *Store) GetGenerationJob(_ context.Context, game model.GameID, round *model.RoundID, role model.LLMRole, hash string) (model.GenerationJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r model.RoundID
	if round != nil {
		r = *round
	}
	j, ok := s.jobs[jobKey{game, r, role, hash}]
	if !ok {
		return model.GenerationJob{}, model.ErrNotFound
	}
	return j, nil
}

// SaveGenerationJob — см. postgres.Store: запись переживает откат транзакции (`jobs` не входит в снимок InTx).
func (s *Store) SaveGenerationJob(_ context.Context, j model.GenerationJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r model.RoundID
	if j.RoundID != nil {
		r = *j.RoundID
	}
	key := jobKey{j.GameID, r, j.Role, j.InputHash}
	if _, ok := s.jobs[key]; ok {
		return nil
	}
	j.ID = s.id()
	j.CreatedAt = s.now()
	s.jobs[key] = j
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
