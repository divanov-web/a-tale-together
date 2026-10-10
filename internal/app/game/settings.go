package game

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"taletogether/internal/model"
)

// Param — настраиваемый параметр игры для /game_set и диалога настройки.
type Param struct {
	Key   string // ключ в команде /game_set
	Title string // название для игрока
	Hint  string // формат значения
}

// Params — параметры в порядке диалога настройки.
var Params = []Param{
	{Key: "title", Title: "Название мира", Hint: "текст до 60 символов"},
	{Key: "genre", Title: "Жанр", Hint: "код жанра из списка"},
	{Key: "round_minutes", Title: "Длительность раунда", Hint: "минуты от 10 до 60"},
	{Key: "timezone", Title: "Часовой пояс", Hint: "например Europe/Moscow"},
	{Key: "play_window", Title: "Игровое окно", Hint: "HH:MM-HH:MM по часовому поясу игры или «нет»"},
	{Key: "ends_at", Title: "Дата окончания рассказа", Hint: "ГГГГ-ММ-ДД или «нет»"},
	{Key: "max_rounds", Title: "Число раундов", Hint: "число или 0 без границы"},
	{Key: "max_players", Title: "Лимит игроков", Hint: "от 1 до 20"},
	{Key: "max_locations", Title: "Лимит активных локаций", Hint: "от 1 до 4"},
	{Key: "min_answers", Title: "Кворум раунда", Hint: "заявок от 1 до 20; если меньше, раунд пропускается"},
	{Key: "faction_good", Title: "Фракция добра", Hint: "имя по сюжету до 40 символов"},
	{Key: "faction_evil", Title: "Фракция зла", Hint: "имя по сюжету до 40 символов"},
	{Key: "faction_neutral", Title: "Нейтральная фракция", Hint: "имя по сюжету до 40 символов"},
}

// Set задаёт параметр игры; право — CanAdmin. Формат и режим на этапе 1 не меняются.
func (s *Service) Set(ctx context.Context, game model.Game, actor model.UserID, key, value string) (model.Game, error) {
	ok, err := s.CanAdmin(ctx, game, actor)
	if err != nil {
		return model.Game{}, fmt.Errorf("game.Set: %w", err)
	}
	if !ok {
		return model.Game{}, ErrForbidden
	}
	value = strings.TrimSpace(value)
	key = strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(key, "faction_") {
		return game, s.setFaction(ctx, game, model.FactionCode(strings.TrimPrefix(key, "faction_")), value)
	}
	if err := s.apply(ctx, &game, key, value); err != nil {
		return model.Game{}, err
	}
	if err := s.store.UpdateGame(ctx, game); err != nil {
		return model.Game{}, fmt.Errorf("game.Set: %w", err)
	}
	return game, nil
}

func (s *Service) setFaction(ctx context.Context, game model.Game, code model.FactionCode, title string) error {
	if n := len([]rune(title)); n < 2 || n > 40 {
		return fmt.Errorf("%w: имя фракции от 2 до 40 символов", ErrBadValue)
	}
	factions, err := s.store.ListFactions(ctx, game.ID)
	if err != nil {
		return fmt.Errorf("game.Set: %w", err)
	}
	for _, f := range factions {
		if f.Code == code {
			f.Title = title
			if err := s.store.UpdateFaction(ctx, f); err != nil {
				return fmt.Errorf("game.Set: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: нет фракции %s", ErrBadValue, code)
}

// apply меняет поле игры в памяти по ключу параметра.
func (s *Service) apply(ctx context.Context, g *model.Game, key, value string) error {
	no := func(v string) bool {
		v = strings.ToLower(v)
		return v == "нет" || v == "-" || v == "0" || v == "no" || v == ""
	}
	switch key {
	case "title":
		if n := len([]rune(value)); n < 2 || n > 60 {
			return fmt.Errorf("%w: название от 2 до 60 символов", ErrBadValue)
		}
		g.Title = value
	case "genre":
		genre, err := s.store.GetGenreByCode(ctx, strings.ToLower(value))
		if errors.Is(err, model.ErrNotFound) {
			return fmt.Errorf("%w: неизвестный жанр %q", ErrBadValue, value)
		}
		if err != nil {
			return fmt.Errorf("game.Set: %w", err)
		}
		g.GenreID = genre.ID
	case "round_minutes":
		n, err := strconv.Atoi(value)
		if err != nil || n < 10 || n > 60 {
			return fmt.Errorf("%w: длительность раунда от 10 до 60 минут", ErrBadValue)
		}
		g.RoundMinutes = n
	case "timezone":
		if _, err := time.LoadLocation(value); err != nil || value == "" {
			return fmt.Errorf("%w: часовой пояс в формате Europe/Moscow", ErrBadValue)
		}
		g.Timezone = value
	case "play_window":
		if no(value) {
			g.PlayFrom, g.PlayTo = "", ""
			return nil
		}
		from, to, ok := strings.Cut(strings.ReplaceAll(value, " ", ""), "-")
		if !ok || !validClock(from) || !validClock(to) {
			return fmt.Errorf("%w: окно в формате HH:MM-HH:MM", ErrBadValue)
		}
		g.PlayFrom, g.PlayTo = from, to
	case "ends_at":
		if no(value) {
			g.Settings.EndsAt = nil
			return nil
		}
		loc, err := time.LoadLocation(g.Timezone)
		if err != nil {
			loc = time.UTC
		}
		t, err := time.ParseInLocation("2006-01-02", value, loc)
		if err != nil {
			return fmt.Errorf("%w: дата в формате ГГГГ-ММ-ДД", ErrBadValue)
		}
		if !t.After(s.clock.Now()) {
			return fmt.Errorf("%w: дата окончания должна быть в будущем", ErrBadValue)
		}
		g.Settings.EndsAt = &t
	case "max_rounds":
		n, err := strconv.Atoi(value)
		if no(value) {
			n, err = 0, nil
		}
		if err != nil || n < 0 || n > 1000 {
			return fmt.Errorf("%w: число раундов от 0 до 1000", ErrBadValue)
		}
		g.Settings.MaxRounds = n
	case "max_players":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 20 {
			return fmt.Errorf("%w: лимит игроков от 1 до 20", ErrBadValue)
		}
		g.Settings.MaxPlayers = n
	case "max_locations":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 4 {
			return fmt.Errorf("%w: лимит локаций от 1 до 4", ErrBadValue)
		}
		g.Settings.MaxActiveLocations = n
	case "min_answers":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 20 {
			return fmt.Errorf("%w: кворум раунда от 1 до 20 заявок", ErrBadValue)
		}
		g.Settings.MinRoundAnswers = n
	default:
		return fmt.Errorf("%w: неизвестный параметр %q", ErrBadValue, key)
	}
	return nil
}

func validClock(v string) bool {
	_, err := time.Parse("15:04", v)
	return err == nil
}
