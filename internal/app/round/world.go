package round

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
	"taletogether/internal/service/engine"
	"taletogether/internal/service/narrator"
)

// Пункт 27 (docs/game/world.md, ADR-0019): мир — сетка клеток, которую рассказчик достраивает в вызове раунда.
// Здесь: локальный снимок (занятые клетки и соседи), применение пакета мира событиями в той же транзакции,
// стартовый пакет при `/begin`, сохранённые ответы модели по ключу задания.

// relevant — клетки для снимка: занятые персонажами (и группами) и их соседи по четырём направлениям. Остальная
// карта в раунде не нужна: переход возможен только к соседу, объекты — только в занятых клетках.
func relevant(locations []model.Location, chars []model.Character) []model.Location {
	byXY := map[[2]int]int{}
	for i, l := range locations {
		byXY[[2]int{l.X, l.Y}] = i
	}
	keep := map[int]bool{}
	for _, c := range chars {
		if c.LocationID == nil {
			continue
		}
		for i, l := range locations {
			if l.ID != *c.LocationID {
				continue
			}
			keep[i] = true
			for _, d := range model.Directions {
				dx, dy := d.Offset()
				if j, ok := byXY[[2]int{l.X + dx, l.Y + dy}]; ok {
					keep[j] = true
				}
			}
		}
	}
	out := make([]model.Location, 0, len(keep))
	for i, l := range locations {
		if keep[i] {
			out = append(out, l)
		}
	}
	return out
}

// cellPayload — полезная нагрузка `cell_generated`.
type cellPayload struct {
	LocationID  model.LocationID    `json:"location_id"`
	X           int                 `json:"x"`
	Y           int                 `json:"y"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Difficulty  int                 `json:"difficulty"`
	Terrain     string              `json:"terrain"`
	Stage       model.LocationStage `json:"stage"`
	Source      string              `json:"source"` // model | template
}

// applyWorld записывает пакет мира: детализирует клетки (строка локации, объекты) и создаёт карточки соседей,
// каждое изменение — событием `cell_generated` / `object_created` с видимостью в клетке; события дописываются в журнал
// за событиями движка и сворачиваются в состояние, чтобы варианты следующего раунда видели новые объекты.
func (s *Service) applyWorld(ctx context.Context, g model.Game, r model.Round, state *model.RoundSnapshot, pkg narrator.WorldPackage, now time.Time) ([]model.Event, error) {
	var events []model.Event
	emit := func(t model.EventType, loc model.LocationID, payload any) error {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		id := loc
		events = append(events, model.Event{GameID: g.ID, RoundID: r.ID, Type: t, Payload: b, Visibility: model.VisibilityLocation, LocationID: &id})
		return nil
	}
	for _, cell := range pkg.Cells {
		l, ok := state.Location(cell.LocationID)
		if !ok {
			continue
		}
		visited := now
		loc := model.Location{ID: l.ID, GameID: g.ID, Name: cell.Name, Description: cell.Description, Difficulty: cell.Difficulty, X: l.X, Y: l.Y, Stage: model.StageDetailed, Terrain: cell.Terrain, VisitedAt: &visited, Start: l.Start}
		if err := s.store.UpdateLocation(ctx, loc); err != nil {
			return nil, err
		}
		if err := s.store.CreateObjects(ctx, g.ID, l.ID, cell.Objects); err != nil {
			return nil, err
		}
		source := "model"
		if cell.Template {
			source = "template"
		}
		if err := emit(model.EventCellGenerated, l.ID, cellPayload{LocationID: l.ID, X: l.X, Y: l.Y, Name: cell.Name, Description: cell.Description, Difficulty: cell.Difficulty, Terrain: cell.Terrain, Stage: model.StageDetailed, Source: source}); err != nil {
			return nil, err
		}
		for _, o := range cell.Objects {
			if err := emit(model.EventObjectCreated, l.ID, map[string]any{"location_id": l.ID, "object": o}); err != nil {
				return nil, err
			}
		}
	}
	for _, n := range pkg.Neighbors {
		created, err := s.store.CreateLocation(ctx, model.Location{GameID: g.ID, Name: n.Name, Description: n.Card, X: n.X, Y: n.Y, Stage: model.StageCard, Terrain: n.Terrain})
		if err != nil {
			return nil, err
		}
		source := "model"
		if n.Template {
			source = "template"
		}
		if err := emit(model.EventCellGenerated, created.ID, cellPayload{LocationID: created.ID, X: n.X, Y: n.Y, Name: n.Name, Description: n.Card, Terrain: n.Terrain, Stage: model.StageCard, Source: source}); err != nil {
			return nil, err
		}
	}
	if len(events) == 0 {
		return nil, nil
	}
	offset, err := s.store.NextEventSeq(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	for i := range events {
		events[i].Seq = offset + int64(i)
	}
	if err := s.store.AppendEvents(ctx, events); err != nil {
		return nil, err
	}
	if err := engine.Apply(state, events); err != nil {
		return nil, err
	}
	return events, nil
}

// planTasks — задания на генерацию по состоянию после свёртки и всей карте игры (сосед может существовать вне снимка).
func (s *Service) planTasks(ctx context.Context, g model.Game, state *model.RoundSnapshot) (narrator.Tasks, error) {
	all, err := s.store.ListLocations(ctx, g.ID)
	if err != nil {
		return narrator.Tasks{}, err
	}
	known := map[[2]int]bool{}
	for _, l := range all {
		known[[2]int{l.X, l.Y}] = true
	}
	return narrator.PlanTasks(state, func(x, y int) bool { return known[[2]int{x, y}] }), nil
}

// startPackage применяет стартовый пакет завязки (ADR-0019): клетка от модели заменяет описание и объекты
// стартовой клетки шаблона, карточки соседей создаются по направлениям; без ответа модели — соседи по шаблону.
// Событий нет: игра ещё без раундов, стартовый мир — нулевой снимок.
func (s *Service) startPackage(ctx context.Context, g model.Game, start model.Location, res narrator.OpeningResult, tasks []narrator.NeighborTask) error {
	if res.Start != nil {
		loc := start
		loc.Name, loc.Description, loc.Difficulty = res.Start.Name, res.Start.Description, res.Start.Difficulty
		if res.Start.Terrain != "" {
			loc.Terrain = res.Start.Terrain
		}
		if err := s.store.UpdateLocation(ctx, loc); err != nil {
			return err
		}
		if err := s.store.ReplaceObjects(ctx, g.ID, loc.ID, res.Start.Objects); err != nil {
			return err
		}
	}
	neighbors := res.Neighbors
	if neighbors == nil {
		neighbors = narrator.TemplateWorld(narrator.Tasks{Neighbors: tasks}).Neighbors
	}
	for _, n := range neighbors {
		if _, err := s.store.CreateLocation(ctx, model.Location{GameID: g.ID, Name: n.Name, Description: n.Card, X: n.X, Y: n.Y, Stage: model.StageCard, Terrain: n.Terrain}); err != nil {
			return err
		}
	}
	return nil
}

// complete — вызов модели с сохранением ответа по ключу задания (ADR-0019): хэш системной части, входа и схемы
// в `generation_jobs` вне транзакции; повтор раунда после отката или перезапуска берёт готовый ответ без вызова
// модели и платы, мир не генерируется второй раз. Ошибка кэша только в лог: вызов идёт как обычно.
func (s *Service) complete(ctx context.Context, g model.Game, round *model.RoundID, call applm.Call, req model.LLMRequest, reused *int) (model.LLMResponse, error) {
	hash := inputHash(call.PromptVersion, req)
	if job, err := s.store.GetGenerationJob(ctx, g.ID, round, call.Role, hash); err == nil {
		s.logger.Info("llm reply reused", slog.Int64("game_id", int64(g.ID)), slog.String("role", string(call.Role)), slog.String("hash", hash[:12]))
		if reused != nil {
			*reused++
		}
		return job.Response, nil
	} else if !errors.Is(err, model.ErrNotFound) {
		s.logger.Warn("llm reply cache unavailable", slog.Int64("game_id", int64(g.ID)), slog.Any("err", err))
	}
	resp, err := s.LLM.Complete(ctx, call, req)
	if err != nil {
		return resp, err
	}
	if saveErr := s.store.SaveGenerationJob(ctx, model.GenerationJob{GameID: g.ID, RoundID: round, Role: call.Role, InputHash: hash, Response: resp}); saveErr != nil {
		s.logger.Warn("llm reply not cached", slog.Int64("game_id", int64(g.ID)), slog.Any("err", saveErr))
	}
	return resp, nil
}

// inputHash — ключ задания: версия промпта, системная часть, вход и схема.
func inputHash(version string, req model.LLMRequest) string {
	h := sha256.New()
	for _, part := range []string{version, string(req.Role), req.System, req.Input, string(req.JSONSchema)} {
		fmt.Fprintf(h, "%d:%s\x00", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}
