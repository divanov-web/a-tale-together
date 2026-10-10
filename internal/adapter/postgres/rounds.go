package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

const roundColumns = `id, game_id, season_id, round_no, status, opens_at, deadline_at, closed_at, seed, snapshot, skip_reason, created_at, updated_at`

func scanRound(row pgx.Row) (model.Round, error) {
	var r model.Round
	err := row.Scan(&r.ID, &r.GameID, &r.SeasonID, &r.RoundNo, &r.Status, &r.OpensAt, &r.DeadlineAt, &r.ClosedAt, &r.Seed, &r.Snapshot, &r.SkipReason, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Round{}, model.ErrNotFound
	}
	return r, err
}

// CreateRound создаёт раунд; второй открытый раунд игры или повтор номера — ошибка БД.
func (s *Store) CreateRound(ctx context.Context, r model.Round) (model.Round, error) {
	const q = `INSERT INTO rounds (game_id, season_id, round_no, status, opens_at, deadline_at, seed)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING ` + roundColumns
	out, err := scanRound(s.db(ctx).QueryRow(ctx, q, r.GameID, r.SeasonID, r.RoundNo, r.Status, r.OpensAt, r.DeadlineAt, r.Seed))
	if err != nil {
		return model.Round{}, fmt.Errorf("postgres.CreateRound: %w", err)
	}
	return out, nil
}

// UpdateRound сохраняет статус, дедлайн, закрытие, seed, снимок и причину пропуска.
func (s *Store) UpdateRound(ctx context.Context, r model.Round) error {
	const q = `UPDATE rounds SET status = $3, deadline_at = $4, closed_at = $5, seed = $6, snapshot = $7, skip_reason = $8, updated_at = now()
WHERE game_id = $1 AND id = $2`
	if _, err := s.db(ctx).Exec(ctx, q, r.GameID, r.ID, r.Status, r.DeadlineAt, r.ClosedAt, r.Seed, r.Snapshot, r.SkipReason); err != nil {
		return fmt.Errorf("postgres.UpdateRound: %w", err)
	}
	return nil
}

// OpenRound возвращает открытый раунд игры.
func (s *Store) OpenRound(ctx context.Context, game model.GameID) (model.Round, error) {
	r, err := scanRound(s.db(ctx).QueryRow(ctx, `SELECT `+roundColumns+` FROM rounds WHERE game_id = $1 AND status = 'open'`, game))
	if err != nil {
		return model.Round{}, fmt.Errorf("postgres.OpenRound: %w", err)
	}
	return r, nil
}

// LastRound возвращает раунд игры с наибольшим номером.
func (s *Store) LastRound(ctx context.Context, game model.GameID) (model.Round, error) {
	r, err := scanRound(s.db(ctx).QueryRow(ctx, `SELECT `+roundColumns+` FROM rounds WHERE game_id = $1 ORDER BY round_no DESC LIMIT 1`, game))
	if err != nil {
		return model.Round{}, fmt.Errorf("postgres.LastRound: %w", err)
	}
	return r, nil
}

// RoundByNo возвращает раунд игры по номеру.
func (s *Store) RoundByNo(ctx context.Context, game model.GameID, no int) (model.Round, error) {
	r, err := scanRound(s.db(ctx).QueryRow(ctx, `SELECT `+roundColumns+` FROM rounds WHERE game_id = $1 AND round_no = $2`, game, no))
	if err != nil {
		return model.Round{}, fmt.Errorf("postgres.RoundByNo: %w", err)
	}
	return r, nil
}

// LockGame возвращает игру под блокировкой строки до конца транзакции; занятая другим воркером строка
// пропускается (SKIP LOCKED) и даёт ErrNotFound. Вызывать внутри InTx.
func (s *Store) LockGame(ctx context.Context, id model.GameID) (model.Game, error) {
	g, err := scanGame(s.db(ctx).QueryRow(ctx, `SELECT `+gameColumns+` FROM games WHERE id = $1 FOR UPDATE SKIP LOCKED`, id))
	if err != nil {
		return model.Game{}, fmt.Errorf("postgres.LockGame: %w", err)
	}
	return g, nil
}

// DueGameIDs возвращает активные игры, у которых наступило время планировщика.
func (s *Store) DueGameIDs(ctx context.Context, now time.Time) ([]model.GameID, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT id FROM games WHERE status = 'active' AND next_round_at IS NOT NULL AND next_round_at <= $1 ORDER BY next_round_at`, now)
	if err != nil {
		return nil, fmt.Errorf("postgres.DueGameIDs: %w", err)
	}
	defer rows.Close()
	var out []model.GameID
	for rows.Next() {
		var id model.GameID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres.DueGameIDs: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const submissionColumns = `id, game_id, round_id, character_id, raw_text, status, submitted_at, updated_at`

func scanSubmission(row pgx.Row) (model.Submission, error) {
	var sub model.Submission
	err := row.Scan(&sub.ID, &sub.GameID, &sub.RoundID, &sub.CharacterID, &sub.RawText, &sub.Status, &sub.SubmittedAt, &sub.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Submission{}, model.ErrNotFound
	}
	return sub, err
}

// UpsertSubmission создаёт заявку персонажа на раунд или заменяет текст существующей; время берётся из заявки.
func (s *Store) UpsertSubmission(ctx context.Context, sub model.Submission) (model.Submission, error) {
	const q = `INSERT INTO action_submissions (game_id, round_id, character_id, raw_text, status, submitted_at, updated_at)
VALUES ($1, $2, $3, $4, 'pending', $5, $5)
ON CONFLICT (round_id, character_id) DO UPDATE SET raw_text = EXCLUDED.raw_text, updated_at = EXCLUDED.updated_at
RETURNING ` + submissionColumns
	out, err := scanSubmission(s.db(ctx).QueryRow(ctx, q, sub.GameID, sub.RoundID, sub.CharacterID, sub.RawText, sub.UpdatedAt))
	if err != nil {
		return model.Submission{}, fmt.Errorf("postgres.UpsertSubmission: %w", err)
	}
	return out, nil
}

// GetSubmission возвращает заявку персонажа на раунд.
func (s *Store) GetSubmission(ctx context.Context, game model.GameID, round model.RoundID, character model.CharacterID) (model.Submission, error) {
	sub, err := scanSubmission(s.db(ctx).QueryRow(ctx, `SELECT `+submissionColumns+` FROM action_submissions WHERE game_id = $1 AND round_id = $2 AND character_id = $3`, game, round, character))
	if err != nil {
		return model.Submission{}, fmt.Errorf("postgres.GetSubmission: %w", err)
	}
	return sub, nil
}

// ListSubmissions возвращает заявки раунда по порядку подачи.
func (s *Store) ListSubmissions(ctx context.Context, game model.GameID, round model.RoundID) ([]model.Submission, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+submissionColumns+` FROM action_submissions WHERE game_id = $1 AND round_id = $2 ORDER BY submitted_at, id`, game, round)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListSubmissions: %w", err)
	}
	defer rows.Close()
	var out []model.Submission
	for rows.Next() {
		sub, err := scanSubmission(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ListSubmissions: scan: %w", err)
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// MoveSubmissions переносит ожидающие заявки пропущенного раунда в следующий.
func (s *Store) MoveSubmissions(ctx context.Context, game model.GameID, from, to model.RoundID) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE action_submissions SET round_id = $3 WHERE game_id = $1 AND round_id = $2 AND status = 'pending'`, game, from, to); err != nil {
		return fmt.Errorf("postgres.MoveSubmissions: %w", err)
	}
	return nil
}

// SetSubmissionsStatus ставит статус всем заявкам раунда.
func (s *Store) SetSubmissionsStatus(ctx context.Context, game model.GameID, round model.RoundID, status model.SubmissionStatus) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE action_submissions SET status = $3, updated_at = now() WHERE game_id = $1 AND round_id = $2`, game, round, status); err != nil {
		return fmt.Errorf("postgres.SetSubmissionsStatus: %w", err)
	}
	return nil
}

// UpdateCharacterActivity сохраняет статус активности и число пропусков персонажа.
func (s *Store) UpdateCharacterActivity(ctx context.Context, game model.GameID, id model.CharacterID, status model.CharacterStatus, missed int) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE characters SET status = $3, missed_rounds = $4, updated_at = now() WHERE game_id = $1 AND id = $2`, game, id, status, missed); err != nil {
		return fmt.Errorf("postgres.UpdateCharacterActivity: %w", err)
	}
	return nil
}
