package round

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	applm "taletogether/internal/app/llm"
	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// worldgenRetryAfter — пауза между попытками нулевого вызова после сбоя модели.
const worldgenRetryAfter = time.Minute

// ScenarioStep — ключ параметра настройки «стартовый мир»; кнопки вариантов несут ответ этого шага (ADR-0015).
const ScenarioStep = "scenario"

// WorldgenEnabled — нулевой вызов возможен: модель подключена. Без модели шаг выбора стартового мира пропускается.
func (s *Service) WorldgenEnabled() bool { return s.LLM != nil }

// worldgen — нулевой вызов «генерация мира» в транзакции тика для игры `setup` (ADR-0028): по описанию жанра
// рассказчик предлагает четыре варианта стартового мира; они сохраняются в `games.world_setups`, администратору в
// личку через outbox уходит список с кнопками выбора. Сбой модели — следующая попытка через минуту, после
// `model.MaxWorldgenAttempts` попыток мир остаётся по шаблону жанра и администратор получает об этом сообщение.
// Ответ сохраняется по ключу задания (`complete`): откат транзакции второй вызов не делает. Возвращает ошибку только
// от хранилища: сбой модели раунда не касается и в `noteFailure` не попадает.
func (s *Service) worldgen(ctx context.Context, g *model.Game, now time.Time) error {
	chat, err := s.store.GetChat(ctx, g.ChatID)
	if err != nil {
		return err
	}
	ws := g.WorldSetups
	if ws == nil {
		ws = &model.WorldSetups{}
	}
	if !ws.Pending() {
		g.NextRoundAt = nil
		return s.store.UpdateGame(ctx, *g)
	}
	if s.LLM == nil {
		ws.Attempts = model.MaxWorldgenAttempts
		g.WorldSetups, g.NextRoundAt = ws, nil
		s.journal(ctx, *g, chat.TelegramChatID, model.Round{}, "worldgen", map[string]any{"fallback": "рассказчик не настроен: мир по шаблону жанра"})
		return s.store.UpdateGame(ctx, *g)
	}
	genre, err := s.store.GetGenre(ctx, g.GenreID)
	if err != nil {
		return err
	}
	ws.Attempts++
	p := narrator.WorldgenParams{Genre: genre.Title, Tone: genre.Template.Tone, Seed: fmt.Sprintf("game-%d-attempt-%d", g.ID, ws.Attempts)}
	for _, f := range genre.Template.Factions {
		p.Factions = append(p.Factions, narrator.Faction{Code: string(f.Code), Title: f.Title, Description: f.Description, Tone: f.Tone, Relations: f.Relations})
	}
	if locs, err := s.store.ListLocations(ctx, g.ID); err != nil {
		return err
	} else {
		for _, l := range locs {
			if l.Start {
				p.Start = narrator.SetupStart{Name: l.Name, Terrain: l.Terrain, Description: l.Description}
			}
		}
	}
	call := applm.Call{Game: g, Role: model.RoleWorldgen, PromptVersion: narrator.WorldgenPromptVersion}
	game := *g
	svc := narrator.New(narrator.Func(func(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		return s.complete(ctx, game, nil, call, req, nil)
	}), s.logger)
	res, err := svc.Worldgen(ctx, p)
	if err != nil {
		s.logger.Warn("worldgen failed", slog.Int64("game_id", int64(g.ID)), slog.Int("attempt", ws.Attempts), slog.Any("err", err))
		data := map[string]any{"attempt": ws.Attempts, "error": err.Error(), "issues": res.Issues}
		if ws.Attempts >= model.MaxWorldgenAttempts {
			data["fallback"] = "мир по шаблону жанра"
			g.WorldSetups, g.NextRoundAt = ws, nil
			s.journal(ctx, *g, chat.TelegramChatID, model.Round{}, "worldgen", data)
			if err := s.store.UpdateGame(ctx, *g); err != nil {
				return err
			}
			return s.notifyAdmin(ctx, *g, worldgenFailedText(*g), nil)
		}
		retry := now.Add(worldgenRetryAfter)
		g.WorldSetups, g.NextRoundAt = ws, &retry
		s.journal(ctx, *g, chat.TelegramChatID, model.Round{}, "worldgen", data)
		return s.store.UpdateGame(ctx, *g)
	}
	ws.Options = res.Options
	g.WorldSetups, g.NextRoundAt = ws, nil
	s.logger.Info("worldgen done", slog.Int64("game_id", int64(g.ID)), slog.Int("options", len(res.Options)), slog.Int("attempt", ws.Attempts),
		slog.Int("tokens_in", res.TokensIn), slog.Int("tokens_out", res.TokensOut))
	s.journal(ctx, *g, chat.TelegramChatID, model.Round{}, "worldgen", map[string]any{"attempt": ws.Attempts, "options": res.Options, "issues": res.Issues})
	if err := s.store.UpdateGame(ctx, *g); err != nil {
		return err
	}
	return s.notifyAdmin(ctx, *g, SetupOptionsText(*g), SetupOptionsKeyboard(*g))
}

// notifyAdmin ставит личное сообщение администратору игры в outbox той же транзакции; без открытой лички молчит:
// варианты он увидит на шаге настройки.
func (s *Service) notifyAdmin(ctx context.Context, g model.Game, text string, kb model.Keyboard) error {
	user, err := s.store.GetUser(ctx, g.CreatedByUserID)
	if err != nil {
		return err
	}
	if user.StartedAt == nil {
		return nil
	}
	return s.store.EnqueueOutbox(ctx, model.OutboxMessage{GameID: g.ID, TelegramChatID: user.PrivateChat(), Kind: model.OutboxPrivate, Part: 1, Text: text, Keyboard: kb, SendAfter: s.clock.Now()})
}

// SetupOptionsText — список вариантов стартового мира для администратора: заголовок и краткое описание каждого,
// подсказка, как выбрать (кнопкой на шаге настройки или `/set scenario <номер>`).
func SetupOptionsText(g model.Game) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Варианты стартового мира «%s»\n", g.Title)
	if g.WorldSetups != nil {
		for i, o := range g.WorldSetups.Options {
			fmt.Fprintf(&b, "\n%d. %s — «%s»\n%s\n", i+1, o.Title, o.World, o.Summary)
		}
	}
	b.WriteString("\nВыберите вариант кнопкой или командой /set scenario <номер>.")
	return b.String()
}

// SetupOptionsKeyboard — кнопка на каждый вариант с ответом шага `scenario` (ADR-0015); кнопки «Оставить как есть»
// нет: администратор обязан выбрать из сгенерированных (решение руководителя 2026-10-14).
func SetupOptionsKeyboard(g model.Game) model.Keyboard {
	var kb model.Keyboard
	if g.WorldSetups != nil {
		for i, o := range g.WorldSetups.Options {
			kb = append(kb, []model.Button{{Text: fmt.Sprintf("%d. %s", i+1, o.Title), Data: model.CallbackAnswer(ScenarioStep, fmt.Sprint(i+1))}})
		}
	}
	return kb
}

func worldgenFailedText(g model.Game) string {
	return fmt.Sprintf("Варианты стартового мира «%s» придумать не удалось: мир остаётся по шаблону жанра. Настройку можно продолжить: /set", g.Title)
}
