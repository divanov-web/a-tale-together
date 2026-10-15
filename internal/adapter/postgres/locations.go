package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

const locationColumns = `id, game_id, name, description, difficulty, x, y, stage, terrain, visited_at, hidden, distant_view, start, created_at`

func scanLocation(row pgx.Row) (model.Location, error) {
	var l model.Location
	err := row.Scan(&l.ID, &l.GameID, &l.Name, &l.Description, &l.Difficulty, &l.X, &l.Y, &l.Stage, &l.Terrain, &l.VisitedAt, &l.Hidden, &l.DistantView, &l.Start, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Location{}, model.ErrNotFound
	}
	l.Objects = []model.LocationObject{}
	return l, err
}

// CreateLocation создаёт клетку с объектами; повтор координат в игре — ошибка БД. Стадия по умолчанию `detailed`.
func (s *Store) CreateLocation(ctx context.Context, l model.Location) (model.Location, error) {
	if l.Stage == "" {
		l.Stage = model.StageDetailed
	}
	const q = `INSERT INTO locations (game_id, name, description, difficulty, x, y, stage, terrain, visited_at, hidden, distant_view, start)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING ` + locationColumns
	out, err := scanLocation(s.db(ctx).QueryRow(ctx, q, l.GameID, l.Name, l.Description, l.Difficulty, l.X, l.Y, l.Stage, l.Terrain, l.VisitedAt, l.Hidden, l.DistantView, l.Start))
	if err != nil {
		return model.Location{}, fmt.Errorf("postgres.CreateLocation: %w", err)
	}
	if err := s.CreateObjects(ctx, out.GameID, out.ID, l.Objects); err != nil {
		return model.Location{}, fmt.Errorf("postgres.CreateLocation: %w", err)
	}
	out.Objects = append(out.Objects, l.Objects...)
	return out, nil
}

// UpdateLocation сохраняет имя, описание, сложность, стадию, тип местности, время первого посещения и видимость клетки.
func (s *Store) UpdateLocation(ctx context.Context, l model.Location) error {
	const q = `UPDATE locations SET name = $3, description = $4, difficulty = $5, stage = $6, terrain = $7, visited_at = $8, hidden = $9, distant_view = $10, updated_at = now()
WHERE game_id = $1 AND id = $2`
	if _, err := s.db(ctx).Exec(ctx, q, l.GameID, l.ID, l.Name, l.Description, l.Difficulty, l.Stage, l.Terrain, l.VisitedAt, l.Hidden, l.DistantView); err != nil {
		return fmt.Errorf("postgres.UpdateLocation: %w", err)
	}
	return nil
}

// ListLocations возвращает клетки игры с объектами по порядку создания.
func (s *Store) ListLocations(ctx context.Context, game model.GameID) ([]model.Location, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+locationColumns+` FROM locations WHERE game_id = $1 ORDER BY id`, game)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListLocations: %w", err)
	}
	defer rows.Close()
	var out []model.Location
	index := map[model.LocationID]int{}
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ListLocations: scan: %w", err)
		}
		index[l.ID] = len(out)
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.ListLocations: %w", err)
	}
	objects, err := s.listObjects(ctx, game)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListLocations: %w", err)
	}
	for loc, objs := range objects {
		if i, ok := index[loc]; ok {
			out[i].Objects = objs
		}
	}
	return out, nil
}

const objectColumns = `location_id, key, title, description, state, revealed, hidden_until_revealed, searched, approaches`

// listObjects — объекты всех клеток игры по порядку создания, сгруппированные по клетке.
func (s *Store) listObjects(ctx context.Context, game model.GameID) (map[model.LocationID][]model.LocationObject, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+objectColumns+` FROM world_objects WHERE game_id = $1 ORDER BY id`, game)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[model.LocationID][]model.LocationObject{}
	for rows.Next() {
		var loc model.LocationID
		var o model.LocationObject
		if err := rows.Scan(&loc, &o.Key, &o.Title, &o.Description, &o.State, &o.Revealed, &o.HiddenUntil, &o.Searched, &o.Approaches); err != nil {
			return nil, fmt.Errorf("scan object: %w", err)
		}
		if o.Approaches == nil {
			o.Approaches = []model.Approach{}
		}
		out[loc] = append(out[loc], o)
	}
	return out, rows.Err()
}

// CreateObjects создаёт объекты клетки; повтор ключа внутри клетки — ошибка БД.
func (s *Store) CreateObjects(ctx context.Context, game model.GameID, loc model.LocationID, objects []model.LocationObject) error {
	const q = `INSERT INTO world_objects (game_id, location_id, key, title, description, state, revealed, hidden_until_revealed, searched, approaches)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	for _, o := range objects {
		approaches := o.Approaches
		if approaches == nil {
			approaches = []model.Approach{}
		}
		if _, err := s.db(ctx).Exec(ctx, q, game, loc, o.Key, o.Title, o.Description, o.State, o.Revealed, o.HiddenUntil, o.Searched, approaches); err != nil {
			return fmt.Errorf("postgres.CreateObjects: %s: %w", o.Key, err)
		}
	}
	return nil
}

// SaveObjectState сохраняет изменяемые поля объекта после свёртки событий: состояние, раскрытие, осмотр.
func (s *Store) SaveObjectState(ctx context.Context, game model.GameID, loc model.LocationID, o model.LocationObject) error {
	const q = `UPDATE world_objects SET state = $4, revealed = $5, searched = $6, updated_at = now() WHERE game_id = $1 AND location_id = $2 AND key = $3`
	if _, err := s.db(ctx).Exec(ctx, q, game, loc, o.Key, o.State, o.Revealed, o.Searched); err != nil {
		return fmt.Errorf("postgres.SaveObjectState: %w", err)
	}
	return nil
}

// ReplaceObjects удаляет объекты клетки и создаёт новые (стартовый пакет, сброс).
func (s *Store) ReplaceObjects(ctx context.Context, game model.GameID, loc model.LocationID, objects []model.LocationObject) error {
	if _, err := s.db(ctx).Exec(ctx, `DELETE FROM world_objects WHERE game_id = $1 AND location_id = $2`, game, loc); err != nil {
		return fmt.Errorf("postgres.ReplaceObjects: %w", err)
	}
	return s.CreateObjects(ctx, game, loc, objects)
}

// AddDiscovery отмечает первый вход персонажа фракции в клетку и увеличивает счётчик исследования фракции;
// повтор пары (клетка, фракция) ничего не меняет (false).
func (s *Store) AddDiscovery(ctx context.Context, game model.GameID, loc model.LocationID, faction model.FactionID, round model.RoundID, character model.CharacterID) (bool, error) {
	const q = `INSERT INTO location_discoveries (game_id, location_id, faction_id, round_id, character_id) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (location_id, faction_id) DO NOTHING`
	tag, err := s.db(ctx).Exec(ctx, q, game, loc, faction, round, character)
	if err != nil {
		return false, fmt.Errorf("postgres.AddDiscovery: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := s.db(ctx).Exec(ctx, `UPDATE factions SET explored_since_reset = explored_since_reset + 1 WHERE game_id = $1 AND id = $2`, game, faction); err != nil {
		return false, fmt.Errorf("postgres.AddDiscovery: counter: %w", err)
	}
	return true, nil
}

// ListDiscoveries возвращает открывшие фракции по клеткам игры.
func (s *Store) ListDiscoveries(ctx context.Context, game model.GameID) (map[model.LocationID][]model.FactionID, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT location_id, faction_id FROM location_discoveries WHERE game_id = $1 ORDER BY id`, game)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListDiscoveries: %w", err)
	}
	defer rows.Close()
	out := map[model.LocationID][]model.FactionID{}
	for rows.Next() {
		var loc model.LocationID
		var f model.FactionID
		if err := rows.Scan(&loc, &f); err != nil {
			return nil, fmt.Errorf("postgres.ListDiscoveries: scan: %w", err)
		}
		out[loc] = append(out[loc], f)
	}
	return out, rows.Err()
}

// ResetWorld — сброс мира для dev-команды `/reset`: остаются только стартовая клетка (без объектов и посещения),
// открытия и счётчики исследования обнуляются, сохранённые ответы модели, квесты и цели фракций удаляются (пункт 28).
func (s *Store) ResetWorld(ctx context.Context, game model.GameID) error {
	for _, q := range []string{
		`DELETE FROM faction_goals WHERE game_id = $1`,
		`DELETE FROM quests WHERE game_id = $1`,
		`DELETE FROM location_discoveries WHERE game_id = $1`,
		`UPDATE factions SET explored_since_reset = 0 WHERE game_id = $1`,
		`DELETE FROM generation_jobs WHERE game_id = $1`,
		`UPDATE characters SET location_id = (SELECT id FROM locations WHERE game_id = $1 AND start LIMIT 1) WHERE game_id = $1`,
		`UPDATE parties SET location_id = NULL WHERE game_id = $1`,
		`DELETE FROM locations WHERE game_id = $1 AND NOT start`,
		`DELETE FROM world_objects WHERE game_id = $1`,
		`UPDATE locations SET visited_at = NULL, stage = 'detailed', hidden = false, distant_view = '', updated_at = now() WHERE game_id = $1`,
	} {
		if _, err := s.db(ctx).Exec(ctx, q, game); err != nil {
			return fmt.Errorf("postgres.ResetWorld: %w", err)
		}
	}
	return nil
}

// GetGenerationJob возвращает сохранённый ответ модели по ключу задания.
func (s *Store) GetGenerationJob(ctx context.Context, game model.GameID, round *model.RoundID, role model.LLMRole, hash string) (model.GenerationJob, error) {
	const q = `SELECT id, game_id, round_id, role, input_hash, response, created_at FROM generation_jobs
WHERE game_id = $1 AND coalesce(round_id, 0) = coalesce($2, 0) AND role = $3 AND input_hash = $4`
	var j model.GenerationJob
	var resp cachedResponse
	err := s.db(ctx).QueryRow(ctx, q, game, round, role, hash).Scan(&j.ID, &j.GameID, &j.RoundID, &j.Role, &j.InputHash, &resp, &j.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.GenerationJob{}, model.ErrNotFound
	}
	if err != nil {
		return model.GenerationJob{}, fmt.Errorf("postgres.GetGenerationJob: %w", err)
	}
	j.Response = resp.response()
	return j, nil
}

// SaveGenerationJob сохраняет ответ модели вне транзакции вызывающего (через пул), чтобы откат раунда его не
// потерял; повтор ключа молча пропускается.
func (s *Store) SaveGenerationJob(ctx context.Context, j model.GenerationJob) error {
	const q = `INSERT INTO generation_jobs (game_id, round_id, role, input_hash, response) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING`
	if _, err := s.pool.Exec(ctx, q, j.GameID, j.RoundID, j.Role, j.InputHash, cached(j.Response)); err != nil {
		return fmt.Errorf("postgres.SaveGenerationJob: %w", err)
	}
	return nil
}

// cachedResponse — ответ модели в `generation_jobs.response`. Цена не хранится: повторное использование ответа
// бесплатно (ADR-0032); `tokens_in`/`tokens_out` — итоги для строк до разбивки по видам.
type cachedResponse struct {
	Text       string          `json:"text,omitempty"`
	JSON       json.RawMessage `json:"json,omitempty"`
	Model      string          `json:"model,omitempty"`
	TokensIn   int             `json:"tokens_in"`
	TokensOut  int             `json:"tokens_out"`
	Usage      *model.LLMUsage `json:"usage,omitempty"`
	DurationMS int64           `json:"duration_ms,omitempty"`
}

func cached(r model.LLMResponse) cachedResponse {
	u := r.Usage
	return cachedResponse{Text: r.Text, JSON: r.JSON, Model: r.Model, TokensIn: u.TotalIn(), TokensOut: u.Output, Usage: &u, DurationMS: r.Duration.Milliseconds()}
}

func (c cachedResponse) response() model.LLMResponse {
	usage := model.LLMUsage{Input: c.TokensIn, Output: c.TokensOut}
	if c.Usage != nil {
		usage = *c.Usage
	}
	return model.LLMResponse{Text: c.Text, JSON: c.JSON, Model: c.Model, Usage: usage, Duration: time.Duration(c.DurationMS) * time.Millisecond}
}
