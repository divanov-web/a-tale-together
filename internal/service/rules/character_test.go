package rules

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidateAttributes(t *testing.T) {
	t.Parallel()
	def := basicFantasy()
	tests := []struct {
		name    string
		attrs   map[string]int
		wantErr bool
	}{
		{name: "valid sum 14", attrs: map[string]int{"strength": 5, "agility": 3, "wits": 2, "will": 4}},
		{name: "sum too high", attrs: map[string]int{"strength": 5, "agility": 4, "wits": 2, "will": 4}, wantErr: true},
		{name: "below min", attrs: map[string]int{"strength": 1, "agility": 5, "wits": 5, "will": 3}, wantErr: true},
		{name: "missing key", attrs: map[string]int{"strength": 4, "agility": 4, "wits": 4}, wantErr: true},
		{name: "unknown key", attrs: map[string]int{"strength": 3, "agility": 3, "wits": 3, "luck": 3}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateAttributes(def, tt.attrs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAttributes() error = %v; wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error %v is not ErrInvalid", err)
			}
		})
	}
}

func TestValidateSkills(t *testing.T) {
	t.Parallel()
	def := basicFantasy()
	tests := []struct {
		name    string
		skills  map[string]int
		wantErr bool
	}{
		{name: "valid 3+2+1", skills: map[string]int{"melee": 3, "survival": 2, "awareness": 1}},
		{name: "valid with zeros", skills: map[string]int{"melee": 3, "survival": 3, "awareness": 0}},
		{name: "over max", skills: map[string]int{"melee": 4, "survival": 2}, wantErr: true},
		{name: "sum too low", skills: map[string]int{"melee": 3, "survival": 2}, wantErr: true},
		{name: "unknown skill", skills: map[string]int{"magic": 3, "survival": 3}, wantErr: true},
		{name: "negative", skills: map[string]int{"melee": 3, "survival": 3, "stealth": 1, "thievery": -1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateSkills(def, tt.skills); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSkills() error = %v; wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAutoDistribute_AllArchetypesValid(t *testing.T) {
	t.Parallel()
	def := basicFantasy()
	for _, a := range def.Archetypes {
		attrs, skills, err := AutoDistribute(def, a.Key)
		if err != nil {
			t.Fatalf("AutoDistribute(%s): %v", a.Key, err)
		}
		if err := ValidateAttributes(def, attrs); err != nil {
			t.Fatalf("archetype %s attributes: %v", a.Key, err)
		}
		if err := ValidateSkills(def, skills); err != nil {
			t.Fatalf("archetype %s skills: %v", a.Key, err)
		}
		if len(skills) != len(def.Skills) {
			t.Fatalf("archetype %s: skills has %d keys; want all %d", a.Key, len(skills), len(def.Skills))
		}
	}
	if _, _, err := AutoDistribute(def, "bard"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown archetype error = %v; want ErrInvalid", err)
	}
}

func TestHP(t *testing.T) {
	t.Parallel()
	def := basicFantasy()
	hp, err := HP(def, map[string]int{"strength": 4})
	if err != nil || hp != 8 {
		t.Fatalf("HP() = %d, %v; want 8, nil", hp, err)
	}
	def.HPFormula = "strength * 2"
	if _, err := HP(def, map[string]int{"strength": 4}); err == nil {
		t.Fatal("unsupported formula accepted")
	}
}

func TestParseAttributes(t *testing.T) {
	t.Parallel()
	def := basicFantasy()
	want := map[string]int{"strength": 4, "agility": 3, "wits": 2, "will": 3}
	tests := []struct {
		name    string
		text    string
		want    map[string]int
		wantErr bool
	}{
		{name: "numbers in order", text: "4 3 2 3", want: want},
		{name: "titles any order", text: "Воля 3, Сила 4, Разум 2, Ловкость 3", want: want},
		{name: "keys", text: "strength=4 agility=3 wits=2 will=3", want: want},
		{name: "wrong count", text: "4 3 2", wantErr: true},
		{name: "unknown title", text: "удача 4 сила 3", wantErr: true},
		{name: "not a number", text: "сила четыре", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseAttributes(def, tt.text)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseAttributes(%q) error = %v; wantErr %v", tt.text, err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseAttributes(%q) = %v; want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestParseSkills(t *testing.T) {
	t.Parallel()
	def := basicFantasy()
	got, err := ParseSkills(def, "Бой 3, выживание 2, внимательность 1")
	if err != nil {
		t.Fatalf("ParseSkills: %v", err)
	}
	want := map[string]int{"athletics": 0, "melee": 3, "survival": 2, "awareness": 1, "thievery": 0, "persuasion": 0, "stealth": 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseSkills = %v; want %v", got, want)
	}
	if _, err := ParseSkills(def, "магия 3"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown skill error = %v; want ErrInvalid", err)
	}
}

func TestFindArchetype(t *testing.T) {
	t.Parallel()
	def := basicFantasy()
	for _, in := range []string{"2", "rogue", "Плут", " плут "} {
		a, ok := FindArchetype(def, in)
		if !ok || a.Key != "rogue" {
			t.Fatalf("FindArchetype(%q) = %v, %v; want rogue", in, a.Key, ok)
		}
	}
	if _, ok := FindArchetype(def, "9"); ok {
		t.Fatal("FindArchetype(9) found")
	}
}
