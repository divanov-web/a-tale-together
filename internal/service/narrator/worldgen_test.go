package narrator

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"taletogether/internal/model"
)

func worldgenParams() WorldgenParams {
	return WorldgenParams{Genre: "Классическое фэнтези", Tone: "лёгкая ирония", Seed: "game-7-attempt-1",
		Factions: []Faction{{Code: "good", Title: "Орден Рассвета", Relations: map[string]string{"evil": "hostile"}}, {Code: "evil", Title: "Гильдия теней"}},
		Start:    SetupStart{Name: "Рыночная площадь", Terrain: "town", Description: "Сердце города."}}
}

func setupJSON(title, world string) string {
	return `{"title":"` + title + `","world":"` + world + `","summary":"Городок у реки, пропали гуси.","tone":"Ирония, суета ярмарки.","premise":"Гуси исчезли ночью, ярмарка в полдень.",
 "factions":[{"code":"good","title":"Гильдия ярмарки","description":"Торговцы.","tone":"деловые","goal":"Спасти ярмарку."},{"code":"evil","title":"Братство тихих","description":"Музыканты.","tone":"скрытные","goal":"Спрятать барда."}],
 "start":{"name":"Гусиные ряды","terrain":"Village","description":"Пустые загоны и солома."}}`
}

// TestWorldgen_Valid: вход несёт жанр, лимиты и seed; четыре валидных варианта принимаются, местность нормализуется,
// пятый вариант сверх лимита и повтор заголовка отброшены с замечаниями.
func TestWorldgen_Valid(t *testing.T) {
	t.Parallel()
	var got model.LLMRequest
	options := []string{setupJSON("Гуси против молчания", "Ольховый Брод"), setupJSON("Соль и колокол", "Порт Косы"), setupJSON("гуси против молчания", "Повтор"),
		setupJSON("Наследство без ключа", "Усадьба Трёх Ив"), setupJSON("Ярмарка на костях", "Старый Мост"), setupJSON("Лишний", "Лишний мир")}
	svc := New(Func(func(_ context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		got = req
		return model.LLMResponse{JSON: []byte(`{"options":[` + strings.Join(options, ",") + `]}`), TokensIn: 9, TokensOut: 4}, nil
	}), slog.Default())
	res, err := svc.Worldgen(context.Background(), worldgenParams())
	if err != nil || len(res.Options) != 4 || res.TokensIn != 9 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if got.Role != model.RoleWorldgen || got.System != WorldgenSystemPrompt() || string(got.JSONSchema) != string(WorldgenSchema) {
		t.Fatalf("request = %+v", got)
	}
	for _, want := range []string{`"genre":{"title":"Классическое фэнтези"`, `"factions":[{"code":"good","title":"Орден Рассвета"`, `"start":{"name":"Рыночная площадь"`, `"limits":{"options":4,"title_max_chars":40`, `"seed":"game-7-attempt-1"`, `"avoid":[]`} {
		if !strings.Contains(got.Input, want) {
			t.Fatalf("input without %s: %s", want, got.Input)
		}
	}
	first := res.Options[0]
	if first.Title != "Гуси против молчания" || first.World != "Ольховый Брод" || first.Start.Terrain != "village" || len(first.Factions) != 2 || first.Factions[1].Code != model.FactionEvil || first.Factions[1].Goal != "Спрятать барда." {
		t.Fatalf("first option = %+v", first)
	}
	if res.Options[3].Title != "Ярмарка на костях" || len(res.Issues) != 2 || !strings.Contains(res.Issues[0], "повтор заголовка") || !strings.Contains(res.Issues[1], "сверх лимита") {
		t.Fatalf("options = %+v, issues = %v", res.Options, res.Issues)
	}
}

// TestWorldgen_Invalid: вариант без фракции жанра, с пустым заголовком или без завязки отбрасывается; меньше двух
// пригодных — ErrWorldgen; ошибка провайдера и ответ не по схеме пробрасываются.
func TestWorldgen_Invalid(t *testing.T) {
	t.Parallel()
	bad := strings.Replace(setupJSON("Без зла", "Мир"), `{"code":"evil"`, `{"code":"neutral"`, 1)
	empty := setupJSON("", "Мир без имени")
	noPremise := strings.Replace(setupJSON("Тишина", "Мир"), `"premise":"Гуси исчезли ночью, ярмарка в полдень."`, `"premise":""`, 1)
	cases := []struct {
		name, json string
		err        error
		options    int
	}{
		{"two valid of five", `{"options":[` + strings.Join([]string{bad, setupJSON("Один", "Мир один"), empty, noPremise, setupJSON("Два", "Мир два")}, ",") + `]}`, nil, 2},
		{"one valid", `{"options":[` + bad + `,` + setupJSON("Один", "Мир один") + `]}`, ErrWorldgen, 1},
		{"not json", `not json`, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := New(Func(func(_ context.Context, _ model.LLMRequest) (model.LLMResponse, error) {
				return model.LLMResponse{JSON: []byte(c.json)}, nil
			}), slog.Default())
			res, err := svc.Worldgen(context.Background(), worldgenParams())
			if c.err != nil && !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if c.err == nil && c.options > 0 && err != nil {
				t.Fatalf("err = %v", err)
			}
			if c.options == 0 && err == nil {
				t.Fatal("expected schema error")
			}
			if len(res.Options) != c.options {
				t.Fatalf("options = %+v (%d), want %d; issues %v", res.Options, len(res.Options), c.options, res.Issues)
			}
		})
	}
	svc := New(Func(func(_ context.Context, _ model.LLMRequest) (model.LLMResponse, error) {
		return model.LLMResponse{}, errors.New("provider down")
	}), slog.Default())
	if _, err := svc.Worldgen(context.Background(), worldgenParams()); err == nil || !strings.Contains(err.Error(), "provider down") {
		t.Fatalf("err = %v", err)
	}
}
