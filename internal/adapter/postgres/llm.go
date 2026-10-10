package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

const llmProfileColumns = `id, name, provider, model_interpreter, model_narrator, params, enabled, created_at, updated_at`

func scanLLMProfile(row pgx.Row) (model.LLMProfile, error) {
	var p model.LLMProfile
	err := row.Scan(&p.ID, &p.Name, &p.Provider, &p.ModelInterpreter, &p.ModelNarrator, &p.Params, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.LLMProfile{}, model.ErrNotFound
	}
	return p, err
}

// ListLLMProfiles возвращает профили по имени.
func (s *Store) ListLLMProfiles(ctx context.Context) ([]model.LLMProfile, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+llmProfileColumns+` FROM llm_profiles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListLLMProfiles: %w", err)
	}
	defer rows.Close()
	var out []model.LLMProfile
	for rows.Next() {
		p, err := scanLLMProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ListLLMProfiles: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateLLMProfile создаёт профиль; повтор имени — ошибка БД.
func (s *Store) CreateLLMProfile(ctx context.Context, p model.LLMProfile) (model.LLMProfile, error) {
	const q = `INSERT INTO llm_profiles (name, provider, model_interpreter, model_narrator, params, enabled)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING ` + llmProfileColumns
	out, err := scanLLMProfile(s.db(ctx).QueryRow(ctx, q, p.Name, p.Provider, p.ModelInterpreter, p.ModelNarrator, p.Params, p.Enabled))
	if err != nil {
		return model.LLMProfile{}, fmt.Errorf("postgres.CreateLLMProfile: %w", err)
	}
	return out, nil
}

// GetAppSetting возвращает значение глобальной настройки.
func (s *Store) GetAppSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db(ctx).QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("postgres.GetAppSetting: %w", model.ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("postgres.GetAppSetting: %w", err)
	}
	return v, nil
}

// SetAppSetting записывает глобальную настройку.
func (s *Store) SetAppSetting(ctx context.Context, key, value string) error {
	const q = `INSERT INTO app_settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.db(ctx).Exec(ctx, q, key, value); err != nil {
		return fmt.Errorf("postgres.SetAppSetting: %w", err)
	}
	return nil
}

// SetGameLLMProfile ставит или снимает профиль игры.
func (s *Store) SetGameLLMProfile(ctx context.Context, game model.GameID, profile *model.LLMProfileID) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE games SET llm_profile_id = $2, updated_at = now() WHERE id = $1`, game, profile); err != nil {
		return fmt.Errorf("postgres.SetGameLLMProfile: %w", err)
	}
	return nil
}

// CreateLLMRun записывает вызов модели.
func (s *Store) CreateLLMRun(ctx context.Context, r model.LLMRun) (model.LLMRun, error) {
	const q = `INSERT INTO llm_runs (game_id, round_id, profile_id, provider, role, model, prompt_version, tokens_in, tokens_out, cost_usd, subscription, duration_ms, error, request_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id, created_at`
	err := s.db(ctx).QueryRow(ctx, q, r.GameID, r.RoundID, r.ProfileID, r.Provider, r.Role, r.Model, r.PromptVersion, r.TokensIn, r.TokensOut,
		r.CostUSD, r.Subscription, r.DurationMS, r.Error, r.RequestID).Scan(&r.ID, &r.CreatedAt)
	if err != nil {
		return model.LLMRun{}, fmt.Errorf("postgres.CreateLLMRun: %w", err)
	}
	return r, nil
}

// ListLLMRuns возвращает последние вызовы (диагностика и тесты).
func (s *Store) ListLLMRuns(ctx context.Context, limit int) ([]model.LLMRun, error) {
	const q = `SELECT id, game_id, round_id, profile_id, provider, role, model, prompt_version, tokens_in, tokens_out, cost_usd, subscription, duration_ms, error, request_id, created_at
FROM llm_runs ORDER BY id DESC LIMIT $1`
	rows, err := s.db(ctx).Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListLLMRuns: %w", err)
	}
	defer rows.Close()
	var out []model.LLMRun
	for rows.Next() {
		var r model.LLMRun
		if err := rows.Scan(&r.ID, &r.GameID, &r.RoundID, &r.ProfileID, &r.Provider, &r.Role, &r.Model, &r.PromptVersion, &r.TokensIn, &r.TokensOut,
			&r.CostUSD, &r.Subscription, &r.DurationMS, &r.Error, &r.RequestID, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres.ListLLMRuns: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
