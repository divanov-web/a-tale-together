package visibility

import (
	"testing"

	"taletogether/internal/model"
)

func TestFilter_NoLeaks(t *testing.T) {
	t.Parallel()
	loc1, loc2 := model.LocationID(1), model.LocationID(2)
	events := []model.Event{
		{Seq: 1, Type: model.EventCharacterReturned, Visibility: model.VisibilityPublic},
		{Seq: 2, Type: model.EventObjectChanged, Visibility: model.VisibilityLocation, LocationID: &loc1},
		{Seq: 3, Type: model.EventFactLearned, Visibility: model.VisibilityPrivate, VisibleTo: []model.CharacterID{11}},
		{Seq: 4, Type: model.EventHelpGiven, Visibility: model.VisibilityPrivate, VisibleTo: []model.CharacterID{12, 13}},
	}
	cases := []struct {
		name string
		v    Viewer
		want []int64
	}{
		{"in location, private 11", Viewer{CharacterID: 11, LocationID: &loc1}, []int64{1, 2, 3}},
		{"other location, private 12", Viewer{CharacterID: 12, LocationID: &loc2}, []int64{1, 4}},
		{"nowhere, no private", Viewer{CharacterID: 99}, []int64{1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Filter(events, tc.v)
			var seqs []int64
			for _, ev := range got {
				seqs = append(seqs, ev.Seq)
			}
			if len(seqs) != len(tc.want) {
				t.Fatalf("seqs = %v; want %v", seqs, tc.want)
			}
			for i := range seqs {
				if seqs[i] != tc.want[i] {
					t.Fatalf("seqs = %v; want %v", seqs, tc.want)
				}
			}
		})
	}
	if pub := Public(events); len(pub) != 2 || pub[1].Seq != 2 {
		t.Fatalf("Public = %+v", pub)
	}
}
