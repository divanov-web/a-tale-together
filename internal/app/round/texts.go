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

// openingTemplate — запасная стартовая сцена: шаблон жанра с подставленными названием мира и именами фракций.
func openingTemplate(g model.Game, template string, factions []model.Faction) string {
	r := strings.NewReplacer("{{world_title}}", g.Title)
	text := r.Replace(template)
	for _, f := range factions {
		text = strings.ReplaceAll(text, "{{faction_"+string(f.Code)+"}}", f.Title)
	}
	if strings.TrimSpace(text) == "" {
		text = "История начинается."
	}
	return text
}

// openingScene — сообщение стартовой сцены в группу: заголовок с названием мира и текст завязки.
func openingScene(g model.Game, text string) string {
	return fmt.Sprintf("«%s» — начало истории\n\n%s", g.Title, text)
}

// roundOpenText — анонс открытия раунда в группу; ключи строк жирным (B40, ADR-0030).
func roundOpenText(g model.Game, r model.Round, carried int, now time.Time) model.FormattedText {
	var b model.TextBuilder
	b.Printf("%s открыт\n", header(g, r.RoundNo))
	b.Key("Заявки до " + FormatTime(g, r.DeadlineAt, now))
	b.WriteString("/act <текст> здесь или в личке с ботом. ")
	b.Key("Соседние места")
	b.WriteString("/scout")
	if carried > 0 {
		b.WriteString("\n")
		b.Key("Перенесено заявок из пропущенного раунда")
		b.Printf("%d.", carried)
	}
	return b.Build()
}

func roundSkippedText(g model.Game, r model.Round) string {
	return fmt.Sprintf("%s\n%s.", header(g, r.RoundNo), SkippedText)
}

// roundFailedText — сообщение о раунде, который не удалось разрешить (ADR-0013); заявки переходят в следующий.
func roundFailedText(g model.Game, r model.Round) string {
	return fmt.Sprintf("%s не удалось разрешить\nСбой на стороне бота. Заявки перенесены в следующий раунд.", header(g, r.RoundNo))
}

// finishedText — финальная хроника шаблоном (G13: эпилог рассказчика отложен): причина, число раундов и предел,
// если он задан (B48: причина число не повторяет), итог каждого персонажа.
func finishedText(g model.Game, rounds int, reason string, chars []model.Character, locations []model.Location, def model.CharacterModelDef) string {
	names := map[model.LocationID]string{}
	for _, l := range locations {
		names[l.ID] = l.Name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "«%s» — история завершена\n\n%s. Раундов сыграно: %d", g.Title, upperFirst(reason), rounds)
	if g.Settings.MaxRounds > 0 {
		fmt.Fprintf(&b, " из %d", g.Settings.MaxRounds)
	}
	b.WriteString(".")
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

// rewardFact — факт награды из события `quest_stage_done`.
func rewardFact(ev model.Event) string {
	var p struct {
		Reward model.StageReward `json:"reward"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	return p.Reward.Fact
}

// noticeText — уведомление о заявке игроку (с названием мира: у игрока может быть несколько игр): статус, исход,
// проверка (числа по режиму), приватные события персонажа, варианты следующего раунда по его локации (B15);
// ключи строк жирным (B40, ADR-0030).
func noticeText(g model.Game, r model.Round, res model.ActionResult, own []model.Event, preset rules.ModePreset, options model.FormattedText) model.FormattedText {
	var b model.TextBuilder
	b.Printf("«%s» — раунд %d — ваш итог\n\n", g.Title, r.RoundNo)
	b.Key(fmt.Sprintf("Заявка «%s»", truncateText(res.Intent.RawText, 80)))
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
		b.WriteString("\n")
		if preset.ShowNumbers {
			b.Key("Проверка")
			b.Printf("параметр %d, сложность %d, шанс %d%%, бросок %d", res.Check.Param, res.Check.Difficulty, res.Check.ChancePct, res.Check.Roll)
			if res.Check.Pushed {
				b.Printf(", повторный бросок %d", res.Check.PushRoll)
			}
		} else if g.Mode == model.ModeNarrative {
			b.Key("Шанс был")
			b.WriteString(rules.ChanceWords(res.Check.ChancePct))
		} else {
			b.Printf("Шанс был %d%%", res.Check.ChancePct)
			if res.Check.Pushed {
				b.WriteString(", пришлось напрячься")
			}
		}
	}
	for _, ev := range own {
		if ev.Visibility != model.VisibilityPrivate {
			continue
		}
		switch ev.Type {
		case model.EventFactLearned:
			b.WriteString("\n• " + payloadStr(ev, "fact"))
		case model.EventConditionAdded:
			b.WriteString("\n• ")
			b.Key("состояние")
			b.WriteString(narrator.ConditionWords(payloadStr(ev, "condition")))
		case model.EventHPChanged:
			b.WriteString("\n• ")
			b.Key("HP")
			b.WriteString(payloadStr(ev, "to"))
		case model.EventQuestIssued:
			b.WriteString("\n• ")
			b.Key("новое дело фракции")
			b.Printf("«%s» — %s", payloadStr(ev, "quest"), payloadStr(ev, "summary"))
		case model.EventQuestStagePlaced:
			b.WriteString("\n• ")
			b.Key("«" + payloadStr(ev, "quest") + "»")
			b.Printf("этап «%s» — здесь, в «%s»", payloadStr(ev, "stage"), payloadStr(ev, "cell"))
		case model.EventQuestStageDone:
			b.WriteString("\n• ")
			b.Key("«" + payloadStr(ev, "quest") + "»")
			b.Printf("этап «%s» выполнен", payloadStr(ev, "stage"))
			if reward := rewardFact(ev); reward != "" {
				b.WriteString(" — " + reward)
			}
		case model.EventQuestDone:
			b.Printf("\n• «%s» завершён", payloadStr(ev, "quest"))
		case model.EventGoalRevised:
			b.WriteString("\n• ")
			b.Key("цель фракции теперь")
			b.WriteString(payloadStr(ev, "text"))
		}
	}
	if options.Text != "" {
		b.WriteString("\n\n")
		b.Append(options)
	}
	b.WriteString("\n\n")
	b.Key("Следующая заявка")
	b.WriteString("/act <текст> после открытия раунда. " + scoutHint)
	return b.Build()
}

// scoutHint — подсказка к личным сообщениям раунда: переходов среди вариантов нет (B36, ADR-0025), куда идти — `/scout`.
const scoutHint = "Куда можно пойти — /scout."

// roundOpenPersonalText — личное «раунд открыт» с вариантами (B15) персонажам, не получившим уведомление о заявке;
// ключи жирным (B40).
func roundOpenPersonalText(g model.Game, r model.Round, options model.FormattedText, now time.Time) model.FormattedText {
	var b model.TextBuilder
	b.Printf("«%s» — раунд %d открыт\n\n", g.Title, r.RoundNo)
	b.Key("Заявки до " + FormatTime(g, r.DeadlineAt, now))
	b.WriteString("напишите, что делает персонаж, или /act <текст>. Одна заявка на раунд. " + scoutHint)
	if options.Text != "" {
		b.WriteString("\n\n")
		b.Append(options)
	}
	return b.Build()
}

// optionsFor — блок вариантов персонажа по его локации (B15): подход, параметр персонажа, итоговая сложность и шанс
// в процентах во всех режимах игры (B19: слова остаются публичной хронике). Заголовок title дополняется названием
// локации; заголовок и подходы — ключи строк, жирным (B40). Пусто без локации или подходов.
func optionsFor(c model.SnapshotCharacter, state *model.RoundSnapshot, opts []narrator.Option, title string) model.FormattedText {
	if c.LocationID == nil {
		return model.FormattedText{}
	}
	var lines model.TextBuilder
	for _, o := range opts {
		if o.LocationID != *c.LocationID {
			continue
		}
		lines.WriteString("\n• ")
		if o.Attribute == "" {
			lines.WriteString(o.Title + " (без броска)")
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
		lines.Key(fmt.Sprintf("%s (%s)", o.Title, stat))
		lines.Printf("параметр %d, сложность %d, шанс %d%%", ch.Param, o.Difficulty, ch.Pct)
	}
	if lines.Len() == 0 {
		return model.FormattedText{}
	}
	where := ""
	if l, ok := state.Location(*c.LocationID); ok {
		where = " (" + l.Name + ")"
	}
	var b model.TextBuilder
	b.Header(title + where)
	b.Append(lines.Build())
	return b.Build()
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
