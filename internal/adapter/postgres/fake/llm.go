package fake

import (
	"context"
	"fmt"
	"sort"

	"taletogether/internal/model"
)

// ListLLMProfiles — см. postgres.Store.
func (s *Store) ListLLMProfiles(_ context.Context) ([]model.LLMProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	out := make([]model.LLMProfile, 0, len(s.llmProfiles))
	for _, p := range s.llmProfiles {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CreateLLMProfile — см. postgres.Store.
func (s *Store) CreateLLMProfile(_ context.Context, p model.LLMProfile) (model.LLMProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.llmProfiles {
		if ex.Name == p.Name {
			return model.LLMProfile{}, fmt.Errorf("fake.CreateLLMProfile: unique violation")
		}
	}
	p.ID = model.LLMProfileID(s.id())
	p.CreatedAt, p.UpdatedAt = s.now(), s.now()
	s.llmProfiles[p.ID] = p
	return p, nil
}

// GetAppSetting — см. postgres.Store.
func (s *Store) GetAppSetting(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.settings[key]
	if !ok {
		return "", model.ErrNotFound
	}
	return v, nil
}

// SetAppSetting — см. postgres.Store.
func (s *Store) SetAppSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[key] = value
	return nil
}

// SetGameLLMProfile — см. postgres.Store.
func (s *Store) SetGameLLMProfile(_ context.Context, game model.GameID, profile *model.LLMProfileID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.games[game]
	if !ok {
		return model.ErrNotFound
	}
	g.LLMProfileID = profile
	s.games[game] = g
	return nil
}

// CreateLLMRun — см. postgres.Store.
func (s *Store) CreateLLMRun(_ context.Context, r model.LLMRun) (model.LLMRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ID = model.LLMRunID(s.id())
	r.CreatedAt = s.now()
	s.llmRuns = append(s.llmRuns, r)
	return r, nil
}

// ListLLMRuns — см. postgres.Store: последние вызовы, новые первыми.
func (s *Store) ListLLMRuns(_ context.Context, limit int) ([]model.LLMRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.LLMRun
	for i := len(s.llmRuns) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.llmRuns[i])
	}
	return out, nil
}

// ListLLMPrices — см. postgres.Store; тарифы задаются полем `LLMPrices` при настройке теста.
func (s *Store) ListLLMPrices(_ context.Context) ([]model.LLMPrice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.LLMPrice, len(s.LLMPrices))
	copy(out, s.LLMPrices)
	return out, nil
}
