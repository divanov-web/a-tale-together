package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

// Пункт 28 (ADR-0020): квесты фракций с этапами, цели фракций по версиям, счётчик исследования.

const questColumns = `id, game_id, faction_id, kind, status, title, summary, created_round_id, done_round_id, created_at, updated_at`
const stageColumns = `id, game_id, quest_id, stage_no, key, title, summary, distance, condition, reward, location_id, status, done_round_id`

func scanQuest(row pgx.Row) (model.Quest, error) {
	var q model.Quest
	err := row.Scan(&q.ID, &q.GameID, &q.FactionID, &q.Kind, &q.Status, &q.Title, &q.Summary, &q.CreatedRoundID, &q.DoneRoundID, &q.CreatedAt, &q.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Quest{}, model.ErrNotFound
	}
	q.Stages = []model.QuestStage{}
	return q, err
}

func scanStage(row pgx.Row) (model.QuestStage, error) {
	var st model.QuestStage
	var cond, reward []byte
	if err := row.Scan(&st.ID, &st.GameID, &st.QuestID, &st.StageNo, &st.Key, &st.Title, &st.Summary, &st.Distance, &cond, &reward, &st.LocationID, &st.Status, &st.DoneRoundID); err != nil {
		return model.QuestStage{}, err
	}
	if err := json.Unmarshal(cond, &st.Condition); err != nil {
		return model.QuestStage{}, fmt.Errorf("condition: %w", err)
	}
	if err := json.Unmarshal(reward, &st.Reward); err != nil {
		return model.QuestStage{}, fmt.Errorf("reward: %w", err)
	}
	return st, nil
}

// CreateQuest создаёт квест с этапами по порядку; возвращает квест с идентификаторами.
func (s *Store) CreateQuest(ctx context.Context, q model.Quest) (model.Quest, error) {
	const insQuest = `INSERT INTO quests (game_id, faction_id, kind, status, title, summary, created_round_id)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING ` + questColumns
	status := q.Status
	if status == "" {
		status = model.QuestActive
	}
	out, err := scanQuest(s.db(ctx).QueryRow(ctx, insQuest, q.GameID, q.FactionID, q.Kind, status, q.Title, q.Summary, q.CreatedRoundID))
	if err != nil {
		return model.Quest{}, fmt.Errorf("postgres.CreateQuest: %w", err)
	}
	const insStage = `INSERT INTO quest_stages (game_id, quest_id, stage_no, key, title, summary, distance, condition, reward, location_id, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING ` + stageColumns
	for i, st := range q.Stages {
		cond, _ := json.Marshal(st.Condition)
		reward, _ := json.Marshal(st.Reward)
		status := st.Status
		if status == "" {
			status = model.StagePending
		}
		row, err := scanStage(s.db(ctx).QueryRow(ctx, insStage, out.GameID, out.ID, i+1, st.Key, st.Title, st.Summary, st.Distance, cond, reward, st.LocationID, status))
		if err != nil {
			return model.Quest{}, fmt.Errorf("postgres.CreateQuest: stage %d: %w", i+1, err)
		}
		out.Stages = append(out.Stages, row)
	}
	return out, nil
}

// UpdateQuest сохраняет статус и раунд завершения квеста.
func (s *Store) UpdateQuest(ctx context.Context, q model.Quest) error {
	const query = `UPDATE quests SET status = $3, done_round_id = $4, updated_at = now() WHERE game_id = $1 AND id = $2`
	if _, err := s.db(ctx).Exec(ctx, query, q.GameID, q.ID, q.Status, q.DoneRoundID); err != nil {
		return fmt.Errorf("postgres.UpdateQuest: %w", err)
	}
	return nil
}

// UpdateQuestStage сохраняет условие, награду, клетку, статус и раунд закрытия этапа.
func (s *Store) UpdateQuestStage(ctx context.Context, st model.QuestStage) error {
	const query = `UPDATE quest_stages SET condition = $3, reward = $4, location_id = $5, status = $6, done_round_id = $7, updated_at = now()
WHERE game_id = $1 AND id = $2`
	cond, _ := json.Marshal(st.Condition)
	reward, _ := json.Marshal(st.Reward)
	if _, err := s.db(ctx).Exec(ctx, query, st.GameID, st.ID, cond, reward, st.LocationID, st.Status, st.DoneRoundID); err != nil {
		return fmt.Errorf("postgres.UpdateQuestStage: %w", err)
	}
	return nil
}

// ListQuests возвращает квесты игры с этапами по порядку создания (активные и закрытые).
func (s *Store) ListQuests(ctx context.Context, game model.GameID) ([]model.Quest, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+questColumns+` FROM quests WHERE game_id = $1 ORDER BY id`, game)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListQuests: %w", err)
	}
	defer rows.Close()
	var out []model.Quest
	index := map[model.QuestID]int{}
	for rows.Next() {
		q, err := scanQuest(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ListQuests: scan: %w", err)
		}
		index[q.ID] = len(out)
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.ListQuests: %w", err)
	}
	srows, err := s.db(ctx).Query(ctx, `SELECT `+stageColumns+` FROM quest_stages WHERE game_id = $1 ORDER BY quest_id, stage_no`, game)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListQuests: stages: %w", err)
	}
	defer srows.Close()
	for srows.Next() {
		st, err := scanStage(srows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ListQuests: stage scan: %w", err)
		}
		if i, ok := index[st.QuestID]; ok {
			out[i].Stages = append(out[i].Stages, st)
		}
	}
	if err := srows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.ListQuests: stages: %w", err)
	}
	return out, nil
}

// CreateFactionGoal записывает новую версию цели фракции (номер — следующий за последней).
func (s *Store) CreateFactionGoal(ctx context.Context, g model.FactionGoal) (model.FactionGoal, error) {
	const query = `INSERT INTO faction_goals (game_id, faction_id, version, text, stage, quest_id, round_id)
VALUES ($1, $2, (SELECT coalesce(max(version), 0) + 1 FROM faction_goals WHERE faction_id = $2), $3, $4, $5, $6)
RETURNING id, game_id, faction_id, version, text, stage, quest_id, round_id, created_at`
	var out model.FactionGoal
	err := s.db(ctx).QueryRow(ctx, query, g.GameID, g.FactionID, g.Text, g.Stage, g.QuestID, g.RoundID).
		Scan(&out.ID, &out.GameID, &out.FactionID, &out.Version, &out.Text, &out.Stage, &out.QuestID, &out.RoundID, &out.CreatedAt)
	if err != nil {
		return model.FactionGoal{}, fmt.Errorf("postgres.CreateFactionGoal: %w", err)
	}
	return out, nil
}

// ListFactionGoals возвращает текущую (последнюю) версию цели каждой фракции игры.
func (s *Store) ListFactionGoals(ctx context.Context, game model.GameID) ([]model.FactionGoal, error) {
	const query = `SELECT DISTINCT ON (faction_id) id, game_id, faction_id, version, text, stage, quest_id, round_id, created_at
FROM faction_goals WHERE game_id = $1 ORDER BY faction_id, version DESC`
	rows, err := s.db(ctx).Query(ctx, query, game)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListFactionGoals: %w", err)
	}
	defer rows.Close()
	var out []model.FactionGoal
	for rows.Next() {
		var g model.FactionGoal
		if err := rows.Scan(&g.ID, &g.GameID, &g.FactionID, &g.Version, &g.Text, &g.Stage, &g.QuestID, &g.RoundID, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres.ListFactionGoals: scan: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetFactionExplored записывает счётчик исследования фракции (сброс после выдачи или завершения квеста).
func (s *Store) SetFactionExplored(ctx context.Context, game model.GameID, faction model.FactionID, n int) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE factions SET explored_since_reset = $3 WHERE game_id = $1 AND id = $2`, game, faction, n); err != nil {
		return fmt.Errorf("postgres.SetFactionExplored: %w", err)
	}
	return nil
}
