package round

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
	"taletogether/internal/service/narrator"
)

const setupsJSON = `{"options":[
 {"title":"Гуси против молчания","world":"Ольховый Брод","summary":"Городок у реки, пропали гуси.","tone":"Ирония и суета ярмарки.","premise":"Гуси исчезли ночью, ярмарка открывается в полдень, и городу нужен виноватый.",
  "factions":[{"code":"good","title":"Гильдия ярмарки","description":"Торговцы.","tone":"деловые","goal":"Спасти ярмарку."},{"code":"evil","title":"Братство тихих","description":"Музыканты.","tone":"скрытные","goal":"Спрятать барда."}],
  "start":{"name":"Гусиные ряды","terrain":"village","description":"Пустые загоны и солома."}},
 {"title":"Соль и колокол","world":"Порт Косы","summary":"Корабль с зерном сел на мель.","tone":"Тревога и туман.","premise":"Зерно мокнет, прилив к ночи.",
  "factions":[{"code":"good","title":"Совет пристани","description":"Купцы.","tone":"резкие","goal":"Спасти груз."},{"code":"evil","title":"Люди маяка","description":"Смотрители.","tone":"упрямые","goal":"Оправдать смотрителя."}],
  "start":{"name":"Причал у маяка","terrain":"harbor","description":"Мокрые доски и колокол без языка."}}
]}`

// setupWorld — игра в `setup` с открытой личкой администратора и временем тика: как после `/create`.
func setupWorld(t *testing.T) *world {
	t.Helper()
	w := towerWorld(t)
	ctx := context.Background()
	if err := w.store.MarkStarted(ctx, w.game.CreatedByUserID); err != nil {
		t.Fatal(err)
	}
	w.game.Status = model.GameSetup
	now := w.clock.Now()
	w.game.NextRoundAt = &now
	if err := w.store.UpdateGame(ctx, w.game); err != nil {
		t.Fatal(err)
	}
	return w
}

// TestWorldgen_Tick (ADR-0028): тик игры `setup` делает один вызов `worldgen` без раунда с описанием жанра, сохраняет
// варианты у игры, снимает время тика и ставит администратору личное сообщение с кнопками выбора; повторный тик
// модель не зовёт.
func TestWorldgen_Tick(t *testing.T) {
	t.Parallel()
	w := setupWorld(t)
	llm := &fakeLLM{setups: setupsJSON}
	w.svc.LLM = llm
	w.tick(t, 0)
	calls := llm.byRole(model.RoleWorldgen)
	if len(calls) != 1 || calls[0].Round != nil || calls[0].PromptVersion != narrator.WorldgenPromptVersion {
		t.Fatalf("worldgen calls = %+v", calls)
	}
	if in := llm.inputs[0]; !strings.Contains(in, `"genre":{"title":"Классическое фэнтези"`) || !strings.Contains(in, `"code":"good","title":"Орден Рассвета"`) || !strings.Contains(in, `"start":{"name":"Рыночная площадь"`) || !strings.Contains(in, `"seed":"game-`) {
		t.Fatalf("worldgen input = %s", in)
	}
	g := w.game
	if g.Status != model.GameSetup || g.NextRoundAt != nil || g.WorldSetups == nil || g.WorldSetups.Attempts != 1 || len(g.WorldSetups.Options) != 2 || g.WorldSetups.ChosenNo != 0 || g.Setup() != nil {
		t.Fatalf("game after tick = %+v", g.WorldSetups)
	}
	msgs := w.outbox(t)
	if len(msgs) != 1 || msgs[0].Kind != model.OutboxPrivate || msgs[0].TelegramChatID != 1 || !strings.Contains(msgs[0].Text, "Варианты стартового мира «Мир»") || !strings.Contains(msgs[0].Text, "1. Гуси против молчания — «Ольховый Брод»") || !strings.Contains(msgs[0].Text, "/set scenario") {
		t.Fatalf("outbox = %+v", msgs)
	}
	kb := msgs[0].Keyboard
	if len(kb) != 3 || kb[0][0].Data != "@scenario 1" || kb[0][0].Text != "1. Гуси против молчания" || kb[1][0].Data != "@scenario 2" || kb[2][0].Data != "@scenario -" {
		t.Fatalf("keyboard = %+v", kb)
	}
	w.tick(t, time.Minute)
	if len(llm.byRole(model.RoleWorldgen)) != 1 || len(w.outbox(t)) != 1 {
		t.Fatal("second tick called the model again")
	}
}

// TestWorldgen_Fallback: сбой модели — попытка через минуту, после трёх попыток варианты пусты, администратор получает
// сообщение о шаблоне; без модели шаблон сразу и без сообщения.
func TestWorldgen_Fallback(t *testing.T) {
	t.Parallel()
	w := setupWorld(t)
	llm := &fakeLLM{}
	w.svc.LLM = llm
	w.tick(t, 0)
	if g := w.game; g.WorldSetups == nil || g.WorldSetups.Attempts != 1 || g.NextRoundAt == nil || !g.NextRoundAt.Equal(w.clock.Now().Add(time.Minute)) || !g.WorldSetups.Pending() {
		t.Fatalf("after first failure = %+v, next %v", w.game.WorldSetups, w.game.NextRoundAt)
	}
	w.tick(t, 30*time.Second)
	if len(llm.byRole(model.RoleWorldgen)) != 1 {
		t.Fatal("retry before its time")
	}
	w.tick(t, 30*time.Second)
	w.tick(t, time.Minute)
	g := w.game
	if len(llm.byRole(model.RoleWorldgen)) != 3 || g.NextRoundAt != nil || g.WorldSetups.Attempts != 3 || g.WorldSetups.Pending() || len(g.WorldSetups.Options) != 0 {
		t.Fatalf("after three failures = %+v, calls %d", g.WorldSetups, len(llm.byRole(model.RoleWorldgen)))
	}
	if msgs := w.outbox(t); len(msgs) != 1 || !strings.Contains(msgs[0].Text, "придумать не удалось") || msgs[0].Keyboard != nil {
		t.Fatalf("outbox = %+v", msgs)
	}

	w2 := setupWorld(t)
	w2.svc.LLM = nil
	w2.tick(t, 0)
	if g := w2.game; g.NextRoundAt != nil || g.WorldSetups == nil || g.WorldSetups.Pending() || len(w2.outbox(t)) != 0 {
		t.Fatalf("without llm = %+v", g.WorldSetups)
	}
}

// TestOpening_WithSetup: выбранный вариант даёт завязке тон и крючок `premise`; при сбое завязки стартовая сцена —
// крючок варианта, а цель фракции — из варианта, не из шаблона жанра.
func TestOpening_WithSetup(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	var setups model.WorldSetups
	if err := json.Unmarshal([]byte(setupsJSON), &setups); err != nil {
		t.Fatal(err)
	}
	setups.ChosenNo = 2
	w.game.WorldSetups = &setups
	if err := w.store.UpdateGame(ctx, w.game); err != nil {
		t.Fatal(err)
	}
	llm := &fakeLLM{opening: `{"text":"Над Миром висит туман. Элис, Боб и Кэт стоят на причале, и колокол без языка молчит над ними, пока зерно мокнет в трюме."}`}
	w.svc.LLM = llm
	w.begin(t)
	if in := llm.inputs[0]; !strings.Contains(in, `"tone":"Тревога и туман."`) || !strings.Contains(in, `"premise":"Зерно мокнет, прилив к ночи."`) {
		t.Fatalf("opening input = %s", in)
	}

	w2 := towerWorld(t)
	w2.game.WorldSetups = &setups
	if err := w2.store.UpdateGame(ctx, w2.game); err != nil {
		t.Fatal(err)
	}
	w2.svc.LLM = &fakeLLM{}
	g := w2.begin(t)
	if g.OpeningScene != "Зерно мокнет, прилив к ночи." {
		t.Fatalf("fallback opening = %q", g.OpeningScene)
	}
	goals, err := w2.store.ListFactionGoals(ctx, g.ID)
	if err != nil || len(goals) == 0 {
		t.Fatalf("goals = %+v, %v", goals, err)
	}
	for _, fg := range goals {
		if fg.Text != "Спасти груз." && fg.Text != "Оправдать смотрителя." {
			t.Fatalf("goal from template instead of setup: %+v", fg)
		}
	}
}
