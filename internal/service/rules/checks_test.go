package rules

import (
	"testing"

	"taletogether/internal/model"
)

func TestChancePct_MatchesTable(t *testing.T) {
	t.Parallel()
	// Таблица docs/game/checks.md.
	cases := map[[2]int]int{{2, 1}: 31, {2, 2}: 3, {4, 1}: 52, {4, 2}: 13, {6, 1}: 67, {6, 2}: 26, {6, 3}: 6, {8, 2}: 40, {10, 1}: 84, {10, 4}: 7, {1, 2}: 0, {3, 0}: 100}
	for in, want := range cases {
		if got := ChancePct(in[0], in[1]); got != want {
			t.Errorf("ChancePct(%d, %d) = %d; want %d", in[0], in[1], got, want)
		}
	}
}

func TestPool_And_Difficulty(t *testing.T) {
	t.Parallel()
	c := model.SnapshotCharacter{Attributes: map[string]int{"agility": 4}, Skills: map[string]int{"thievery": 3}, Conditions: []string{model.ConditionWounded},
		Inventory: []model.Item{{Key: "lockpicks", Bonus: &model.ItemBonus{Skill: "thievery", Dice: 1}}, {Key: "kit", Bonus: &model.ItemBonus{Skill: "thievery", Dice: 2}}}}
	if got := Pool(c, "agility", "thievery", 3); got != 4+3+2-1+2 {
		t.Fatalf("Pool = %d", got)
	}
	if got := Pool(model.SnapshotCharacter{Conditions: []string{model.ConditionExhausted}}, "strength", "", 0); got != 1 {
		t.Fatalf("Pool floor = %d", got)
	}
	if got := Difficulty(Preset(model.ModeNarrative), 3, 1); got != 2 {
		t.Fatalf("narrative difficulty = %d; want cap 1 + location 1", got)
	}
	if got := Difficulty(Preset(model.ModeMechanics), 3, 1); got != 4 {
		t.Fatalf("mechanics difficulty = %d", got)
	}
	if RiskDifficulty(model.RiskExtreme) != 3 || RiskDifficulty("") != 1 {
		t.Fatal("risk table")
	}
	if RoundsPerDay(15, 0) != 96 || RoundsPerDay(20, 12*60) != 36 || DeathRounds(10, 36) != 360 || DeathRounds(0, 1) != 10 {
		t.Fatal("rounds per day / death rounds")
	}
}

func TestParseTyped(t *testing.T) {
	t.Parallel()
	cases := []struct {
		text   string
		action model.ActionType
		target string
		push   bool
		amb    bool
	}{
		{"идти вниз в подвал", model.ActionMove, "вниз в подвал", false, false},
		{"Открыть дверь force", model.ActionInteract, "дверь", true, false},
		{"wait", model.ActionWait, "", false, false},
		{"помочь @Вор", model.ActionHelp, "@вор", false, false},
		{"осмотреть «статую».", model.ActionExplore, "статую", false, false},
		{"крадусь к башне и слушаю", model.ActionSneak, "к башне и слушаю", false, false},
		{"что-то странное делаю", "", "", false, true},
	}
	for _, tc := range cases {
		in := ParseTyped(model.Submission{ID: 1, CharacterID: 2, RawText: tc.text})
		if in.ActionType != tc.action || in.Target != tc.target || in.Push != tc.push || (len(in.Ambiguities) > 0) != tc.amb || in.RawText != tc.text {
			t.Errorf("ParseTyped(%q) = %+v", tc.text, in)
		}
	}
}
