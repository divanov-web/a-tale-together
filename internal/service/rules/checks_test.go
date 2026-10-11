package rules

import (
	"testing"

	"taletogether/internal/model"
)

func TestChancePct_MatchesTable(t *testing.T) {
	t.Parallel()
	// Таблица docs/game/checks.md: 50 + 10 × (параметр − сложность) в границах 5–95.
	cases := map[[2]int]int{{8, 5}: 80, {2, 5}: 20, {5, 5}: 50, {3, 5}: 30, {9, 5}: 90, {12, 5}: 95, {0, 5}: 5, {-1, 5}: 5, {2, 9}: 5, {8, 7}: 60, {8, 9}: 40}
	if !Success(80, 80) || Success(81, 80) || !Success(1, 5) || Success(96, 95) {
		t.Fatal("Success boundaries")
	}
	for in, want := range cases {
		if got := ChancePct(in[0], in[1]); got != want {
			t.Errorf("ChancePct(%d, %d) = %d; want %d", in[0], in[1], got, want)
		}
	}
}

func TestParam_And_Difficulty(t *testing.T) {
	t.Parallel()
	c := model.SnapshotCharacter{Attributes: map[string]int{"agility": 5}, Skills: map[string]int{"thievery": 3}, Conditions: []string{model.ConditionWounded},
		Inventory: []model.Item{{Key: "lockpicks", Bonus: &model.ItemBonus{Skill: "thievery", Points: 1}}, {Key: "kit", Bonus: &model.ItemBonus{Skill: "thievery", Points: 2}}}}
	if got := Param(c, "agility", "thievery", 3); got != 5+3+2-1+2 {
		t.Fatalf("Param = %d", got)
	}
	if got := Param(c, "strength", "", 0); got != -1 {
		t.Fatalf("Param without attribute and with a penalty = %d; want -1 (границы задаёт шанс)", got)
	}
	if got := Difficulty(Preset(model.ModeNarrative), 7, 1); got != 6 {
		t.Fatalf("narrative difficulty = %d; want cap 5 + location 1", got)
	}
	if got := Difficulty(Preset(model.ModeMechanics), 7, 1); got != 8 {
		t.Fatalf("mechanics difficulty = %d", got)
	}
	if RiskDifficulty(model.RiskExtreme) != 9 || RiskDifficulty(model.RiskHard) != 7 || RiskDifficulty("") != 5 {
		t.Fatal("risk table")
	}
	if DifficultyWords(5) != "обычно" || DifficultyWords(7) != "трудно" || DifficultyWords(9) != "очень трудно" || DifficultyWords(10) != "героически" {
		t.Fatal("difficulty words")
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
