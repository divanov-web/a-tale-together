package round

import (
	"context"
	"log/slog"

	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// opening — завязка истории и стартовый пакет при `/begin` (ADR-0018, ADR-0019): рассказчик придумывает начало по
// жанру, фракциям, стартовой клетке и открытым описаниям персонажей, описывает стартовую клетку с объектами и
// даёт карточки недостающих соседей одним вызовом с ролью `opening` через `app/llm.Complete` (модель рассказчика
// профиля; ответ сохраняется по ключу задания). Без провайдера или при сбое — стартовая сцена и клетка шаблона
// жанра, соседи «неизведанные земли». Возвращает текст без заголовка; ошибка — только у записи в хранилище.
func (s *Service) opening(ctx context.Context, g model.Game, chat model.Chat, genre model.Genre, factions []model.Faction, chars []model.Character, locs []model.Location) (string, error) {
	fallback := openingTemplate(g, genre.Template.OpeningScene, factions)
	var start model.Location
	for _, l := range locs {
		if l.Start {
			start = l
		}
	}
	if start.ID == 0 && len(locs) > 0 {
		start = locs[0]
	}
	known := map[[2]int]bool{}
	for _, l := range locs {
		known[[2]int{l.X, l.Y}] = true
	}
	var tasks []narrator.NeighborTask
	for _, d := range model.Directions {
		dx, dy := d.Offset()
		if x, y := start.X+dx, start.Y+dy; start.ID != 0 && !known[[2]int{x, y}] {
			tasks = append(tasks, narrator.NeighborTask{X: x, Y: y, Direction: d, Of: start.ID, OfName: start.Name})
		}
	}
	if s.LLM == nil || start.ID == 0 {
		s.journal(ctx, g, chat.TelegramChatID, model.Round{}, "opening", map[string]any{"fallback": "рассказчик не настроен: стартовая сцена жанра"})
		return fallback, s.startPackage(ctx, g, start, narrator.OpeningResult{}, tasks)
	}
	def, err := s.characterDef(ctx, g)
	if err != nil {
		return "", err
	}
	p := narrator.OpeningParams{Title: g.Title, Genre: genre.Title, Tone: genre.Template.Tone, Mode: g.Mode, Def: def, Neighbors: tasks,
		Start: narrator.OpeningStart{LocationID: start.ID, Name: start.Name, Description: start.Description, Terrain: start.Terrain, Objects: []string{}}}
	for _, o := range start.Objects {
		p.Start.Objects = append(p.Start.Objects, o.Title)
	}
	titles := map[model.FactionID]string{}
	for _, f := range factions {
		titles[f.ID] = f.Title
		rel := map[string]string{}
		for k, v := range f.Relations {
			rel[string(k)] = v
		}
		p.Factions = append(p.Factions, narrator.Faction{Code: string(f.Code), Title: f.Title, Description: f.Description, Tone: f.Tone, Relations: rel})
	}
	for _, c := range chars {
		if c.Status.Absent() {
			continue
		}
		p.Characters = append(p.Characters, narrator.OpeningCharacter{Nickname: c.Nickname, Gender: string(c.Gender), Faction: titles[c.FactionID], Archetype: c.Archetype, PublicBio: c.PublicBio})
	}
	call := applm.Call{Game: &g, Role: model.RoleOpening, PromptVersion: narrator.OpeningPromptVersion}
	svc := narrator.New(narrator.Func(func(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		return s.complete(ctx, g, nil, call, req, nil)
	}), s.logger)
	res, err := svc.Opening(ctx, p)
	if err != nil {
		s.logger.Warn("opening unavailable, genre template", slog.Int64("game_id", int64(g.ID)), slog.Any("err", err))
		s.journal(ctx, g, chat.TelegramChatID, model.Round{}, "opening", map[string]any{"fallback": "стартовая сцена жанра", "error": err.Error(), "calls": res.Calls})
		return fallback, s.startPackage(ctx, g, start, narrator.OpeningResult{}, tasks)
	}
	s.logger.Info("opening narrated", slog.Int64("game_id", int64(g.ID)), slog.Int("calls", res.Calls), slog.Int("issues", len(res.Issues)),
		slog.Bool("start_from_model", res.Start != nil), slog.Int("neighbors", len(res.Neighbors)),
		slog.Int("tokens_in", res.TokensIn), slog.Int("tokens_out", res.TokensOut))
	s.journal(ctx, g, chat.TelegramChatID, model.Round{}, "opening", map[string]any{"calls": res.Calls, "issues": res.Issues, "text": res.Text, "start": res.Start, "neighbors": res.Neighbors})
	return res.Text, s.startPackage(ctx, g, start, res, tasks)
}
