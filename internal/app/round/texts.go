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

// header — подпись мира и раунда в каждом игровом сообщении.
func header(g model.Game, roundNo int) string {
	return fmt.Sprintf("«%s» — раунд %d", g.Title, roundNo)
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
	return fmt.Sprintf("«%s» — начало истории\n%s", g.Title, text)
}

func roundOpenText(g model.Game, r model.Round, carried int, now time.Time) string {
	text := fmt.Sprintf("%s открыт. Заявки до %s: /act <текст> в личке с ботом, одна на раунд.", header(g, r.RoundNo), FormatTime(g, r.DeadlineAt, now))
	if carried > 0 {
		text += fmt.Sprintf(" Перенесено заявок из пропущенного раунда: %d.", carried)
	}
	return text
}

func roundSkippedText(g model.Game, r model.Round) string {
	return fmt.Sprintf("%s. %s.", header(g, r.RoundNo), SkippedText)
}

// roundFailedText — сообщение о раунде, который не удалось разрешить (ADR-0013); заявки переходят в следующий.
func roundFailedText(g model.Game, r model.Round) string {
	return fmt.Sprintf("%s не удалось разрешить: сбой на стороне бота. Заявки перенесены в следующий раунд.", header(g, r.RoundNo))
}

// finishedText — финальная хроника шаблоном: причина, число раундов, итог каждого персонажа.
func finishedText(g model.Game, rounds int, reason string, chars []model.Character, locations []model.Location, def model.CharacterModelDef) string {
	names := map[model.LocationID]string{}
	for _, l := range locations {
		names[l.ID] = l.Name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "«%s» — история завершена\n%s. Раундов сыграно: %d.", g.Title, upperFirst(reason), rounds)
	if len(chars) > 0 {
		b.WriteString("\nПерсонажи:")
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
		fmt.Fprintf(&b, "\n• %s — %s, HP %d/%d, %s", c.Nickname, narrator.StatusWords(c.Status), c.HP, maxHP, where)
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

func pausedText(g model.Game) string {
	return fmt.Sprintf("«%s» — игра на паузе. Раунды не открываются; возобновить: /pause.", g.Title)
}

func resumedText(g model.Game, r *model.Round, next *time.Time, now time.Time) string {
	if r != nil {
		return fmt.Sprintf("%s продолжается. Заявки до %s.", header(g, r.RoundNo), FormatTime(g, r.DeadlineAt, now))
	}
	if next != nil {
		return fmt.Sprintf("«%s» — игра возобновлена. Следующий раунд в %s.", g.Title, FormatTime(g, *next, now))
	}
	return fmt.Sprintf("«%s» — игра возобновлена.", g.Title)
}

func firstRoundLaterText(g model.Game, next time.Time, now time.Time) string {
	return fmt.Sprintf("«%s» — раунды открываются в игровое окно %s–%s. Первый раунд в %s.", g.Title, g.PlayFrom, g.PlayTo, FormatTime(g, next, now))
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
	return fmt.Sprintf("%s — итоги\n%s", header(g, r.RoundNo), strings.Join(lines, "\n"))
}

// noticeText — уведомление о заявке игроку: статус, исход, проверка (числа по режиму), приватные события персонажа.
func noticeText(g model.Game, r model.Round, res model.ActionResult, own []model.Event, preset rules.ModePreset) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — ваш итог\nЗаявка «%s»: ", header(g, r.RoundNo), truncateText(res.Intent.RawText, 80))
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
	b.WriteString("\nСледующая заявка: /act <текст> после открытия раунда.")
	return b.String()
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
