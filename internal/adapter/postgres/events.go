package postgres

import (
	"context"
	"fmt"

	"taletogether/internal/model"
)

// NextEventSeq возвращает следующий сквозной номер события игры (вызывать под блокировкой игры).
func (s *Store) NextEventSeq(ctx context.Context, game model.GameID) (int64, error) {
	var n int64
	if err := s.db(ctx).QueryRow(ctx, `SELECT coalesce(max(seq), 0) + 1 FROM world_events WHERE game_id = $1`, game).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres.NextEventSeq: %w", err)
	}
	return n, nil
}

// AppendEvents записывает события раунда; журнал только добавляется.
func (s *Store) AppendEvents(ctx context.Context, events []model.Event) error {
	const q = `INSERT INTO world_events (game_id, round_id, seq, type, payload, visibility, visible_to, location_id, character_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	for _, ev := range events {
		to := ev.VisibleTo
		if to == nil {
			to = []model.CharacterID{}
		}
		if _, err := s.db(ctx).Exec(ctx, q, ev.GameID, ev.RoundID, ev.Seq, ev.Type, ev.Payload, ev.Visibility, to, ev.LocationID, ev.CharacterID); err != nil {
			return fmt.Errorf("postgres.AppendEvents: seq %d: %w", ev.Seq, err)
		}
	}
	return nil
}

// ListEvents возвращает события раунда по порядку.
func (s *Store) ListEvents(ctx context.Context, game model.GameID, round model.RoundID) ([]model.Event, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT id, game_id, round_id, seq, type, payload, visibility, visible_to, location_id, character_id, created_at FROM world_events WHERE game_id = $1 AND round_id = $2 ORDER BY seq`, game, round)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListEvents: %w", err)
	}
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		var ev model.Event
		if err := rows.Scan(&ev.ID, &ev.GameID, &ev.RoundID, &ev.Seq, &ev.Type, &ev.Payload, &ev.Visibility, &ev.VisibleTo, &ev.LocationID, &ev.CharacterID, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres.ListEvents: scan: %w", err)
		}
		if len(ev.VisibleTo) == 0 {
			ev.VisibleTo = nil
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// CreateResolvedActions записывает итоги заявок раунда; повтор по submission_id пропускается.
func (s *Store) CreateResolvedActions(ctx context.Context, game model.GameID, round model.RoundID, results []model.ActionResult) error {
	const q = `INSERT INTO resolved_actions (game_id, round_id, submission_id, character_id, intent, status, outcome, reason, summary, checks, event_seqs)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT (submission_id) DO NOTHING`
	for _, r := range results {
		seqs := r.EventSeqs
		if seqs == nil {
			seqs = []int64{}
		}
		if _, err := s.db(ctx).Exec(ctx, q, game, round, r.SubmissionID, r.CharacterID, r.Intent, r.Status, r.Outcome, r.Reason, r.Summary, r.Check, seqs); err != nil {
			return fmt.Errorf("postgres.CreateResolvedActions: submission %d: %w", r.SubmissionID, err)
		}
	}
	return nil
}

// ListResolvedActions возвращает итоги заявок раунда по порядку записи.
func (s *Store) ListResolvedActions(ctx context.Context, game model.GameID, round model.RoundID) ([]model.ActionResult, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT submission_id, character_id, intent, status, outcome, reason, summary, checks, event_seqs FROM resolved_actions WHERE game_id = $1 AND round_id = $2 ORDER BY id`, game, round)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListResolvedActions: %w", err)
	}
	defer rows.Close()
	var out []model.ActionResult
	for rows.Next() {
		var r model.ActionResult
		if err := rows.Scan(&r.SubmissionID, &r.CharacterID, &r.Intent, &r.Status, &r.Outcome, &r.Reason, &r.Summary, &r.Check, &r.EventSeqs); err != nil {
			return nil, fmt.Errorf("postgres.ListResolvedActions: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
