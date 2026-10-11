package round

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// partLimit — символов в одной части игрового сообщения: лимит Telegram с запасом на подпись продолжения.
const partLimit = model.TelegramTextMaxLen - 96

// chronicleMessage — текст хроники в группу: подпись раунда, общий текст и блоки по сценам от рассказчика
// (или шаблонные строки событий), к каждой сцене — подходы следующего раунда со сложностью, посчитанные кодом;
// заголовок, общий текст, сцены и «Варианты» разделены пустой строкой (B16).
func chronicleMessage(g model.Game, r model.Round, events []model.Event, state *model.RoundSnapshot, opts []narrator.Option) string {
	byLocation := map[model.LocationID][]narrator.Option{}
	for _, o := range opts {
		byLocation[o.LocationID] = append(byLocation[o.LocationID], o)
	}
	var b strings.Builder
	if n := r.Narration; n != nil {
		b.WriteString(header(g, r.RoundNo))
		if n.Chronicle != "" {
			b.WriteString("\n\n" + n.Chronicle)
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
				b.WriteString("\n\n" + line)
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
		// Блок на группу в локации, затем персонажи без группы (пункт 19), как в сценах рассказчика.
		for _, pt := range state.Parties {
			var present []string
			for _, c := range state.Characters {
				if c.LocationID != nil && *c.LocationID == l.ID && c.Status != model.CharacterDead && c.PartyID != nil && *c.PartyID == pt.ID {
					present = append(present, c.Nickname)
				}
			}
			if len(present) > 0 {
				fmt.Fprintf(&b, "\n\n— %s (%s): %s —\n\n%s", pt.Name, l.Name, strings.Join(present, ", "), line)
			}
		}
		var present []string
		for _, c := range state.Characters {
			if c.LocationID != nil && *c.LocationID == l.ID && c.Status != model.CharacterDead && (c.PartyID == nil || !hasParty(state, *c.PartyID)) {
				present = append(present, c.Nickname)
			}
		}
		if len(present) > 0 {
			fmt.Fprintf(&b, "\n\n— %s (%s) —\n\n%s", l.Name, strings.Join(present, ", "), line)
		}
	}
	return b.String()
}

func hasParty(state *model.RoundSnapshot, id model.PartyID) bool {
	_, ok := state.Party(id)
	return ok
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

// tagMentions дописывает `(@username)` после первого упоминания никнейма в тексте хроники (B2): игрок получает
// уведомление Telegram, остальные видят, чей это персонаж. Совпадение только по целому слову; заголовки сцен
// «— … —» и уже отмеченные ники пропускаются.
func tagMentions(text string, mentions map[string]string) string {
	if len(mentions) == 0 {
		return text
	}
	nicks := make([]string, 0, len(mentions))
	for n := range mentions {
		nicks = append(nicks, n)
	}
	sort.Slice(nicks, func(i, j int) bool { return len(nicks[i]) > len(nicks[j]) }) // длинные ники раньше: «Кот» внутри «Котофей»
	lines := strings.Split(text, "\n")
	for _, nick := range nicks {
		tag := " (" + mentions[nick] + ")"
		for i, line := range lines {
			if strings.HasPrefix(line, "— ") && strings.HasSuffix(line, " —") {
				continue
			}
			if at := wordIndex(line, nick); at >= 0 {
				lines[i] = line[:at+len(nick)] + tag + line[at+len(nick):]
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

// wordIndex — байтовая позиция первого вхождения word в s как целого слова, иначе −1.
func wordIndex(s, word string) int {
	from := 0
	for {
		i := strings.Index(s[from:], word)
		if i < 0 {
			return -1
		}
		at := from + i
		end := at + len(word)
		before, _ := utf8.DecodeLastRuneInString(s[:at])
		after, _ := utf8.DecodeRuneInString(s[end:])
		if !isWordRune(before) && !isWordRune(after) {
			return at
		}
		from = end
	}
}

func isWordRune(r rune) bool {
	return r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
}
