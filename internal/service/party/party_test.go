package party

import (
	"testing"

	"taletogether/internal/model"
)

func TestLeader(t *testing.T) {
	t.Parallel()
	members := []model.Character{
		{ID: 1, UserID: 10, Status: model.CharacterInactive},
		{ID: 2, UserID: 11, Status: model.CharacterActive},
		{ID: 3, UserID: 12, Status: model.CharacterWaiting},
	}
	cases := []struct {
		name  string
		admin model.UserID
		want  model.CharacterID
	}{
		{"admin present", 12, 3},
		{"admin absent", 10, 2},
		{"no admin", 99, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := Leader(members, tc.admin); !ok || got != tc.want {
				t.Fatalf("Leader = %d, %v; want %d", got, ok, tc.want)
			}
		})
	}
	if got, ok := Leader(members[:1], 0); !ok || got != 1 {
		t.Errorf("all absent: %d, %v", got, ok)
	}
	if _, ok := Leader(nil, 0); ok {
		t.Error("empty members")
	}
}

func TestSplitLeaderAndNames(t *testing.T) {
	t.Parallel()
	if id, ok := SplitLeader([]model.SnapshotCharacter{{ID: 1, Attributes: map[string]int{"will": 3}}, {ID: 2, Attributes: map[string]int{"will": 5}}, {ID: 3, Attributes: map[string]int{"will": 5}}}); !ok || id != 2 {
		t.Errorf("split leader = %d, %v", id, ok)
	}
	if _, ok := SplitLeader(nil); ok {
		t.Error("empty split")
	}
	names := []string{"Ночной дозор", "Вольные клинки"}
	if got := NextName(names, nil); got != "Ночной дозор" {
		t.Errorf("first = %q", got)
	}
	if got := NextName(names, []string{"ночной дозор"}); got != "Вольные клинки" {
		t.Errorf("second = %q", got)
	}
	if got := NextName(names, []string{"Ночной дозор", "Вольные клинки", "Группа 3"}); got != "Группа 4" {
		t.Errorf("fallback = %q", got)
	}
	if err := ValidateName("Я"); err == nil {
		t.Error("short name accepted")
	}
	if err := ValidateName("#1"); err == nil {
		t.Error("hash name accepted")
	}
	if err := ValidateName("Ночной дозор"); err != nil {
		t.Error(err)
	}
}

func TestNearestAndFind(t *testing.T) {
	t.Parallel()
	locs := []model.Location{
		{ID: 1, Name: "Площадь", X: 0, Y: 0},
		{ID: 2, Name: "Зал", X: 1, Y: 0},
		{ID: 3, Name: "Подвал", X: 2, Y: 0},
		{ID: 4, Name: "Остров", X: 5, Y: 5},
	}
	if got, ok := Nearest(locs, 1, []model.LocationID{3, 2}); !ok || got != 2 {
		t.Errorf("nearest = %d, %v", got, ok)
	}
	if got, ok := Nearest(locs, 1, []model.LocationID{4, 3}); !ok || got != 3 {
		t.Errorf("nearest far = %d, %v", got, ok)
	}
	if _, ok := Nearest(locs, 1, []model.LocationID{9}); ok {
		t.Error("unknown location found")
	}
	parties := []model.Party{{ID: 5, Name: "Ночной дозор", Status: model.PartyActive}, {ID: 6, Name: "Старые", Status: model.PartyDisbanded}}
	if p, ok := Find(parties, "ночной ДОЗОР"); !ok || p.ID != 5 {
		t.Errorf("find by name = %+v, %v", p, ok)
	}
	if p, ok := Find(parties, "#5"); !ok || p.ID != 5 {
		t.Errorf("find by id = %+v, %v", p, ok)
	}
	if _, ok := Find(parties, "Старые"); ok {
		t.Error("disbanded found")
	}
	if Active(parties) != 1 {
		t.Error("active count")
	}
}

func TestAssignAndStale(t *testing.T) {
	t.Parallel()
	locs := []model.Location{
		{ID: 1, Name: "Площадь", X: 0, Y: 0},
		{ID: 2, Name: "Зал", X: 1, Y: 0},
		{ID: 3, Name: "Подвал", X: 2, Y: 0},
	}
	loc := func(id model.LocationID) *model.LocationID { return &id }
	active := []model.Party{{ID: 5, Status: model.PartyActive, LocationID: loc(3)}, {ID: 6, Status: model.PartyActive, LocationID: loc(2)}, {ID: 7, Status: model.PartyDisbanded, LocationID: loc(1)}}
	if d, ok := Assign(active, locs, 2, 3); !ok || d.Join != 6 || d.Location != 2 {
		t.Errorf("same location = %+v, %v", d, ok)
	}
	if d, ok := Assign(active, locs, 1, 3); !ok || !d.Create || d.Location != 1 {
		t.Errorf("create = %+v, %v", d, ok)
	}
	if d, ok := Assign(active, locs, 1, 2); !ok || d.Join != 6 || d.Location != 2 {
		t.Errorf("nearest = %+v, %v", d, ok)
	}
	if _, ok := Assign(nil, locs, 1, 0); !ok {
		t.Error("no limit must create")
	}
	if d, ok := Assign([]model.Party{{ID: 9, Status: model.PartyActive}}, locs, 1, 1); !ok || d.Join != 9 || d.Location != 1 {
		t.Errorf("unreachable fallback = %+v, %v", d, ok)
	}
	chars := []model.Character{{ID: 1, PartyID: ptrParty(5), Status: model.CharacterInactive}, {ID: 2, PartyID: ptrParty(6), Status: model.CharacterWaiting}}
	if stale := Stale(active, chars); len(stale) != 1 || stale[0].ID != 5 {
		t.Errorf("stale = %+v", stale)
	}
}

func ptrParty(id model.PartyID) *model.PartyID { return &id }
