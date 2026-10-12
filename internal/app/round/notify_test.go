package round

import (
	"context"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
)

// TestNotifyOpen_PersonalOptions (B15): при открытии раунда персонажи без уведомления о заявке получают личное
// «раунд открыт» с вариантами по своей локации; подавшие заявку получают варианты следующего раунда в своём итоге;
// игроки без лички и выбывшие персонажи сообщений не получают.
func TestNotifyOpen_PersonalOptions(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	alice, bob, cat := w.chars[0], w.chars[1], w.chars[2]
	for _, c := range []model.Character{alice, bob} { // Кэт личку не открывала
		if err := w.store.MarkStarted(ctx, c.UserID); err != nil {
			t.Fatal(err)
		}
	}
	byKind := func(kind model.OutboxKind, round model.RoundID) map[model.TelegramChatID]string {
		out := map[model.TelegramChatID]string{}
		for _, m := range w.outbox(t) {
			if m.Kind == kind && m.RoundID != nil && *m.RoundID == round {
				out[m.TelegramChatID] = m.Text
			}
		}
		return out
	}
	chatOf := func(c model.Character) model.TelegramChatID {
		u, _ := w.store.GetUser(ctx, c.UserID)
		return u.PrivateChat()
	}
	if _, err := w.svc.Begin(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	w.reload(t)
	r1, _ := w.store.OpenRound(ctx, w.game.ID)
	opens := byKind(model.OutboxPrivateOpen, r1.ID)
	want := "«Мир» — раунд 1 открыт\n\nЗаявки до 12:10: напишите, что делает персонаж, или /act <текст>. Одна заявка на раунд.\n\nВарианты для вас (Рыночная площадь):\n• разобрать приписку (Разум/внимательность): параметр 6, сложность 5, шанс 60%\n• идти на север: Зал Серой башни\n• идти на юг: Неизведанные земли\n• идти на запад: Неизведанные земли\n• идти на восток: Неизведанные земли"
	if len(opens) != 2 || opens[chatOf(alice)] != want || opens[chatOf(bob)] != want {
		t.Fatalf("round 1 personal opens = %+v", opens)
	}
	// Раунд 1: Элис уходит в зал, Боб осматривает доску; в итогах варианты раунда 2 по новой локации.
	if _, _, err := w.svc.Submit(ctx, w.game, alice, "идти к башне"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Submit(ctx, w.game, bob, "осмотреть доску"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	notices := byKind(model.OutboxPrivate, r1.ID)
	if len(notices) != 2 {
		t.Fatalf("notices = %+v", notices)
	}
	// В зале свои объекты не описаны: варианты Элис — шаблонный «след» этапа квеста (пункт 28, рассказчик недоступен)
	// и переходы (пункт 27), в том числе к шаблонным соседям; уведомление называет размещённый этап.
	if text := notices[chatOf(alice)]; !strings.Contains(text, "\n• «След гонца»: этап «Найти след» — здесь, в «Зал Серой башни»\n\nВарианты на раунд 2 (Зал Серой башни):\n• разобраться на месте (без броска)\n• идти на юг: Рыночная площадь\n• идти на запад: Неизведанные земли\n• идти на восток: Неизведанные земли\n\nСледующая заявка") {
		t.Fatalf("alice notice = %q", text)
	}
	if text := notices[chatOf(bob)]; !strings.Contains(text, "\n\nВарианты на раунд 2 (Рыночная площадь):\n• разобрать приписку (Разум/внимательность): параметр 6, сложность 5, шанс 60%\n• идти на север: Зал Серой башни\n• идти на юг: Неизведанные земли\n• идти на запад: Неизведанные земли\n• идти на восток: Неизведанные земли\n\nСледующая заявка") {
		t.Fatalf("bob notice = %q", text)
	}
	r2, _ := w.store.OpenRound(ctx, w.game.ID)
	if opens := byKind(model.OutboxPrivateOpen, r2.ID); len(opens) != 0 { // подавшие уведомлены итогом, Кэт без лички
		t.Fatalf("round 2 personal opens = %+v", opens)
	}
	// Раунд 2: Кэт открыла личку, Элис выбыла; заявку подаёт только Боб.
	if err := w.store.MarkStarted(ctx, cat.UserID); err != nil {
		t.Fatal(err)
	}
	if err := w.store.UpdateCharacterActivity(ctx, w.game.ID, alice.ID, model.CharacterInactive, 4); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Submit(ctx, w.game, bob, "ждать"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	r3, _ := w.store.OpenRound(ctx, w.game.ID)
	opens = byKind(model.OutboxPrivateOpen, r3.ID)
	if len(opens) != 1 || !strings.HasPrefix(opens[chatOf(cat)], "«Мир» — раунд 3 открыт\n\nЗаявки до 12:30") || !strings.Contains(opens[chatOf(cat)], "Варианты для вас (Рыночная площадь)") {
		t.Fatalf("round 3 personal opens = %+v", opens)
	}
}
