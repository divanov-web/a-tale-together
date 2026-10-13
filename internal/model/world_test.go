package model

import (
	"encoding/json"
	"strings"
	"testing"
)

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

// TestGlimpse_HiddenExit (ADR-0025): подпись выхода скрывает имя невидимого соседа, `ExitTo` не находит скрытого
// соседа по имени, но находит по направлению; старые снимки без поля `hidden` остаются видимыми.
func TestGlimpse_HiddenExit(t *testing.T) {
	t.Parallel()
	snap := RoundSnapshot{Locations: []SnapshotLocation{
		{ID: 1, Name: "Площадь", X: 0, Y: 0},
		{ID: 2, Name: "Лес", X: 0, Y: 1},
		{ID: 3, Name: "Деревня", X: 1, Y: 0, Hidden: true, DistantView: "Дым над крышами."},
		{ID: 4, Name: "Пещера", X: 0, Y: -1, Hidden: true},
	}}
	here := &snap.Locations[0]
	var titles []string
	for _, e := range snap.Exits(here) {
		titles = append(titles, e.Title())
	}
	if got := strings.Join(titles, "; "); got != "на север: Лес; на юг: неизведанная территория; на восток: неизведанная территория (Дым над крышами)" {
		t.Fatalf("titles = %q", got)
	}
	if _, ok := snap.ExitTo(here, "Деревня"); ok {
		t.Fatal("hidden neighbor matched by name")
	}
	if e, ok := snap.ExitTo(here, "на восток"); !ok || e.To.ID != 3 {
		t.Fatalf("hidden neighbor by direction = %+v, %v", e, ok)
	}
	if e, ok := snap.ExitTo(here, "лес"); !ok || e.To.ID != 2 {
		t.Fatalf("visible neighbor by name = %+v, %v", e, ok)
	}
	if (Location{Name: "Лес"}).Glimpse() != "Лес" || (Location{Name: "Лес", Hidden: true}).Glimpse() != UnknownTerritory {
		t.Error("location glimpse")
	}
	var old SnapshotLocation
	if err := json.Unmarshal([]byte(`{"id":7,"name":"Старая","x":0,"y":0}`), &old); err != nil || old.Hidden || old.Glimpse() != "Старая" {
		t.Fatalf("old snapshot = %+v, %v", old, err)
	}
}
