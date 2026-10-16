package rules

import (
	"testing"

	"taletogether/internal/model"
)

func TestCheckCooldown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		action model.CooldownAction
		last   int
		round  int
		want   CooldownState
	}{
		{"never used", model.CooldownBio, 0, 5, CooldownState{Ready: true}},
		{"before first round", model.CooldownBio, 0, 0, CooldownState{Ready: true}},
		{"same round", model.CooldownBio, 1, 1, CooldownState{Left: 10, ReadyAt: 11}},
		{"one left", model.CooldownBio, 1, 10, CooldownState{Left: 1, ReadyAt: 11}},
		{"ready exactly", model.CooldownBio, 1, 11, CooldownState{Ready: true, ReadyAt: 11}},
		{"ready later", model.CooldownSecret, 3, 40, CooldownState{Ready: true, ReadyAt: 13}},
		{"rename 100", model.CooldownRename, 2, 50, CooldownState{Left: 52, ReadyAt: 102}},
		{"hint 5", model.CooldownHint, 7, 9, CooldownState{Left: 3, ReadyAt: 12}},
		{"unknown action", model.CooldownAction("dance"), 1, 1, CooldownState{Ready: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CheckCooldown(tc.action, tc.last, tc.round); got != tc.want {
				t.Fatalf("CheckCooldown(%s, %d, %d) = %+v; want %+v", tc.action, tc.last, tc.round, got, tc.want)
			}
		})
	}
}
