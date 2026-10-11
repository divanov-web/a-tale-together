package round

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
	"taletogether/internal/service/rules"
)

// Формат сообщений игроку (B16): заголовок отдельной строкой, пустая строка между заголовком, телом, списками и
// подсказкой; списки через «•» с новой строки. Эталоны каждого текста — в texts_test.go.

// SkippedText — сообщение о пропуске раунда без кворума; на время разработки отправляется в группу
// (решение руководителя 2026-10-10), в prod раунд пропускается молча.
const SkippedText = "Недостаточное количество участников. Раунд пропущен"

// FormatTime — время в часовом поясе игры: «15:04» в тот же день, иначе «02.01 15:04».
func FormatTime(g model.Game, t, now time.Time) string {
	loc := location(g.Timezone)
	lt, ln := t.In(loc), now.In(loc)
	if lt.YearDay() == ln.YearDay() && lt.Year() == ln.Year() {
		return lt.Format("15:04")
	}
	return lt.Format("02.01 15:04")
}

// header — подпись раунда в сообщениях группы; название мира в чате одной игры лишнее (B9),
// оно остаётся в стартовой сцене, финальной хронике, /status и личных уведомлениях.
func header(_ model.Game, roundNo int) string {
	return fmt.Sprintf("Раунд %d", roundNo)
}

// openingScene подставляет название мира и имена фракций в стартовую сцену шаблона жанра.
func openingScene(g model.Game, template string, factions []model.Faction) string {
	r := strings.NewReplacer("{{world_title}}", g.Title)
	text := r.Replace(template)
	for _, f := range factions {
		text = strings.ReplaceAll(text, "{{faction_"+string(f.Code)+"}}", f.Title)
	}
	if strings.TrimSpace(text) == "" {
		text = "История начинается."
	}
	return fmt.Sprintf("«%s» — начало истории\n\n%s", g.Title, text)
}

func roundOpenText(g model.Game, r model.Round, carried int, now time.Time) string {
	text := fmt.Sprintf("%s открыт\nЗаявки до %s: /act <текст> в личке с ботом, одна на раунд.", header(g, r.RoundNo), FormatTime(g, r.DeadlineAt, now))
	if carried > 0 {
		text += fmt.Sprintf("\nПеренесено заявок из пропущенного раунда: %d.", carried)
	}
	return text
}

func roundSkippedText(g model.Game, r model.Round) string {
	return fmt.Sprintf("%s\n%s.", header(g, r.RoundNo), SkippedText)
}

// roundFailedText — сообщение о раунде, который не удалось разрешить (ADR-0013); заявки переходят в следующий.
func roundFailedText(g model.Game, r model.Round) string {
	return fmt.Sprintf("%s не удалось разрешить\nСбой на стороне бота. Заявки перенесены в следующий раунд.", header(g, r.RoundNo))
}

// finishedText — финальная хроника шаблоном: причина, число раундов, итог каждого персонажа.
func finishedText(g model.Game, rounds int, reason string, chars []model.Character, locations []model.Location, def model.CharacterModelDef) string {
	names := map[model.LocationID]string{}
	for _, l := range locations {
		names[l.ID] = l.Name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "«%s» — история завершена\n\n%s. Раундов сыграно: %d.", g.Title, upperFirst(reason), rounds)
	if len(chars) > 0 {
		b.WriteString("\n\nПерсонажи:")
	}
	for _, c := range chars {
		maxHP, err := rules.HP(def, c.Attributes)
		if err != nil || maxHP <= 0 {
			maxHP = max(c.HP, 1)
		}
		where := "неизвестно где"
		if c.LocationID != nil {
			if n, ok := names[*c.LocationID]; ok {
				where = n
			}
		}
		status := ""
		if w := narrator.StatusWords(c.Status); w != "" {
			status = w + ", "
		}
		fmt.Fprintf(&b, "\n• %s — %sHP %d/%d, %s", c.Nickname, status, c.HP, maxHP, where)
	}
	return b.String()
}

func upperFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

func pausedText() string {
	return "Игра на паузе\nРаунды не открываются. Возобновить: /pause."
}

// autoPausedText — автоматическая пауза после пропущенных подряд раундов (B11).
func autoPausedText(skipped int) string {
	return fmt.Sprintf("Игра на паузе\n%s подряд без кворума. Возобновить: /pause.", upperFirst(roundsWord(skipped)))
}

// roundsWord — число с существительным «раунд» в нужной форме.
func roundsWord(n int) string {
	word := "раундов"
	switch {
	case n%10 == 1 && n%100 != 11:
		word = "раунд"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		word = "раунда"
	}
	return fmt.Sprintf("%d %s", n, word)
}

func resumedText(g model.Game, r *model.Round, next *time.Time, now time.Time) string {
	if r != nil {
		return fmt.Sprintf("%s продолжается\nЗаявки до %s.", header(g, r.RoundNo), FormatTime(g, r.DeadlineAt, now))
	}
	if next != nil {
		return fmt.Sprintf("Игра возобновлена\nСледующий раунд в %s.", FormatTime(g, *next, now))
	}
	return "Игра возобновлена"
}

func firstRoundLaterText(g model.Game, next time.Time, now time.Time) string {
	return fmt.Sprintf("Игра запущена\nРаунды открываются в игровое окно %s–%s. Первый раунд в %s.", g.PlayFrom, g.PlayTo, FormatTime(g, next, now))
}

// chronicleText — шаблонная хроника без модели (сбой или отсутствие рассказчика): публичные и локационные события словами.
func chronicleText(g model.Game, r model.Round, events []model.Event, state *model.RoundSnapshot) string {
	var lines []string
	for _, ev := range events {
		if ev.Type == model.EventCharacterMoved && ev.LocationID != nil && model.LocationID(payloadNum(ev, "from")) != *ev.LocationID {
			continue // второе событие перехода (для видимости в локации назначения) не дублируем
		}
		_, line := narrator.Describe(ev, state)
		lines = append(lines, "• "+line)
	}
	if len(lines) == 0 {
		lines = append(lines, "• тихий раунд: никто ничего заметного не сделал")
	}
	return fmt.Sprintf("%s — итоги\n\n%s", header(g, r.RoundNo), strings.Join(lines, "\n"))
}

// noticeText — уведомление о заявке игроку (с названием мира: у игрока может быть несколько игр): статус, исход,
// проверка (числа по режиму), приватные события персонажа, варианты следующего раунда по его локации (B15).
func noticeText(g model.Game, r model.Round, res model.ActionResult, own []model.Event, preset rules.ModePreset, options string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "«%s» — раунд %d — ваш итог\n\nЗаявка «%s»: ", g.Title, r.RoundNo, truncateText(res.Intent.RawText, 80))
	switch res.Status {
	case model.ResultResolved:
		b.WriteString(res.Summary)
	case model.ResultRejected:
		b.WriteString("отклонена — " + res.Reason)
	case model.ResultAmbiguous:
		b.WriteString("не понята — " + res.Reason)
	default:
		b.WriteString(string(res.Status) + " — " + res.Reason)
	}
	if res.Check != nil {
		if preset.ShowNumbers {
			fmt.Fprintf(&b, "\nПроверка: параметр %d, сложность %d, шанс %d%%, бросок %d", res.Check.Param, res.Check.Difficulty, res.Check.ChancePct, res.Check.Roll)
			if res.Check.Pushed {
				fmt.Fprintf(&b, ", повторный бросок %d", res.Check.PushRoll)
			}
		} else if g.Mode == model.ModeNarrative {
			fmt.Fprintf(&b, "\nШанс был: %s", rules.ChanceWords(res.Check.ChancePct))
		} else {
			fmt.Fprintf(&b, "\nШанс был %d%%", res.Check.ChancePct)
			if res.Check.Pushed {
				b.WriteString(", пришлось напрячься")
			}
		}
	}
	var facts []string
	for _, ev := range own {
		if ev.Visibility != model.VisibilityPrivate {
			continue
		}
		switch ev.Type {
		case model.EventFactLearned:
			facts = append(facts, "• "+payloadStr(ev, "fact"))
		case model.EventConditionAdded:
			facts = append(facts, "• состояние: "+narrator.ConditionWords(payloadStr(ev, "condition")))
		case model.EventHPChanged:
			facts = append(facts, fmt.Sprintf("• HP: %s", payloadStr(ev, "to")))
		}
	}
	if len(facts) > 0 {
		b.WriteString("\n" + strings.Join(facts, "\n"))
	}
	if options != "" {
		b.WriteString("\n\n" + options)
	}
	b.WriteString("\n\nСледующая заявка: /act <текст> после открытия раунда.")
	return b.String()
}

// roundOpenPersonalText — личное «раунд открыт» с вариантами (B15) персонажам, не получившим уведомление о заявке.
func roundOpenPersonalText(g model.Game, r model.Round, options string, now time.Time) string {
	text := fmt.Sprintf("«%s» — раунд %d открыт\n\nЗаявки до %s: напишите, что делает персонаж, или /act <текст>. Одна заявка на раунд.", g.Title, r.RoundNo, FormatTime(g, r.DeadlineAt, now))
	if options != "" {
		text += "\n\n" + options
	}
	return text
}

// optionsFor — блок вариантов персонажа по его локации (B15): подход, параметр персонажа, итоговая сложность и шанс
// в процентах; в «повествовании» шанс словами. Заголовок title дополняется названием локации. Пусто без локации или подходов.
func optionsFor(c model.SnapshotCharacter, state *model.RoundSnapshot, opts []narrator.Option, mode model.GameMode, title string) string {
	if c.LocationID == nil {
		return ""
	}
	var lines []string
	for _, o := range opts {
		if o.LocationID != *c.LocationID {
			continue
		}
		if o.Attribute == "" {
			lines = append(lines, "• "+o.Title+" (без броска)")
			continue
		}
		stat := o.Attribute
		if o.Skill != "" {
			stat += "/" + strings.ToLower(o.Skill)
		}
		var ch narrator.Chance
		for _, x := range o.Chances {
			if x.Nickname == c.Nickname {
				ch = x
			}
		}
		if mode == model.ModeNarrative {
			lines = append(lines, fmt.Sprintf("• %s (%s): %s", o.Title, stat, rules.ChanceWords(ch.Pct)))
		} else {
			lines = append(lines, fmt.Sprintf("• %s (%s): параметр %d, сложность %d, шанс %d%%", o.Title, stat, ch.Param, o.Difficulty, ch.Pct))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	where := ""
	if l, ok := state.Location(*c.LocationID); ok {
		where = " (" + l.Name + ")"
	}
	return fmt.Sprintf("%s%s:\n%s", title, where, strings.Join(lines, "\n"))
}

func payloadStr(ev model.Event, key string) string {
	var p map[string]any
	_ = json.Unmarshal(ev.Payload, &p)
	switch v := p[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func payloadNum(ev model.Event, key string) float64 {
	var p map[string]any
	_ = json.Unmarshal(ev.Payload, &p)
	v, _ := p[key].(float64)
	return v
}

func truncateText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
