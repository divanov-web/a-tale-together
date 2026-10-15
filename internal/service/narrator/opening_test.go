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
		Start:      OpeningStart{LocationID: 1, Name: "Площадь", Description: "Сердце города.", Terrain: "town", Objects: []string{"доска объявлений"}},
		Neighbors:  []NeighborTask{{X: 0, Y: 1, Direction: model.North, Of: 1, OfName: "Площадь"}, {X: 1, Y: 0, Direction: model.East, Of: 1, OfName: "Площадь"}},
		Def:        def(),
		Characters: []OpeningCharacter{{Nickname: "Элис", Gender: "female", Faction: "Орден", Archetype: "rogue", PublicBio: "Воровка"}, {Nickname: "Боб", PublicBio: "Кузнец"}}}
}

var goodOpening = strings.Repeat("Над площадью туман, и Элис с Бобом ждут вестей от Ордена, пока Гильдия молчит. ", 2)

// goodStart — стартовый пакет: клетка с двумя объектами (один скрытый, раскрывается подходом) и два соседа по заданию.
const goodStart = `"start":{"location_id":1,"name":"Площадь у колодца","description":"Сердце города; у колодца толпа.","difficulty":0,"objects":[
 {"key":"well","title":"колодец","description":"","state":"","hidden":false,"approaches":[{"action":"explore","title":"заглянуть в колодец","attribute":"wits","skill":"awareness","difficulty":5,"on_success":{"state":null,"reveal":"ring","fact":null},"on_fail":{"condition":"exhausted","damage":null,"fact":null}}]},
 {"key":"ring","title":"кольцо на дне","description":"","state":"","hidden":true,"approaches":[{"action":"interact","title":"достать кольцо","attribute":null,"skill":null,"difficulty":0,"on_success":{"state":"taken","reveal":null,"fact":"Кольцо с печатью Ордена."},"on_fail":null}]}]},
 "neighbors":[{"direction":"north","name":"Серая башня","terrain":"ruins","card":"Башня темнеет над крышами.","visible":true,"distant_view":""},{"direction":"east","name":"Рынок","terrain":"town","card":"Шум торговых рядов.","visible":false,"distant_view":"шум торговых рядов"},{"direction":"south","name":"Лишний","terrain":"road","card":"Не из задания.","visible":true,"distant_view":""}]`

func openingJSON(text string) string {
	return `{"text":"` + text + `",` + goodStart + `}`
}

// TestOpening_Valid: вход содержит мир, стартовую клетку, задания на соседей, модель персонажа и открытые данные
// персонажей; валидный ответ принимается с первого вызова, стартовый пакет проверен по заданиям.
func TestOpening_Valid(t *testing.T) {
	t.Parallel()
	var got model.LLMRequest
	svc := New(Func(func(_ context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		got = req
		return model.LLMResponse{JSON: []byte(openingJSON(goodOpening)), Usage: model.LLMUsage{Input: 7, Output: 3}}, nil
	}), slog.Default())
	res, err := svc.Opening(context.Background(), openingParams())
	if err != nil || res.Calls != 1 || res.Text != strings.TrimSpace(goodOpening) || res.TokensIn != 7 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if got.Role != model.RoleOpening || got.System != OpeningSystemPrompt() || string(got.JSONSchema) != string(OpeningSchema) {
		t.Fatalf("request = %+v", got)
	}
	for _, want := range []string{`"title":"Серая башня"`, `"genre":"Классическое фэнтези"`, `"mode":"balanced"`, `"start":{"location_id":1,"name":"Площадь"`, `"objects":["доска объявлений"]`, `"neighbors":[{"x":0,"y":1,"direction":"north"`, `"character_model":{"attributes":[{"key":"strength"`, `"nickname":"Элис"`, `"gender":"female"`, `"public_bio":"Кузнец"`} {
		if !strings.Contains(got.Input, want) {
			t.Fatalf("input without %s: %s", want, got.Input)
		}
	}
	if res.Start == nil || res.Start.Name != "Площадь у колодца" || res.Start.LocationID != 1 || len(res.Start.Objects) != 2 || !res.Start.Objects[1].HiddenUntil {
		t.Fatalf("start = %+v", res.Start)
	}
	if ap := res.Start.Objects[0].Approaches; len(ap) != 1 || string(ap[0].OnSuccess) != `{"reveal":"ring"}` || string(ap[0].OnFail) != `{"condition":"exhausted"}` || ap[0].Difficulty != 5 {
		t.Fatalf("approaches = %+v", ap)
	}
	if res.Neighbors == nil || len(res.Neighbors) != 2 || res.Neighbors[0].Name != "Серая башня" || res.Neighbors[0].X != 0 || res.Neighbors[0].Y != 1 || res.Neighbors[1].Terrain != "town" || res.Neighbors[0].Template {
		t.Fatalf("neighbors = %+v", res.Neighbors)
	}
	if res.Neighbors[0].Hidden || !res.Neighbors[1].Hidden || res.Neighbors[1].DistantView != "шум торговых рядов" {
		t.Fatalf("neighbor visibility = %+v", res.Neighbors)
	}
	// Лишний сосед не из задания отброшен с замечанием; дозапроса по пакету нет.
	if len(res.Issues) != 0 {
		t.Fatalf("issues = %v", res.Issues)
	}
}

// TestOpening_RetryAndIssues: цифры и непредставленный персонаж дают дозапрос с замечаниями; исправленный ответ принимается,
// слишком короткий после дозапроса — ErrOpening; ошибка провайдера пробрасывается; пустой пакет мира — шаблон соседей и
// объекты шаблона у стартовой клетки.
func TestOpening_RetryAndIssues(t *testing.T) {
	t.Parallel()
	answers := []string{openingJSON(strings.Repeat("Элис ждёт у башни уже 3 часа, и никто не приходит. ", 3)), openingJSON(goodOpening)}
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
		return model.LLMResponse{JSON: []byte(openingJSON("Коротко."))}, nil
	}), slog.Default())
	if res, err := short.Opening(context.Background(), openingParams()); !errors.Is(err, ErrOpening) || res.Calls != 2 {
		t.Fatalf("short = %+v, %v", res, err)
	}

	empty := New(Func(func(context.Context, model.LLMRequest) (model.LLMResponse, error) {
		return model.LLMResponse{JSON: []byte(`{"text":"` + goodOpening + `","start":{"location_id":1,"name":"","description":"","difficulty":0,"objects":[]},"neighbors":[]}`)}, nil
	}), slog.Default())
	if res, err := empty.Opening(context.Background(), openingParams()); err != nil || res.Start != nil || len(res.Neighbors) != 2 || !res.Neighbors[0].Template || res.Neighbors[1].Name != model.TemplateCardName || len(res.Issues) != 4 {
		t.Fatalf("empty package = %+v, %v", res, err)
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

// TestOpening_Lair (S22, ADR-0031): задание `generate.lair` уходит во вход; карточка логова принимается всегда скрытой с
// видом издалека; без имени — шаблон «Логово»; без задания поле ответа игнорируется.
func TestOpening_Lair(t *testing.T) {
	t.Parallel()
	withLair := func(lair string) string {
		return `{"text":"` + goodOpening + `",` + goodStart + `,"lair":` + lair + `}`
	}
	p := openingParams()
	p.Lair = &LairTask{X: 3, Y: -3, Bearing: "недалеко на юго-востоке", Faction: "evil", Title: "Гильдия"}
	var got model.LLMRequest
	svc := New(Func(func(_ context.Context, req model.LLMRequest) (model.LLMResponse, error) {
		got = req
		return model.LLMResponse{JSON: []byte(withLair(`{"name":"Чёрная мельница","terrain":"Ruins","card":"Мельница без крыльев на голом холме.","distant_view":"крыло мельницы над холмами"}`))}, nil
	}), slog.Default())
	res, err := svc.Opening(context.Background(), p)
	if err != nil || len(res.Issues) != 0 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if !strings.Contains(got.Input, `"lair":{"x":3,"y":-3,"bearing":"недалеко на юго-востоке","faction":"evil","title":"Гильдия"}`) {
		t.Fatalf("input without lair task: %s", got.Input)
	}
	if l := res.Lair; l == nil || l.X != 3 || l.Y != -3 || l.Name != "Чёрная мельница" || l.Terrain != "ruins" || !l.Hidden || l.DistantView != "крыло мельницы над холмами" || l.Template {
		t.Fatalf("lair = %+v", res.Lair)
	}
	// Без имени — шаблон с замечанием.
	empty := New(Func(func(context.Context, model.LLMRequest) (model.LLMResponse, error) {
		return model.LLMResponse{JSON: []byte(withLair(`{"name":"","terrain":"","card":"","distant_view":""}`))}, nil
	}), slog.Default())
	res, err = empty.Opening(context.Background(), p)
	if err != nil || res.Lair == nil || !res.Lair.Template || res.Lair.Name != model.TemplateLairName || !res.Lair.Hidden || res.Lair.DistantView != "" || len(res.Issues) != 1 {
		t.Fatalf("template lair = %+v, issues %v, %v", res.Lair, res.Issues, err)
	}
	// Без задания логова нет, даже если модель что-то вернула.
	res, err = svc.Opening(context.Background(), openingParams())
	if err != nil || res.Lair != nil {
		t.Fatalf("lair without task = %+v, %v", res.Lair, err)
	}
}
