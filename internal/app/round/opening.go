package round

import (
	"context"
	"log/slog"

	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// opening — завязка истории при `/begin` (ADR-0018): рассказчик придумывает начало по жанру, фракциям, локациям и
// открытым описаниям персонажей одним вызовом с ролью `opening` через `app/llm.Complete` (модель рассказчика профиля).
// Без провайдера или при сбое — стартовая сцена из шаблона жанра. Возвращает текст без заголовка.
func (s *Service) opening(ctx context.Context, g model.Game, chat model.Chat, genre model.Genre, factions []model.Faction, chars []model.Character, locs []model.Location) string {
	fallback := openingTemplate(g, genre.Template.OpeningScene, factions)
	if s.LLM == nil {
		s.journal(ctx, g, chat.TelegramChatID, model.Round{}, "opening", map[string]any{"fallback": "рассказчик не настроен: стартовая сцена жанра"})
		return fallback
	}
	p := narrator.OpeningParams{Title: g.Title, Genre: genre.Title, Tone: genre.Template.Tone, Mode: g.Mode}
	titles := map[model.FactionID]string{}
	for _, f := range factions {
		titles[f.ID] = f.Title
		rel := map[string]string{}
		for k, v := range f.Relations {
			rel[string(k)] = v
		}
		p.Factions = append(p.Factions, narrator.Faction{Code: string(f.Code), Title: f.Title, Description: f.Description, Tone: f.Tone, Relations: rel})
	}
	for _, l := range locs {
		if !l.Active {
			continue
		}
		exits := []string{}
		for _, link := range l.Links {
			if !link.Hidden {
				exits = append(exits, link.To)
			}
		}
		p.Locations = append(p.Locations, narrator.OpeningLocation{Name: l.Name, Description: l.Description, Start: l.Start, Exits: exits})
	}
	for _, c := range chars {
		if c.Status.Absent() {
			continue
		}
		p.Characters = append(p.Characters, narrator.OpeningCharacter{Nickname: c.Nickname, Gender: string(c.Gender), Faction: titles[c.FactionID], Archetype: c.Archetype, PublicBio: c.PublicBio})
	}
	call := applm.Call{Game: &g, Role: model.RoleOpening, PromptVersion: narrator.OpeningPromptVersion}
	svc := narrator.New(narrator.Func(func(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		return s.LLM.Complete(ctx, call, req)
	}), s.logger)
	res, err := svc.Opening(ctx, p)
	if err != nil {
		s.logger.Warn("opening unavailable, genre template", slog.Int64("game_id", int64(g.ID)), slog.Any("err", err))
		s.journal(ctx, g, chat.TelegramChatID, model.Round{}, "opening", map[string]any{"fallback": "стартовая сцена жанра", "error": err.Error(), "calls": res.Calls})
		return fallback
	}
	s.logger.Info("opening narrated", slog.Int64("game_id", int64(g.ID)), slog.Int("calls", res.Calls), slog.Int("issues", len(res.Issues)),
		slog.Int("tokens_in", res.TokensIn), slog.Int("tokens_out", res.TokensOut))
	s.journal(ctx, g, chat.TelegramChatID, model.Round{}, "opening", map[string]any{"calls": res.Calls, "issues": res.Issues, "text": res.Text})
	return res.Text
}
