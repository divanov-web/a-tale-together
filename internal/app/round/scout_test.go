package round

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"taletogether/internal/model"
)

// TestScoutText (S19, B36): видимый сосед — имя и карточка, скрытый — «неизведанная территория» с видом издалека или
// без него, направление без клетки — «неизведанная территория»; направления жирным (B39, ADR-0029); без локации — отказ.
func TestScoutText(t *testing.T) {
	t.Parallel()
	here := model.LocationID(1)
	locations := []model.Location{
		{ID: 1, Name: "Площадь", X: 0, Y: 0, Stage: model.StageDetailed, Start: true},
		{ID: 2, Name: "Лес", Description: "Тёмная кромка леса.", X: 0, Y: 1, Stage: model.StageCard},
		{ID: 3, Name: "Деревня", Description: "Десяток дворов.", X: 1, Y: 0, Stage: model.StageCard, Hidden: true, DistantView: "Дым над крышами."},
		{ID: 4, Name: "Пещера", Description: "Чёрный зев.", X: 0, Y: -1, Stage: model.StageCard, Hidden: true},
		{ID: 5, Name: "Подвал", Description: "Сырой подвал.", X: 5, Y: 5, Stage: model.StageDetailed},
	}
	got, err := ScoutText(model.Character{LocationID: &here}, locations)
	if err != nil {
		t.Fatal(err)
	}
	want := "Разведка: Площадь (0, 0)\n\n• на север: Лес — Тёмная кромка леса.\n• на юг: неизведанная территория\n• на запад: неизведанная территория\n• на восток: неизведанная территория (Дым над крышами)\n\nПереход: напишите, куда идёт персонаж, — направление или название места."
	if got.Text != want {
		t.Fatalf("scout =\n%q\nwant\n%q", got.Text, want)
	}
	// Жирным ровно четыре направления: смещения в рунах.
	wantBold := []model.TextEntity{{Kind: model.EntityBold, Offset: 28, Length: 8}, {Kind: model.EntityBold, Offset: 66, Length: 5}, {Kind: model.EntityBold, Offset: 99, Length: 8}, {Kind: model.EntityBold, Offset: 135, Length: 9}}
	if !reflect.DeepEqual(got.Entities, wantBold) {
		t.Fatalf("entities = %+v, want %+v", got.Entities, wantBold)
	}
	for _, e := range got.Entities {
		if word := string([]rune(got.Text)[e.Offset : e.Offset+e.Length]); !strings.HasPrefix(word, "на ") {
			t.Fatalf("bold %q", word)
		}
	}
	// Посещённый сосед — только имя.
	visited := model.LocationID(5)
	locations = append(locations, model.Location{ID: 6, Name: "Чердак", Description: "Пыль.", X: 5, Y: 6, Stage: model.StageDetailed})
	if got, _ := ScoutText(model.Character{LocationID: &visited}, locations); got.Text != "Разведка: Подвал (5, 5)\n\n• на север: Чердак\n• на юг: неизведанная территория\n• на запад: неизведанная территория\n• на восток: неизведанная территория\n\nПереход: напишите, куда идёт персонаж, — направление или название места." {
		t.Fatalf("visited neighbor = %q", got.Text)
	}
	if _, err := ScoutText(model.Character{}, locations); !errors.Is(err, ErrNoLocation) {
		t.Fatalf("no location = %v", err)
	}
	gone := model.LocationID(99)
	if _, err := ScoutText(model.Character{LocationID: &gone}, locations); !errors.Is(err, ErrNoLocation) {
		t.Fatalf("unknown location = %v", err)
	}
}
