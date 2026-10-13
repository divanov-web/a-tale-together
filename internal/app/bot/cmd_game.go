package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"taletogether/internal/app/bot/templates"
	"taletogether/internal/app/game"
	"taletogether/internal/model"
)

// create — /create: в группе создаёт мир, в личке — персонажа в текущей игре.
func (s *Service) create(ctx context.Context, req *request) error {
	if req.in.IsPrivate() {
		return s.characterCreate(ctx, req)
	}
	return s.gameCreate(ctx, req)
}

// gameCreate — /create в группе: создание мира и запуск настройки в личке администратора.
func (s *Service) gameCreate(ctx context.Context, req *request) error {
	g, err := s.games.Create(ctx, req.chat, req.user)
	if err != nil {
		return err
	}
	setupSent := false
	if req.user.StartedAt != nil {
		if err := s.startGameSetup(ctx, req.user, g); err == nil {
			setupSent = true
		} else {
			s.logger.Warn("setup dialog not started", "game_id", int64(g.ID), "err", err)
		}
	}
	text, err := templates.Render("game_created", map[string]any{
		"Title": g.Title, "Admin": req.user.Mention(), "JoinCode": g.JoinCode, "JoinLink": s.joinLink(g),
		"SetupSent": setupSent, "SetupLink": s.setupLink(g),
	})
	if err != nil {
		return err
	}
	return s.reply(ctx, req.in, text)
}

// gameStatus — /status: в группе игра чата, в личке текущая игра пользователя; администратору игры в `setup` в личке
// под сводкой кнопка «Продолжить настройку» (`/set`, ADR-0023).
func (s *Service) gameStatus(ctx context.Context, req *request) error {
	var g model.Game
	var err error
	if req.in.IsPrivate() {
		g, err = s.games.Current(ctx, req.user)
	} else {
		g, err = s.games.OfChat(ctx, req.chat.ID)
	}
	if err != nil {
		return err
	}
	text, err := s.renderStatus(ctx, g)
	if err != nil {
		return err
	}
	if req.in.IsPrivate() && g.Status == model.GameSetup {
		if ok, err := s.games.CanAdmin(ctx, g, req.user.ID); err != nil {
			return err
		} else if ok {
			return s.replyKeyboard(ctx, req.in, text, model.Keyboard{{{Text: "Продолжить настройку", Data: model.CallbackCommand("set", "")}}})
		}
	}
	return s.reply(ctx, req.in, text)
}

// renderStatus собирает сводку игры по шаблону status.
func (s *Service) renderStatus(ctx context.Context, g model.Game) (string, error) {
	st, err := s.games.Status(ctx, g)
	if err != nil {
		return "", err
	}
	type factionRow struct {
		Title, Code string
		Count       int
	}
	counts := map[model.FactionID]int{}
	titles := map[model.FactionID]string{}
	for _, c := range st.Characters {
		counts[c.FactionID]++
	}
	var factions []factionRow
	for _, f := range st.Factions {
		titles[f.ID] = f.Title
		factions = append(factions, factionRow{Title: f.Title, Code: string(f.Code), Count: counts[f.ID]})
	}
	type charRow struct{ Nickname, Archetype, Gender, Faction, Status string }
	var chars []charRow
	for _, c := range st.Characters {
		gender := ""
		if c.Gender != "" {
			gender = c.Gender.Word()
		}
		chars = append(chars, charRow{Nickname: c.Nickname, Archetype: c.Archetype, Gender: gender, Faction: titles[c.FactionID], Status: string(c.Status)})
	}
	window := "без окна"
	if g.PlayFrom != "" {
		window = g.PlayFrom + "–" + g.PlayTo
	}
	var limits []string
	if g.Settings.EndsAt != nil {
		limits = append(limits, "до "+g.Settings.EndsAt.Format("2006-01-02"))
	}
	if g.Settings.MaxRounds > 0 {
		limits = append(limits, fmt.Sprintf("%d раундов", g.Settings.MaxRounds))
	}
	if len(limits) == 0 {
		limits = append(limits, "до /finish")
	}
	current, last, next, err := s.roundLines(ctx, g)
	if err != nil {
		return "", err
	}
	switch g.Status {
	case model.GameSetup:
		next = "мир настраивается администратором; создание персонажей откроется после настройки"
	case model.GameDraft:
		next = "игра не запущена: набор игроков; старт — /begin в группе"
	case model.GameStarting:
		next = "кампания запускается: стартовая сцена и первый раунд скоро в группе"
	case model.GameFinishing:
		next = "игра завершается: последний раунд и финальная хроника скоро в группе"
	}
	type partyRow struct {
		Name, Location, Members string
		Count                   int
	}
	var parties []partyRow
	if infos, err := s.games.Parties(ctx, g); err == nil {
		locNames := map[model.LocationID]string{}
		if locs, err := s.games.Locations(ctx, g.ID); err == nil {
			for _, l := range locs {
				locNames[l.ID] = cellLabel(l) // имя и координаты клетки (пункт 27)
			}
		}
		for _, p := range infos {
			row := partyRow{Name: p.Party.Name, Count: len(p.Members)}
			if p.Party.LocationID != nil {
				row.Location = locNames[*p.Party.LocationID]
			}
			names := make([]string, 0, len(p.Members))
			for _, m := range p.Members {
				names = append(names, m.Nickname)
			}
			row.Members = strings.Join(names, ", ")
			parties = append(parties, row)
		}
	}
	return templates.Render("status", map[string]any{
		"Parties": parties,
		"Title":   g.Title, "Status": statusTitle(g.Status), "Genre": st.Genre.Title, "Format": string(g.Format), "Mode": string(g.Mode),
		"SeasonNo": st.Season.SeasonNo, "RulesVersion": g.RulesVersion, "RoundMinutes": g.RoundMinutes, "Timezone": g.Timezone,
		"PlayWindow": window, "Limits": strings.Join(limits, ", "), "MembersCount": len(st.Members),
		"CharactersCount": len(st.Characters), "Factions": factions, "Characters": chars, "JoinLink": s.joinLink(g),
		"MinAnswers": g.Settings.MinRoundAnswers, "CurrentRound": current, "LastRound": last, "NextRound": next,
	})
}

// gameSet — /set <параметр> <значение> для текущей игры; без аргументов у администратора игры в `setup` возвращает
// к диалогу настройки (ADR-0023), иначе список параметров.
func (s *Service) gameSet(ctx context.Context, req *request) error {
	key, value := splitArgs(req.in.Args)
	g, err := s.games.Current(ctx, req.user)
	if key == "" {
		if err == nil && g.Status == model.GameSetup {
			if ok, err := s.games.CanAdmin(ctx, g, req.user.ID); err != nil {
				return err
			} else if ok {
				return s.startGameSetup(ctx, req.user, g)
			}
		}
		return s.reply(ctx, req.in, "Формат: /set <параметр> <значение>\nПараметры:\n"+paramsHelp())
	}
	if err != nil {
		return err
	}
	if _, err := s.games.Set(ctx, g, req.user.ID, key, value); err != nil {
		return err
	}
	return s.reply(ctx, req.in, "Сохранено: "+key+" = "+value)
}

// paramsHelp — список параметров /set.
func paramsHelp() string {
	var b strings.Builder
	for _, p := range game.Params {
		mark := ""
		if p.Advanced {
			mark = "; только командой"
		}
		fmt.Fprintf(&b, "• %s — %s (%s%s)\n", p.Key, p.Title, p.Hint, mark)
	}
	return b.String()
}

// join — /join <код>.
func (s *Service) join(ctx context.Context, req *request) error {
	code, _ := splitArgs(req.in.Args)
	if code == "" {
		return s.reply(ctx, req.in, "Формат: /join <код приглашения>")
	}
	return s.joinGame(ctx, req, code)
}

// joinGame добавляет пользователя в игру и запускает создание персонажа, если его ещё нет. Пока мир в `setup`
// (ADR-0023), вход записывается, а персонаж не создаётся: бот напишет участнику после настройки.
func (s *Service) joinGame(ctx context.Context, req *request, code string) error {
	g, err := s.games.Join(ctx, req.user, code)
	if err != nil {
		return err
	}
	if _, err := s.games.Character(ctx, g, req.user.ID); err == nil {
		return s.reply(ctx, req.in, fmt.Sprintf("Вы уже в игре «%s». Персонаж: /character", g.Title))
	} else if !errors.Is(err, game.ErrNoCharacter) {
		return err
	}
	if g.Status == model.GameSetup {
		return s.reply(ctx, req.in, fmt.Sprintf("Вы вошли в игру «%s». Администратор ещё настраивает мир: бот напишет сюда, когда можно будет создать персонажа.", g.Title))
	}
	if err := s.reply(ctx, req.in, fmt.Sprintf("Вы вошли в игру «%s». Создадим персонажа.", g.Title)); err != nil {
		return err
	}
	return s.startCharacterCreate(ctx, req.user, g)
}

// gamesList — /games [номер]: список игр пользователя и выбор текущей.
func (s *Service) gamesList(ctx context.Context, req *request) error {
	games, err := s.games.Of(ctx, req.user.ID)
	if err != nil {
		return err
	}
	if len(games) == 0 {
		return s.reply(ctx, req.in, "Вы пока не участвуете в играх. Вход по ссылке администратора или /join <код>.")
	}
	if arg, _ := splitArgs(req.in.Args); arg != "" {
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 || n > len(games) {
			return s.reply(ctx, req.in, fmt.Sprintf("Укажите номер игры от 1 до %d.", len(games)))
		}
		if err := s.games.Select(ctx, req.user, games[n-1]); err != nil {
			return err
		}
		return s.reply(ctx, req.in, fmt.Sprintf("Текущая игра: «%s».", games[n-1].Title))
	}
	current, _ := s.games.Current(ctx, req.user)
	var b strings.Builder
	b.WriteString("Ваши игры:\n")
	var kb model.Keyboard
	for i, g := range games {
		mark := ""
		if g.ID == current.ID {
			mark = " — текущая"
		}
		fmt.Fprintf(&b, "%d. «%s» (%s)%s\n", i+1, g.Title, statusTitle(g.Status), mark)
		kb = append(kb, []model.Button{{Text: fmt.Sprintf("%d. %s", i+1, g.Title), Data: model.CallbackCommand("games", strconv.Itoa(i+1))}})
	}
	if len(games) > 1 {
		b.WriteString("Выбрать: кнопкой или /games <номер>")
		return s.replyKeyboard(ctx, req.in, b.String(), kb) // кнопки выбора только при нескольких играх (ADR-0015)
	}
	return s.reply(ctx, req.in, b.String())
}
