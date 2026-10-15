package round

import (
	"reflect"
	"strings"
	"testing"

	"taletogether/internal/model"
)

// TestSplitParts: короткий текст — одна часть; длинный делится по строкам, строка длиннее лимита режется по символам,
// части после первой получают подпись продолжения и не превышают лимит.
func TestSplitParts(t *testing.T) {
	t.Parallel()
	if got := splitParts(model.Plain("короткий\nтекст"), "П\n", 100); len(got) != 1 || got[0].Text != "короткий\nтекст" {
		t.Fatalf("short = %+v", got)
	}
	lines := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		lines = append(lines, strings.Repeat("я", 9))
	}
	text := strings.Join(lines, "\n") + "\n" + strings.Repeat("ж", 70)
	parts := splitParts(model.Plain(text), "П\n", 40)
	if len(parts) < 5 {
		t.Fatalf("parts = %d: %+v", len(parts), parts)
	}
	got := make([]string, 0, len(parts))
	for i, p := range parts {
		got = append(got, p.Text)
		if n := len([]rune(p.Text)); n > 40 {
			t.Errorf("part %d has %d runes", i, n)
		}
		if i > 0 && !strings.HasPrefix(p.Text, "П\n") {
			t.Errorf("part %d without prefix: %q", i, p.Text)
		}
		if i == 0 && strings.HasPrefix(p.Text, "П\n") {
			t.Errorf("first part with prefix: %q", p.Text)
		}
	}
	joined := strings.ReplaceAll(strings.Join(got, "\n"), "П\n", "")
	if strings.Count(joined, "я") != 270 || strings.Count(joined, "ж") != 70 {
		t.Errorf("text lost: %q", joined)
	}
}

// TestSplitParts_Entities (ADR-0030): разметка делится вместе с текстом — отрезок остаётся в своей части со
// смещением от её начала, с учётом подписи продолжения; отрезок через границу частей обрезается.
func TestSplitParts_Entities(t *testing.T) {
	t.Parallel()
	var b model.TextBuilder
	b.Key("Один")
	b.WriteString("раз\n")
	b.Key("Два")
	b.WriteString("два\n")
	b.Bold("Три три")
	parts := splitParts(b.Build(), "П\n", 12)
	want := []model.FormattedText{
		{Text: "Один: раз", Entities: []model.TextEntity{{Kind: model.EntityBold, Offset: 0, Length: 4}}},
		{Text: "П\nДва: два", Entities: []model.TextEntity{{Kind: model.EntityBold, Offset: 2, Length: 3}}},
		{Text: "П\nТри три", Entities: []model.TextEntity{{Kind: model.EntityBold, Offset: 2, Length: 7}}},
	}
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("parts =\n%+v\nwant\n%+v", parts, want)
	}
}
