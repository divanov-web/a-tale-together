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
type TextEntity struct {
	Kind   EntityKind
	Offset int
	Length int
}

// FormattedText — текст с разметкой; пустые Entities — обычное сообщение.
type FormattedText struct {
	Text     string
	Entities []TextEntity
}

// Plain — текст без разметки.
func Plain(text string) FormattedText { return FormattedText{Text: text} }

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

// Build возвращает собранный текст с разметкой.
func (t *TextBuilder) Build() FormattedText {
	return FormattedText{Text: t.b.String(), Entities: t.entities}
}
