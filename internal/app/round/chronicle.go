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
			fmt.Fprintf(&b, "\n\n— %s —", sceneLabel(sc, state))
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
		// Блок на группу в локации, затем персонажи без группы (пункт 19), как в сценах рассказчика; подпись — локация и
		// присутствующие, без названия группы.
		for _, pt := range state.Parties {
			var present []string
			for _, c := range state.Characters {
				if c.LocationID != nil && *c.LocationID == l.ID && c.Status != model.CharacterDead && c.PartyID != nil && *c.PartyID == pt.ID {
					present = append(present, c.Nickname)
				}
			}
			if len(present) > 0 {
				fmt.Fprintf(&b, "\n\n— %s (%s) —\n\n%s", l.Name, strings.Join(present, ", "), line)
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

// sceneLabel — подпись блока сцены для игроков: только название локации. Внутренняя подпись `NarrationScene.Party`
// («Группа (Локация)») хранит имя группы для сопоставления сцен, но игрокам названия групп не показываются
// (решение руководителя 2026-10-13: механика групп геймдизайном ещё не проработана).
func sceneLabel(sc model.NarrationScene, state *model.RoundSnapshot) string {
	if l, ok := state.Location(sc.LocationID); ok {
		return l.Name
	}
	if at := strings.LastIndex(sc.Party, " ("); at > 0 && strings.HasSuffix(sc.Party, ")") {
		return sc.Party[at+2 : len(sc.Party)-1]
	}
	return sc.Party
}

func hasParty(state *model.RoundSnapshot, id model.PartyID) bool {
	_, ok := state.Party(id)
	return ok
}

// splitParts делит текст на части не длиннее limit символов по строкам (слишком длинная строка режется по символам);
// части после первой получают подпись prefix; разметка каждой части вырезается из исходной (`FormattedText.Cut`,
// ADR-0030) и сдвигается на длину подписи.
func splitParts(text model.FormattedText, prefix string, limit int) []model.FormattedText {
	total := utf8.RuneCountInString(text.Text)
	if total <= limit {
		return []model.FormattedText{text}
	}
	room := limit - utf8.RuneCountInString(prefix)
	if room < 1 {
		room = limit
	}
	var ranges [][2]int // границы частей в рунах исходного текста
	start, cur := 0, 0  // начало текущей части и её длина
	flush := func(end int) {
		if cur > 0 {
			ranges = append(ranges, [2]int{start, end})
			cur = 0
		}
	}
	pos := 0
	for _, line := range strings.Split(text.Text, "\n") {
		n := utf8.RuneCountInString(line)
		for n > room {
			flush(pos - 1) // перевод строки перед этой строкой остаётся за границей части
			ranges = append(ranges, [2]int{pos, pos + room})
			pos += room
			n -= room
		}
		if cur > 0 && cur+1+n > room {
			flush(pos - 1)
		}
		if cur == 0 {
			start = pos
		} else {
			cur++
		}
		cur += n
		pos += n + 1
	}
	flush(total)
	parts := make([]model.FormattedText, 0, len(ranges))
	for i, r := range ranges {
		part := text.Cut(r[0], r[1])
		if i > 0 {
			var b model.TextBuilder
			b.WriteString(prefix)
			b.Append(part)
			part = b.Build()
		}
		parts = append(parts, part)
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
