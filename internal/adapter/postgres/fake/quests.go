package fake

import (
	"context"
	"sort"

	"taletogether/internal/model"
)

// CreateQuest — см. postgres.Store.
func (s *Store) CreateQuest(_ context.Context, q model.Quest) (model.Quest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q.ID = model.QuestID(s.id())
	q.CreatedAt, q.UpdatedAt = s.now(), s.now()
	if q.Status == "" {
		q.Status = model.QuestActive
	}
	stages := make([]model.QuestStage, 0, len(q.Stages))
	for i, st := range q.Stages {
		st.ID, st.GameID, st.QuestID, st.StageNo = model.QuestStageID(s.id()), q.GameID, q.ID, i+1
		if st.Status == "" {
			st.Status = model.StagePending
		}
		stages = append(stages, st)
	}
	q.Stages = stages
	s.quests[q.ID] = q
	return q, nil
}

// UpdateQuest — см. postgres.Store.
func (s *Store) UpdateQuest(_ context.Context, q model.Quest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.quests[q.ID]
	if !ok || ex.GameID != q.GameID {
		return model.ErrNotFound
	}
	ex.Status, ex.DoneRoundID, ex.UpdatedAt = q.Status, q.DoneRoundID, s.now()
	s.quests[q.ID] = ex
	return nil
}

// UpdateQuestStage — см. postgres.Store.
func (s *Store) UpdateQuestStage(_ context.Context, st model.QuestStage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.quests[st.QuestID]
	if !ok || q.GameID != st.GameID {
		return model.ErrNotFound
	}
	stages := append([]model.QuestStage{}, q.Stages...)
	for i := range stages {
		if stages[i].ID == st.ID {
			stages[i].Condition, stages[i].Reward, stages[i].LocationID, stages[i].Status, stages[i].DoneRoundID = st.Condition, st.Reward, st.LocationID, st.Status, st.DoneRoundID
		}
	}
	q.Stages = stages
	s.quests[st.QuestID] = q
	return nil
}

// ListQuests — см. postgres.Store.
func (s *Store) ListQuests(_ context.Context, game model.GameID) ([]model.Quest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Quest
	for _, q := range s.quests {
		if q.GameID == game {
			q.Stages = append([]model.QuestStage{}, q.Stages...)
			out = append(out, q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// CreateFactionGoal — см. postgres.Store.
func (s *Store) CreateFactionGoal(_ context.Context, g model.FactionGoal) (model.FactionGoal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g.Version = 1
	for _, ex := range s.goals {
		if ex.FactionID == g.FactionID && ex.Version >= g.Version {
			g.Version = ex.Version + 1
		}
	}
	g.ID = model.FactionGoalID(s.id())
	g.CreatedAt = s.now()
	s.goals[g.ID] = g
	return g, nil
}

// ListFactionGoals — см. postgres.Store: последняя версия каждой фракции.
func (s *Store) ListFactionGoals(_ context.Context, game model.GameID) ([]model.FactionGoal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	latest := map[model.FactionID]model.FactionGoal{}
	for _, g := range s.goals {
		if g.GameID != game {
			continue
		}
		if cur, ok := latest[g.FactionID]; !ok || g.Version > cur.Version {
			latest[g.FactionID] = g
		}
	}
	var out []model.FactionGoal
	for _, g := range latest {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FactionID < out[j].FactionID })
	return out, nil
}

// SetFactionExplored — см. postgres.Store.
func (s *Store) SetFactionExplored(_ context.Context, game model.GameID, faction model.FactionID, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.facts[faction]
	if !ok || f.GameID != game {
		return model.ErrNotFound
	}
	f.ExploredSinceReset = n
	s.facts[faction] = f
	return nil
}

// resetQuests удаляет квесты и цели игры (часть ResetWorld).
func (s *Store) resetQuests(game model.GameID) {
	for id, q := range s.quests {
		if q.GameID == game {
			delete(s.quests, id)
		}
	}
	for id, g := range s.goals {
		if g.GameID == game {
			delete(s.goals, id)
		}
	}
}
