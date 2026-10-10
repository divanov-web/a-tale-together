package fake

import (
	"context"
	"fmt"
	"sort"
	"time"

	"taletogether/internal/model"
)

// LockGame — см. postgres.Store; блокировок в памяти нет.
func (s *Store) LockGame(ctx context.Context, id model.GameID) (model.Game, error) {
	if s.Err != nil {
		return model.Game{}, s.Err
	}
	return s.GetGame(ctx, id)
}

// DueGameIDs — см. postgres.Store.
func (s *Store) DueGameIDs(_ context.Context, now time.Time) ([]model.GameID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	var out []model.GameID
	for _, g := range s.games {
		if g.Status == model.GameActive && g.NextRoundAt != nil && !g.NextRoundAt.After(now) {
			out = append(out, g.ID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// CreateRound — см. postgres.Store; воспроизводит уникальность номера и единственность открытого раунда.
func (s *Store) CreateRound(_ context.Context, r model.Round) (model.Round, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.rounds {
		if ex.GameID == r.GameID && (ex.RoundNo == r.RoundNo || (ex.Status == model.RoundOpen && r.Status == model.RoundOpen)) {
			return model.Round{}, fmt.Errorf("fake.CreateRound: unique violation")
		}
	}
	r.ID = model.RoundID(s.id())
	r.CreatedAt, r.UpdatedAt = s.now(), s.now()
	s.rounds[r.ID] = r
	return r, nil
}

// UpdateRound — см. postgres.Store.
func (s *Store) UpdateRound(_ context.Context, r model.Round) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.rounds[r.ID]
	if !ok || ex.GameID != r.GameID {
		return model.ErrNotFound
	}
	ex.Status, ex.DeadlineAt, ex.ClosedAt, ex.Seed, ex.Snapshot, ex.Narration, ex.SkipReason, ex.UpdatedAt = r.Status, r.DeadlineAt, r.ClosedAt, r.Seed, r.Snapshot, r.Narration, r.SkipReason, s.now()
	s.rounds[r.ID] = ex
	return nil
}

// RecentNarrations — см. postgres.Store.
func (s *Store) RecentNarrations(_ context.Context, game model.GameID, limit int) ([]model.Narration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []model.Round
	for _, r := range s.rounds {
		if r.GameID == game && r.Narration != nil {
			list = append(list, r)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].RoundNo > list[j].RoundNo })
	var out []model.Narration
	for _, r := range list {
		if len(out) >= limit {
			break
		}
		out = append(out, *r.Narration)
	}
	return out, nil
}

// OpenRound — см. postgres.Store.
func (s *Store) OpenRound(_ context.Context, game model.GameID) (model.Round, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rounds {
		if r.GameID == game && r.Status == model.RoundOpen {
			return r, nil
		}
	}
	return model.Round{}, model.ErrNotFound
}

// LastRound — см. postgres.Store.
func (s *Store) LastRound(_ context.Context, game model.GameID) (model.Round, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best model.Round
	for _, r := range s.rounds {
		if r.GameID == game && r.RoundNo > best.RoundNo {
			best = r
		}
	}
	if best.ID == 0 {
		return model.Round{}, model.ErrNotFound
	}
	return best, nil
}

// RoundByNo — см. postgres.Store.
func (s *Store) RoundByNo(_ context.Context, game model.GameID, no int) (model.Round, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rounds {
		if r.GameID == game && r.RoundNo == no {
			return r, nil
		}
	}
	return model.Round{}, model.ErrNotFound
}

// UpsertSubmission — см. postgres.Store.
func (s *Store) UpsertSubmission(_ context.Context, sub model.Submission) (model.Submission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ex := range s.subs {
		if ex.RoundID == sub.RoundID && ex.CharacterID == sub.CharacterID {
			ex.RawText, ex.UpdatedAt = sub.RawText, sub.UpdatedAt
			s.subs[id] = ex
			return ex, nil
		}
	}
	sub.ID = model.SubmissionID(s.id())
	sub.Status = model.SubmissionPending
	sub.SubmittedAt = sub.UpdatedAt
	s.subs[sub.ID] = sub
	return sub, nil
}

// GetSubmission — см. postgres.Store.
func (s *Store) GetSubmission(_ context.Context, game model.GameID, round model.RoundID, character model.CharacterID) (model.Submission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sub := range s.subs {
		if sub.GameID == game && sub.RoundID == round && sub.CharacterID == character {
			return sub, nil
		}
	}
	return model.Submission{}, model.ErrNotFound
}

// ListSubmissions — см. postgres.Store.
func (s *Store) ListSubmissions(_ context.Context, game model.GameID, round model.RoundID) ([]model.Submission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Submission
	for _, sub := range s.subs {
		if sub.GameID == game && sub.RoundID == round {
			out = append(out, sub)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.SubmittedAt.Before(b.SubmittedAt) || (a.SubmittedAt.Equal(b.SubmittedAt) && a.ID < b.ID)
	})
	return out, nil
}

// MoveSubmissions — см. postgres.Store.
func (s *Store) MoveSubmissions(_ context.Context, game model.GameID, from, to model.RoundID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sub := range s.subs {
		if sub.GameID == game && sub.RoundID == from && sub.Status == model.SubmissionPending {
			sub.RoundID = to
			s.subs[id] = sub
		}
	}
	return nil
}

// SetSubmissionsStatus — см. postgres.Store.
func (s *Store) SetSubmissionsStatus(_ context.Context, game model.GameID, round model.RoundID, status model.SubmissionStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sub := range s.subs {
		if sub.GameID == game && sub.RoundID == round {
			sub.Status = status
			s.subs[id] = sub
		}
	}
	return nil
}

// SaveCharacterState — см. postgres.Store.
func (s *Store) SaveCharacterState(_ context.Context, game model.GameID, c model.Character) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.chars[c.ID]
	if !ok || ex.GameID != game {
		return model.ErrNotFound
	}
	ex.LocationID, ex.HP, ex.Conditions, ex.Knowledge, ex.Status, ex.MissedRounds, ex.DeathRoundsLeft, ex.DiedAt = c.LocationID, c.HP, c.Conditions, c.Knowledge, c.Status, c.MissedRounds, c.DeathRoundsLeft, c.DiedAt
	s.chars[c.ID] = ex
	return nil
}

// UpdateCharacterActivity — см. postgres.Store.
func (s *Store) UpdateCharacterActivity(_ context.Context, game model.GameID, id model.CharacterID, status model.CharacterStatus, missed int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chars[id]
	if !ok || c.GameID != game {
		return model.ErrNotFound
	}
	c.Status, c.MissedRounds = status, missed
	s.chars[id] = c
	return nil
}

// EnqueueOutbox — см. postgres.Store; дедуп по (round_id, chat, kind, part).
func (s *Store) EnqueueOutbox(_ context.Context, m model.OutboxMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.Part == 0 {
		m.Part = 1
	}
	if m.RoundID != nil {
		for _, ex := range s.outbox {
			if ex.RoundID != nil && *ex.RoundID == *m.RoundID && ex.TelegramChatID == m.TelegramChatID && ex.Kind == m.Kind && ex.Part == m.Part {
				return nil
			}
		}
	}
	m.ID = model.OutboxID(s.id())
	m.Status = model.OutboxPending
	m.CreatedAt = s.now()
	s.outbox = append(s.outbox, m)
	return nil
}

// ClaimOutbox — см. postgres.Store.
func (s *Store) ClaimOutbox(_ context.Context, now time.Time, stale time.Duration, limit int) ([]model.OutboxMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	var out []model.OutboxMessage
	for i := range s.outbox {
		m := &s.outbox[i]
		if m.Status == model.OutboxSending && m.ClaimedAt != nil && m.ClaimedAt.Before(now.Add(-stale)) {
			m.Status, m.LastError = model.OutboxUnknown, "исход отправки неизвестен"
		}
		if len(out) < limit && m.Status == model.OutboxPending && !m.SendAfter.After(now) {
			t := now
			m.Status, m.Attempts, m.ClaimedAt = model.OutboxSending, m.Attempts+1, &t
			out = append(out, *m)
		}
	}
	return out, nil
}

// FinishOutbox — см. postgres.Store.
func (s *Store) FinishOutbox(_ context.Context, m model.OutboxMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.outbox {
		if s.outbox[i].ID == m.ID {
			ex := &s.outbox[i]
			ex.Status, ex.TelegramMessageID, ex.SendAfter, ex.LastError, ex.SentAt = m.Status, m.TelegramMessageID, m.SendAfter, m.LastError, m.SentAt
			return nil
		}
	}
	return model.ErrNotFound
}

// ListOutbox — см. postgres.Store.
func (s *Store) ListOutbox(_ context.Context, game model.GameID) ([]model.OutboxMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.OutboxMessage
	for _, m := range s.outbox {
		if m.GameID == game {
			out = append(out, m)
		}
	}
	return out, nil
}
