package round

import (
	"strings"
	"testing"
)

// TestSplitParts: короткий текст — одна часть; длинный делится по строкам, строка длиннее лимита режется по символам,
// части после первой получают подпись продолжения и не превышают лимит.
func TestSplitParts(t *testing.T) {
	t.Parallel()
	if got := splitParts("короткий\nтекст", "П\n", 100); len(got) != 1 || got[0] != "короткий\nтекст" {
		t.Fatalf("short = %q", got)
	}
	lines := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		lines = append(lines, strings.Repeat("я", 9))
	}
	text := strings.Join(lines, "\n") + "\n" + strings.Repeat("ж", 70)
	got := splitParts(text, "П\n", 40)
	if len(got) < 5 {
		t.Fatalf("parts = %d: %q", len(got), got)
	}
	for i, p := range got {
		if n := len([]rune(p)); n > 40 {
			t.Errorf("part %d has %d runes", i, n)
		}
		if i > 0 && !strings.HasPrefix(p, "П\n") {
			t.Errorf("part %d without prefix: %q", i, p)
		}
		if i == 0 && strings.HasPrefix(p, "П\n") {
			t.Errorf("first part with prefix: %q", p)
		}
	}
	joined := strings.ReplaceAll(strings.Join(got, "\n"), "П\n", "")
	if strings.Count(joined, "я") != 270 || strings.Count(joined, "ж") != 70 {
		t.Errorf("text lost: %q", joined)
	}
}
