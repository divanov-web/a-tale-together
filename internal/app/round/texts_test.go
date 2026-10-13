package round

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
	"taletogether/internal/service/rules"
)

// TestTexts_Templates — эталоны сообщений игрока (B16): заголовок отдельной строкой, пустая строка между частями.
func TestTexts_Templates(t *testing.T) {
	t.Parallel()
	g := model.Game{Title: "Мир", Timezone: "UTC", PlayFrom: "10:00", PlayTo: "22:00"}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return time.Date(2026, 10, 10, h, m, 0, 0, time.UTC) }
	r1 := model.Round{RoundNo: 1, DeadlineAt: at(12, 10)}
	r2 := model.Round{RoundNo: 2, DeadlineAt: at(12, 20)}
	factions := []model.Faction{{Code: model.FactionGood, Title: "Орден"}, {Code: model.FactionEvil, Title: "Гильдия"}}
	cases := []struct{ name, got, want string }{
		{"opening scene", openingScene(g, openingTemplate(g, "В {{world_title}} наступает осень. {{faction_good}} против {{faction_evil}}.", factions)),
			"«Мир» — начало истории\n\nВ Мир наступает осень. Орден против Гильдия."},
		{"opening scene empty", openingScene(g, openingTemplate(g, "  ", nil)), "«Мир» — начало истории\n\nИстория начинается."},
		{"round open", roundOpenText(g, r1, 0, now), "Раунд 1 открыт\nЗаявки до 12:10: /act <текст> здесь или в личке с ботом. Соседние места: /scout"},
		{"round open carried", roundOpenText(g, r2, 1, now), "Раунд 2 открыт\nЗаявки до 12:20: /act <текст> здесь или в личке с ботом. Соседние места: /scout\nПеренесено заявок из пропущенного раунда: 1."},
		{"round skipped", roundSkippedText(g, r1), "Раунд 1\nНедостаточное количество участников. Раунд пропущен."},
		{"round failed", roundFailedText(g, r1), "Раунд 1 не удалось разрешить\nСбой на стороне бота. Заявки перенесены в следующий раунд."},
		{"paused", pausedText(), "Игра на паузе\nРаунды не открываются. Возобновить: /pause."},
		{"auto paused", autoPausedText(3), "Игра на паузе\n3 раунда подряд без кворума. Возобновить: /pause."},
		{"resumed open round", resumedText(g, &model.Round{RoundNo: 3, DeadlineAt: at(13, 31)}, nil, now), "Раунд 3 продолжается\nЗаявки до 13:31."},
		{"resumed next", resumedText(g, nil, ptr(at(13, 0)), now), "Игра возобновлена\nСледующий раунд в 13:00."},
		{"resumed", resumedText(g, nil, nil, now), "Игра возобновлена"},
		{"first round later", firstRoundLaterText(g, at(10, 0).AddDate(0, 0, 1), now), "Игра запущена\nРаунды открываются в игровое окно 10:00–22:00. Первый раунд в 11.10 10:00."},
		{"finished no characters", finishedText(g, 2, "игру завершил администратор", nil, nil, model.CharacterModelDef{}), "«Мир» — история завершена\n\nИгру завершил администратор. Раундов сыграно: 2."},
		{"chronicle quiet", chronicleText(g, r1, nil, &model.RoundSnapshot{}), "Раунд 1 — итоги\n\n• тихий раунд: никто ничего заметного не сделал"},
		{"personal open", roundOpenPersonalText(g, r1, "", now), "«Мир» — раунд 1 открыт\n\nЗаявки до 12:10: напишите, что делает персонаж, или /act <текст>. Одна заявка на раунд. Куда можно пойти — /scout."},
		{"personal open options", roundOpenPersonalText(g, r1, "Варианты для вас (Площадь):\n• x", now), "«Мир» — раунд 1 открыт\n\nЗаявки до 12:10: напишите, что делает персонаж, или /act <текст>. Одна заявка на раунд. Куда можно пойти — /scout.\n\nВарианты для вас (Площадь):\n• x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got\n%q\nwant\n%q", tc.got, tc.want)
			}
		})
	}
	for n, want := range map[int]string{1: "1 раунд", 2: "2 раунда", 5: "5 раундов", 11: "11 раундов", 12: "12 раундов", 21: "21 раунд", 24: "24 раунда"} {
		if got := roundsWord(n); got != want {
			t.Errorf("roundsWord(%d) = %q; want %q", n, got, want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// TestTexts_Notice — эталон личного итога: заявка, шанс, приватный факт, варианты следующего раунда, подсказка.
func TestTexts_Notice(t *testing.T) {
	t.Parallel()
	g := model.Game{Title: "Мир", Mode: model.ModeBalanced}
	res := model.ActionResult{Intent: model.Intent{RawText: "осмотреть доску"}, Status: model.ResultResolved, Summary: "осматривает: доска объявлений — успех", Check: &model.CheckInfo{Param: 6, Difficulty: 5, ChancePct: 60, Roll: 37}}
	own := []model.Event{{Type: model.EventFactLearned, Visibility: model.VisibilityPrivate, Payload: json.RawMessage(`{"fact":"Гонца видели у башни."}`)}}
	options := "Варианты на раунд 2 (Рыночная площадь):\n• разобрать приписку (Разум/внимательность): параметр 6, сложность 4, шанс 70%"
	want := "«Мир» — раунд 1 — ваш итог\n\nЗаявка «осмотреть доску»: осматривает: доска объявлений — успех\nШанс был 60%\n• Гонца видели у башни.\n\n" + options + "\n\nСледующая заявка: /act <текст> после открытия раунда. Куда можно пойти — /scout."
	if got := noticeText(g, model.Round{RoundNo: 1}, res, own, rules.Preset(g.Mode), options); got != want {
		t.Errorf("notice =\n%q\nwant\n%q", got, want)
	}
	rejected := model.ActionResult{Intent: model.Intent{RawText: "крадусь"}, Status: model.ResultRejected, Reason: "действие появится позже"}
	want = "«Мир» — раунд 1 — ваш итог\n\nЗаявка «крадусь»: отклонена — действие появится позже\n\nСледующая заявка: /act <текст> после открытия раунда. Куда можно пойти — /scout."
	if got := noticeText(g, model.Round{RoundNo: 1}, rejected, nil, rules.Preset(g.Mode), ""); got != want {
		t.Errorf("rejected notice =\n%q\nwant\n%q", got, want)
	}
	g.Mode = model.ModeMechanics
	if got := noticeText(g, model.Round{RoundNo: 1}, res, nil, rules.Preset(g.Mode), ""); got != "«Мир» — раунд 1 — ваш итог\n\nЗаявка «осмотреть доску»: осматривает: доска объявлений — успех\nПроверка: параметр 6, сложность 5, шанс 60%, бросок 37\n\nСледующая заявка: /act <текст> после открытия раунда. Куда можно пойти — /scout." {
		t.Errorf("mechanics notice = %q", got)
	}
}

// TestTexts_OptionsFor — личные варианты по локации персонажа (B15): параметр, сложность и шанс в процентах в любом
// режиме (B19); пусто без локации или подходов.
func TestTexts_OptionsFor(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	chars, _ := w.store.ListCharacters(ctx, w.game.ID, w.chars[0].SeasonID)
	locations, _ := w.store.ListLocations(ctx, w.game.ID)
	def, err := w.svc.characterDef(ctx, w.game)
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot(w.game, chars, locations, nil, nil, nil, def)
	opts := narrator.Options(&state, def, model.ModeBalanced)
	alice := state.Characters[0]
	want := "Варианты для вас (Рыночная площадь):\n• разобрать приписку (Разум/внимательность): параметр 6, сложность 4, шанс 70%"
	if got := optionsFor(alice, &state, opts, "Варианты для вас"); got != want {
		t.Errorf("balanced =\n%q\nwant\n%q", got, want)
	}
	// B19: в «повествовании» личный шанс тоже в процентах, сложность берётся из вариантов этого режима.
	if got := optionsFor(alice, &state, narrator.Options(&state, def, model.ModeNarrative), "Варианты на раунд 2"); got != "Варианты на раунд 2 (Рыночная площадь):\n• разобрать приписку (Разум/внимательность): параметр 6, сложность 4, шанс 70%" {
		t.Errorf("narrative = %q", got)
	}
	// Подвал: сложность локации 1, у плута без атлетики и воровства шансы 20 % и 30 %; снимок локальный (пункт 27):
	// персонаж переносится в подвал до сборки, иначе подвала и его соседей в снимке нет.
	var cellar model.LocationID
	for _, l := range locations {
		if l.Name == "Подвал башни" {
			cellar = l.ID
		}
	}
	chars[0].LocationID = &cellar
	state = snapshot(w.game, chars, locations, nil, nil, nil, def)
	opts = narrator.Options(&state, def, model.ModeBalanced)
	if got := optionsFor(state.Characters[0], &state, opts, "Варианты для вас"); got != "Варианты для вас (Подвал башни):\n• выбить (Сила/атлетика): параметр 2, сложность 5, шанс 20%\n• отпереть (Ловкость/воровство): параметр 3, сложность 5, шанс 30%" {
		t.Errorf("cellar = %q", got)
	}
	state.Characters[0].LocationID = nil
	if got := optionsFor(state.Characters[0], &state, opts, "Варианты для вас"); got != "" {
		t.Errorf("no location = %q", got)
	}
	// Клетка без объектов: вариантов нет — переходы среди них не предлагаются (B36), соседей показывает /scout.
	var hall model.LocationID
	for _, l := range locations {
		if l.Name == "Зал Серой башни" {
			hall = l.ID
		}
	}
	state.Characters[0].LocationID = &hall
	if got := optionsFor(state.Characters[0], &state, narrator.Options(&state, def, model.ModeBalanced), "Варианты для вас"); got != "" {
		t.Errorf("no approaches = %q", got)
	}
}

// TestOptionRef_RoundTrip — ссылка варианта для кнопки (ADR-0015): строка и разбор, обычный текст заявки не ссылка.
func TestOptionRef_RoundTrip(t *testing.T) {
	t.Parallel()
	ref := OptionRef{RoundNo: 12, LocationID: 3, Object: "cellar_hatch", Idx: 1}
	if got := ref.String(); got != "#12:3:cellar_hatch:1" {
		t.Fatalf("String = %q", got)
	}
	back, ok := ParseOptionRef(" " + ref.String())
	if !ok || back != ref {
		t.Fatalf("ParseOptionRef = %+v, %v", back, ok)
	}
	for _, bad := range []string{"иду в лес", "#12:3:well", "#x:3:well:0", "#12:3::0", "#12:3:well:z"} {
		if _, ok := ParseOptionRef(bad); ok {
			t.Fatalf("ParseOptionRef(%q) accepted", bad)
		}
	}
	loc := model.LocationID(3)
	c := model.SnapshotCharacter{Nickname: "Элис", LocationID: &loc}
	opts := []narrator.Option{
		{LocationID: 3, Object: "well", Idx: 0, Title: "спуститься в колодец"},
		{LocationID: 4, Object: "door", Idx: 0, Title: "выбить дверь"},
		{LocationID: 3, Object: "well", Idx: 1, Title: "осмотреть ворот"},
	}
	kb := optionsKeyboard(c, opts, 12)
	if len(kb) != 2 || kb[0][0].Text != "спуститься в колодец" || kb[0][0].Data != "/act #12:3:well:0" || kb[1][0].Data != "/act #12:3:well:1" {
		t.Fatalf("optionsKeyboard = %+v", kb)
	}
	if optionsKeyboard(model.SnapshotCharacter{}, opts, 12) != nil {
		t.Fatal("keyboard without location")
	}
}
