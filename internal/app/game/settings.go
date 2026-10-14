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

// Param — настраиваемый параметр игры для /set и диалога настройки.
type Param struct {
	Key      string // ключ в команде /set
	Title    string // название для игрока
	Hint     string // формат значения
	Advanced bool   // только через /set, в диалог настройки не входит
}

// Params — параметры в порядке диалога настройки; Advanced задаются только командой /set.
var Params = []Param{
	{Key: ScenarioParam, Title: "Стартовый мир", Hint: "номер варианта от 1 до 4 или «-» — по шаблону жанра"}, // варианты от нулевого вызова (ADR-0028); шаг пропускается без модели и после неудачной генерации
	{Key: "title", Title: "Название мира", Hint: "текст до 60 символов"},
	{Key: "genre", Title: "Жанр", Hint: "код жанра из списка"},
	{Key: "round_minutes", Title: "Длительность раунда", Hint: "минуты от 5 до 60"},
	{Key: "timezone", Title: "Часовой пояс", Hint: "например Europe/Moscow"},
	{Key: "play_window", Title: "Игровое окно", Hint: "HH:MM-HH:MM по часовому поясу игры или «нет»"},
	{Key: "ends_at", Title: "Дата окончания рассказа", Hint: "ГГГГ-ММ-ДД или «нет»", Advanced: true}, // по умолчанию «нет»
	{Key: "max_rounds", Title: "Число раундов", Hint: "число или 0 без границы", Advanced: true},     // по умолчанию 0; в диалог вернётся с завершением игры рассказчиком (G13)
	{Key: "min_answers", Title: "Кворум раунда", Hint: "заявок от 1 до 20; если меньше, раунд пропускается"},
	{Key: "faction_good", Title: "Фракция добра", Hint: "имя по сюжету до 40 символов"},
	{Key: "faction_evil", Title: "Фракция зла", Hint: "имя по сюжету до 40 символов"},
}

// ScenarioParam — ключ параметра «стартовый мир»: номер варианта из `games.world_setups` (ADR-0028).
const ScenarioParam = "scenario"

// ErrSetupsPending — варианты стартового мира ещё не получены.
var ErrSetupsPending = errors.New("варианты стартового мира ещё придумываются: подождите сообщение с кнопками или ответьте «-»")

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
	if key == ScenarioParam {
		return s.setScenario(ctx, game, value)
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
		if genre.ID != g.GenreID && g.Status == model.GameSetup {
			// Варианты стартового мира придуманы под прежний жанр: тик планировщика сгенерирует новые (ADR-0028).
			now := s.clock.Now()
			g.WorldSetups, g.NextRoundAt = nil, &now
		}
		g.GenreID = genre.ID
	case "round_minutes":
		n, err := strconv.Atoi(value)
		if err != nil || n < s.MinRoundMinutes || n > model.MaxRoundMinutes {
			return fmt.Errorf("%w: длительность раунда от %d до %d минут", ErrBadValue, s.MinRoundMinutes, model.MaxRoundMinutes)
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

// setScenario применяет вариант стартового мира (ADR-0028) до запуска игры: номер варианта — название мира, фракции
// и стартовая клетка из варианта; «-» или 0 — обратно к шаблону жанра. Название мира, фракции и клетка в одной
// транзакции; администратор может затем переименовать мир и фракции обычными параметрами.
func (s *Service) setScenario(ctx context.Context, game model.Game, value string) (model.Game, error) {
	if game.Status != model.GameSetup && game.Status != model.GameDraft {
		return model.Game{}, fmt.Errorf("%w: стартовый мир меняется только до запуска игры", ErrBadValue)
	}
	n := 0
	if v := strings.ToLower(value); v != "-" && v != "нет" && v != "0" && v != "" {
		var err error
		if n, err = strconv.Atoi(v); err != nil || n < 1 {
			return model.Game{}, fmt.Errorf("%w: номер варианта от 1 до %d", ErrBadValue, model.WorldSetupOptions)
		}
		if game.WorldSetups.Pending() {
			return model.Game{}, ErrSetupsPending
		}
		if n > len(game.WorldSetups.Options) {
			return model.Game{}, fmt.Errorf("%w: вариантов всего %d", ErrBadValue, len(game.WorldSetups.Options))
		}
	}
	if game.WorldSetups == nil {
		game.WorldSetups = &model.WorldSetups{}
	}
	game.WorldSetups.ChosenNo = n
	genre, err := s.store.GetGenre(ctx, game.GenreID)
	if err != nil {
		return model.Game{}, fmt.Errorf("game.Set: %w", err)
	}
	err = s.store.InTx(ctx, func(ctx context.Context) error {
		factions, err := s.store.ListFactions(ctx, game.ID)
		if err != nil {
			return err
		}
		locs, err := s.store.ListLocations(ctx, game.ID)
		if err != nil {
			return err
		}
		setup := game.Setup()
		for _, f := range factions {
			if setup != nil {
				sf, ok := setup.Faction(f.Code)
				if !ok {
					continue
				}
				f.Title, f.Description, f.Tone = sf.Title, sf.Description, sf.Tone
			} else {
				var found bool
				for _, ft := range genre.Template.Factions {
					if ft.Code == f.Code {
						f.Title, f.Description, f.Tone, found = ft.Title, ft.Description, ft.Tone, true
					}
				}
				if !found {
					continue
				}
			}
			if err := s.store.UpdateFaction(ctx, f); err != nil {
				return err
			}
		}
		for _, l := range locs {
			if !l.Start {
				continue
			}
			if setup != nil {
				l.Name, l.Terrain, l.Description = setup.Start.Name, setup.Start.Terrain, setup.Start.Description
			} else if lt, ok := templateStart(genre.Template); ok {
				l.Name, l.Terrain, l.Description = lt.Name, lt.Terrain, lt.Description
			} else {
				continue
			}
			if err := s.store.UpdateLocation(ctx, l); err != nil {
				return err
			}
		}
		if setup != nil {
			game.Title = setup.World
		}
		return s.store.UpdateGame(ctx, game)
	})
	if err != nil {
		return model.Game{}, fmt.Errorf("game.Set: scenario: %w", err)
	}
	return game, nil
}

// templateStart — стартовая клетка шаблона жанра: помеченная `start`, иначе первая.
func templateStart(t model.GenreTemplate) (model.LocationTemplate, bool) {
	if len(t.Locations) == 0 {
		return model.LocationTemplate{}, false
	}
	lt := t.Locations[0]
	for _, candidate := range t.Locations {
		if candidate.Start {
			lt = candidate
		}
	}
	return lt, true
}
