package model

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Разметка сообщений (ADR-0029): текст остаётся простым (журнал, тесты, outbox), выделенные отрезки идут отдельным
// списком, как entities Telegram. Экранирование не нужно, `parse_mode` не используется.

// EntityKind — вид выделения отрезка.
type EntityKind string

// EntityBold — жирный текст.
const EntityBold EntityKind = "bold"

// TextEntity — выделенный отрезок текста: смещение и длина в рунах; адаптер пересчитывает их в единицы UTF-16.
// Теги JSON — для хранения в `messages_outbox.entities` (ADR-0030).
type TextEntity struct {
	Kind   EntityKind `json:"kind"`
	Offset int        `json:"offset"`
	Length int        `json:"length"`
}

// FormattedText — текст с разметкой; пустые Entities — обычное сообщение.
type FormattedText struct {
	Text     string
	Entities []TextEntity
}

// Plain — текст без разметки.
func Plain(text string) FormattedText { return FormattedText{Text: text} }

// Cut возвращает отрезок текста в рунах [from, to) с разметкой: отрезки за границами отбрасываются, пересекающие
// границу обрезаются, смещения считаются от начала отрезка. Границы за пределами текста подтягиваются к нему.
func (f FormattedText) Cut(from, to int) FormattedText {
	runes := []rune(f.Text)
	from, to = max(from, 0), min(to, len(runes))
	if from >= to {
		return FormattedText{}
	}
	out := FormattedText{Text: string(runes[from:to])}
	for _, e := range f.Entities {
		start, end := max(e.Offset, from), min(e.Offset+e.Length, to)
		if start >= end {
			continue
		}
		out.Entities = append(out.Entities, TextEntity{Kind: e.Kind, Offset: start - from, Length: end - start})
	}
	return out
}

// TextBuilder собирает текст с разметкой: обычные куски — WriteString и Printf, выделенные — Bold.
type TextBuilder struct {
	b        strings.Builder
	runes    int
	entities []TextEntity
}

// WriteString добавляет обычный текст.
func (t *TextBuilder) WriteString(s string) {
	t.b.WriteString(s)
	t.runes += utf8.RuneCountInString(s)
}

// Printf добавляет обычный текст по формату.
func (t *TextBuilder) Printf(format string, a ...any) { t.WriteString(fmt.Sprintf(format, a...)) }

// Bold добавляет отрезок жирным; пустая строка не добавляет ничего.
func (t *TextBuilder) Bold(s string) {
	if s == "" {
		return
	}
	t.entities = append(t.entities, TextEntity{Kind: EntityBold, Offset: t.runes, Length: utf8.RuneCountInString(s)})
	t.WriteString(s)
}

// Key добавляет ключ строки «ключ: значение» жирным вместе с двоеточием и пробелом (правило B40); значение
// пишется следом обычным текстом.
func (t *TextBuilder) Key(key string) {
	t.Bold(key)
	t.WriteString(": ")
}

// Header добавляет заголовок списка «Заголовок:» жирным с двоеточием (правило B40); пункты списка идут следом с новых
// строк.
func (t *TextBuilder) Header(title string) {
	t.Bold(title)
	t.WriteString(":")
}

// Append добавляет текст с разметкой, сдвигая его отрезки на текущую длину.
func (t *TextBuilder) Append(f FormattedText) {
	for _, e := range f.Entities {
		t.entities = append(t.entities, TextEntity{Kind: e.Kind, Offset: t.runes + e.Offset, Length: e.Length})
	}
	t.WriteString(f.Text)
}

// Len — длина собранного текста в рунах.
func (t *TextBuilder) Len() int { return t.runes }

// Build возвращает собранный текст с разметкой.
func (t *TextBuilder) Build() FormattedText {
	return FormattedText{Text: t.b.String(), Entities: t.entities}
}
