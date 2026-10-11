package narrator

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"taletogether/internal/model"
)

func openingParams() OpeningParams {
	return OpeningParams{Title: "Серая башня", Genre: "Классическое фэнтези", Tone: "лёгкая ирония", Mode: model.ModeBalanced,
		Factions:   []Faction{{Code: "good", Title: "Орден"}, {Code: "evil", Title: "Гильдия"}},
		Locations:  []OpeningLocation{{Name: "Площадь", Description: "Сердце города.", Start: true, Exits: []string{"Башня"}}, {Name: "Башня", Description: "Старая."}},
		Characters: []OpeningCharacter{{Nickname: "Элис", Gender: "female", Faction: "Орден", Archetype: "rogue", PublicBio: "Воровка"}, {Nickname: "Боб", PublicBio: "Кузнец"}}}
}

var goodOpening = strings.Repeat("Над площадью туман, и Элис с Бобом ждут вестей от Ордена, пока Гильдия молчит. ", 2)

// TestOpening_Valid: вход содержит мир, локации и открытые данные персонажей; валидный ответ принимается с первого вызова.
func TestOpening_Valid(t *testing.T) {
	t.Parallel()
	var got model.LLMRequest
	svc := New(Func(func(_ context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		got = req
		return model.LLMResponse{JSON: []byte(`{"text":"` + goodOpening + `"}`), TokensIn: 7, TokensOut: 3}, nil
	}), slog.Default())
	res, err := svc.Opening(context.Background(), openingParams())
	if err != nil || res.Calls != 1 || len(res.Issues) != 0 || res.Text != strings.TrimSpace(goodOpening) || res.TokensIn != 7 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if got.Role != model.RoleOpening || got.System != OpeningSystemPrompt() || string(got.JSONSchema) != string(OpeningSchema) {
		t.Fatalf("request = %+v", got)
	}
	for _, want := range []string{`"title":"Серая башня"`, `"genre":"Классическое фэнтези"`, `"mode":"balanced"`, `"start":true`, `"exits":["Башня"]`, `"exits":[]`, `"nickname":"Элис"`, `"gender":"female"`, `"public_bio":"Кузнец"`} {
		if !strings.Contains(got.Input, want) {
			t.Fatalf("input without %s: %s", want, got.Input)
		}
	}
}

// TestOpening_RetryAndIssues: цифры и непредставленный персонаж дают дозапрос с замечаниями; исправленный ответ принимается,
// слишком короткий после дозапроса — ErrOpening; ошибка провайдера пробрасывается.
func TestOpening_RetryAndIssues(t *testing.T) {
	t.Parallel()
	answers := []string{`{"text":"` + strings.Repeat("Элис ждёт у башни уже 3 часа, и никто не приходит. ", 3) + `"}`, `{"text":"` + goodOpening + `"}`}
	var inputs []string
	svc := New(Func(func(_ context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		inputs = append(inputs, req.Input)
		out := answers[0]
		answers = answers[1:]
		return model.LLMResponse{JSON: []byte(out)}, nil
	}), slog.Default())
	res, err := svc.Opening(context.Background(), openingParams())
	if err != nil || res.Calls != 2 || res.Text != strings.TrimSpace(goodOpening) || len(res.Issues) != 2 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if !strings.Contains(inputs[1], "Замечания к прошлому ответу") || !strings.Contains(inputs[1], "цифры") || !strings.Contains(inputs[1], `"Боб" не представлен`) {
		t.Fatalf("retry input = %s", inputs[1])
	}

	short := New(Func(func(context.Context, model.LLMRequest) (model.LLMResponse, error) {
		return model.LLMResponse{JSON: []byte(`{"text":"Коротко."}`)}, nil
	}), slog.Default())
	if res, err := short.Opening(context.Background(), openingParams()); !errors.Is(err, ErrOpening) || res.Calls != 2 {
		t.Fatalf("short = %+v, %v", res, err)
	}

	down := New(Func(func(context.Context, model.LLMRequest) (model.LLMResponse, error) {
		return model.LLMResponse{}, errors.New("provider down")
	}), slog.Default())
	if _, err := down.Opening(context.Background(), openingParams()); err == nil || !strings.Contains(err.Error(), "provider down") {
		t.Fatalf("down = %v", err)
	}
	if _, err := New(nil, slog.Default()).Opening(context.Background(), openingParams()); !errors.Is(err, ErrNoLLM) {
		t.Fatalf("nil llm = %v", err)
	}
}

// TestBuildInput_OpeningFirst: завязка идёт первой строкой выжимки прошлого, длинная обрезается.
func TestBuildInput_OpeningFirst(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("а", openingDigestRune+10)
	in := BuildInput(Params{Title: "Мир", Opening: long, Previous: []model.Narration{{Chronicle: "Раунд два."}, {Chronicle: "Раунд один."}}})
	if len(in.World.Previous) != 3 || in.World.Previous[0] != strings.Repeat("а", openingDigestRune)+"…" || in.World.Previous[1] != "Раунд один." || in.World.Previous[2] != "Раунд два." {
		t.Fatalf("previous = %q", in.World.Previous)
	}
	if in := BuildInput(Params{Title: "Мир"}); len(in.World.Previous) != 0 {
		t.Fatalf("previous without opening = %q", in.World.Previous)
	}
}
