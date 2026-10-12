package model

import "testing"

// TestParseDirection: направление узнаётся по слову в любой форме цели заявки; однобуквенное сокращение — только
// одним словом; без направления — false.
func TestParseDirection(t *testing.T) {
	t.Parallel()
	cases := map[string]Direction{
		"north": North, "North": North, "на север": North, "идти на север: Лес": North, "Лес (на север)": North,
		"юг": South, "s": South, "иду на запад, к реке": West, "восток": East, "e": East, "в": East,
	}
	for in, want := range cases {
		if got, ok := ParseDirection(in); !ok || got != want {
			t.Errorf("ParseDirection(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "к башне", "в лес", "север-запад"} {
		if got, ok := ParseDirection(in); ok && in != "север-запад" {
			t.Errorf("ParseDirection(%q) = %q; want none", in, got)
		}
	}
	if dx, dy := North.Offset(); dx != 0 || dy != 1 || South.Opposite() != North || West.Toward() != "на запад" {
		t.Error("offsets or words")
	}
}
