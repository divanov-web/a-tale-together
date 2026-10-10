package main

import (
	"encoding/json"
	"strings"
	"testing"

	"taletogether/assets/prompts"
	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

// TestCases_Parse: оценочный набор читается, у каждого случая есть состояние и сцены, контекст собирается без модели;
// секреты попадают во вход только как данные персонажей сцен.
func TestCases_Parse(t *testing.T) {
	t.Parallel()
	raw, err := prompts.FS.ReadFile("narrator/eval/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var set evalSet
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	if len(set.Cases) < 10 || len(set.Factions) != 2 || len(set.CharacterModel.Attributes) != 4 {
		t.Fatalf("set = %d cases, %d factions", len(set.Cases), len(set.Factions))
	}
	for _, c := range set.Cases {
		if c.Params.State == nil || c.Params.Mode == "" || c.Params.RoundNo == 0 {
			t.Fatalf("%s: params incomplete", c.Name)
		}
		in := narrator.BuildInput(narrator.Params{Title: set.Title, Tone: set.Tone, Factions: set.Factions, Def: set.CharacterModel,
			Mode: c.Params.Mode, RoundNo: c.Params.RoundNo, Events: c.Params.Events, Bios: c.Params.Bios, Previous: c.Params.Previous, State: c.Params.State})
		if len(in.Scenes) == 0 || len(in.Events) != len(c.Params.Events) {
			t.Fatalf("%s: input = %d scenes, %d events", c.Name, len(in.Scenes), len(in.Events))
		}
		if c.Params.Mode == model.ModeNarrative && strings.Contains(in.Encode(), `"difficulty"`) {
			t.Fatalf("%s: narrative input carries difficulty", c.Name)
		}
	}
}
