package round

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// partLimit — символов в одной части игрового сообщения: лимит Telegram с запасом на подпись продолжения.
const partLimit = model.TelegramTextMaxLen - 96

// chronicleMessage — текст хроники в группу: подпись мира и раунда, общий текст и блоки по сценам от рассказчика
// (или шаблонные строки событий), к каждой сцене — подходы следующего раунда со сложностью и шансами, посчитанные кодом.
func chronicleMessage(g model.Game, r model.Round, events []model.Event, state *model.RoundSnapshot, opts []narrator.Option) string {
	byLocation := map[model.LocationID][]narrator.Option{}
	for _, o := range opts {
		byLocation[o.LocationID] = append(byLocation[o.LocationID], o)
	}
	var b strings.Builder
	if n := r.Narration; n != nil {
		b.WriteString(header(g, r.RoundNo))
		if n.Chronicle != "" {
			b.WriteString("\n" + n.Chronicle)
		}
		for _, sc := range n.Scenes {
			label := sc.Party
			if l, ok := state.Location(sc.LocationID); ok && label == "" {
				label = l.Name
			}
			fmt.Fprintf(&b, "\n\n— %s —", label)
			if sc.Text != "" {
				b.WriteString("\n" + sc.Text)
			}
			if line := narrator.OptionsText(byLocation[sc.LocationID], g.Mode); line != "" {
				b.WriteString("\n" + line)
			}
		}
		return b.String()
	}
	b.WriteString(chronicleText(g, r, events, state))
	for _, l := range state.Locations {
		line := narrator.OptionsText(byLocation[l.ID], g.Mode)
		if line == "" {
			continue
		}
		var present []string
		for _, c := range state.Characters {
			if c.LocationID != nil && *c.LocationID == l.ID && c.Status != model.CharacterDead {
				present = append(present, c.Nickname)
			}
		}
		fmt.Fprintf(&b, "\n\n— %s (%s) —\n%s", l.Name, strings.Join(present, ", "), line)
	}
	return b.String()
}

// splitParts делит текст на части не длиннее limit символов по строкам (слишком длинная строка режется по символам);
// каждая часть после первой начинается с prefix. Короткий текст — одна часть.
func splitParts(text, prefix string, limit int) []string {
	if utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}
	room := limit - utf8.RuneCountInString(prefix)
	if room < 1 {
		room = limit
	}
	var parts []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, string(cur))
			cur = cur[:0]
		}
	}
	for _, line := range strings.Split(text, "\n") {
		lr := []rune(line)
		for len(lr) > room {
			flush()
			parts = append(parts, string(lr[:room]))
			lr = lr[room:]
		}
		if len(cur) > 0 && len(cur)+1+len(lr) > room {
			flush()
		}
		if len(cur) > 0 {
			cur = append(cur, '\n')
		}
		cur = append(cur, lr...)
	}
	flush()
	for i := 1; i < len(parts); i++ {
		parts[i] = prefix + parts[i]
	}
	return parts
}
