package round

import (
	"context"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
)

// partiesOf — активные группы игры и персонажи по никнейму.
func partiesOf(t *testing.T, w *world) ([]model.Party, map[string]model.Character) {
	t.Helper()
	ctx := context.Background()
	season, _ := w.store.ActiveSeason(ctx, w.game.ID)
	all, err := w.store.ListParties(ctx, w.game.ID, season.ID)
	if err != nil {
		t.Fatal(err)
	}
	var active []model.Party
	for _, p := range all {
		if p.Status == model.PartyActive {
			active = append(active, p)
		}
	}
	chars, _ := w.store.ListCharacters(ctx, w.game.ID, season.ID)
	byNick := map[string]model.Character{}
	for _, c := range chars {
		byNick[c.Nickname] = c
	}
	return active, byNick
}

// TestBegin_DefaultParty (пункт 19): при старте все персонажи в одной группе с именем из шаблона жанра; лидер — первый
// созданный (у администратора персонажа нет), локация группы — стартовая; повторный /begin после сброса собирает заново.
func TestBegin_DefaultParty(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	w.begin(t)
	parties, chars := partiesOf(t, w)
	if len(parties) != 1 || parties[0].Name != "Ночной дозор" || parties[0].LeaderCharacterID == nil || *parties[0].LeaderCharacterID != w.chars[0].ID || parties[0].LocationID == nil {
		t.Fatalf("default party = %+v", parties)
	}
	for nick, c := range chars {
		if c.PartyID == nil || *c.PartyID != parties[0].ID {
			t.Fatalf("%s without default party: %+v", nick, c)
		}
	}
	w.reload(t)
	if _, err := w.svc.Reset(ctx, w.game, 1); err != nil {
		t.Fatal(err)
	}
	if parties, chars := partiesOf(t, w); len(parties) != 0 || chars["Элис"].PartyID != nil {
		t.Fatalf("after reset: parties %+v, alice %+v", parties, chars["Элис"])
	}
	w.reload(t)
	w.begin(t)
	if parties, _ := partiesOf(t, w); len(parties) != 1 {
		t.Fatalf("after second begin: %+v", parties)
	}
}

// TestRound_PartySplitAndLeader (S14 в сервисе): лидер уходит один — отделяется новой группой с зарезервированным
// идентификатором, группа по умолчанию получает нового лидера и остаётся на площади; записи персонажей и групп
// согласованы с состоянием; хроника группы подписана именем; снимок хранит группы и резерв.
func TestRound_PartySplitAndLeader(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	w.begin(t)
	w.reload(t)
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "идти к башне"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[1], "ждать"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	info, _ := w.svc.Info(ctx, w.game)
	snap := info.Last.Snapshot
	if len(snap.Parties) != 1 || snap.MaxParties != 4 || len(snap.NewPartyIDs) != 3 || len(snap.PartyNames) == 0 || snap.SchemaVersion != model.SnapshotSchemaVersion {
		t.Fatalf("snapshot parties = %+v, new ids %v", snap.Parties, snap.NewPartyIDs)
	}
	parties, chars := partiesOf(t, w)
	if len(parties) != 2 || parties[1].ID != snap.NewPartyIDs[0] || parties[1].Name != "Вольные клинки" || *parties[1].LeaderCharacterID != w.chars[0].ID {
		t.Fatalf("parties after split = %+v", parties)
	}
	if *parties[0].LeaderCharacterID != w.chars[1].ID || *parties[0].LocationID != *chars["Боб"].LocationID {
		t.Fatalf("default party after leader left = %+v", parties[0])
	}
	if *chars["Элис"].PartyID != parties[1].ID || *chars["Боб"].PartyID != parties[0].ID || *chars["Кэт"].PartyID != parties[0].ID {
		t.Fatalf("characters after split: %+v", chars)
	}
	// Пункт 27: за событиями движка (ожидание, разделение, пара перехода, открытие клетки) идут две шаблонные
	// карточки соседей зала; пункт 28: шаблонный этап квеста в зале (объект и привязка); воспроизведение сравнивает
	// только события движка.
	events, _ := w.store.ListEvents(ctx, w.game.ID, info.Last.ID)
	if len(events) != 9 || events[1].Type != model.EventPartySplit || events[4].Type != model.EventCellDiscovered || events[6].Type != model.EventCellGenerated || events[8].Type != model.EventQuestStagePlaced {
		t.Fatalf("events = %+v", events)
	}
	// Воспроизведение раунда с зарезервированным id даёт те же события.
	if n, same, err := w.svc.Replay(ctx, w.game, 1, 1); err != nil || !same || n != 5 {
		t.Fatalf("replay = %d, %v, %v", n, same, err)
	}
}

// TestRound_PartyHeld: при лимите в одну группу расхождение удерживается — группа идёт за лидером, заявка на другое
// направление получает исход «удержан» с объяснением в личном уведомлении.
func TestRound_PartyHeld(t *testing.T) {
	t.Parallel()
	w := towerWorld(t)
	ctx := context.Background()
	w.game.Settings.MaxParties = 1
	if err := w.store.UpdateGame(ctx, w.game); err != nil {
		t.Fatal(err)
	}
	if err := w.store.MarkStarted(ctx, w.chars[1].UserID); err != nil { // личка Боба открыта: уведомление о заявке придёт
		t.Fatal(err)
	}
	w.begin(t)
	w.reload(t)
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "идти к башне"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[1], "осмотреть доску"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	parties, chars := partiesOf(t, w)
	if len(parties) != 1 || len(w.outbox(t)) == 0 {
		t.Fatalf("parties = %+v", parties)
	}
	for nick, c := range chars {
		if *c.LocationID != *chars["Элис"].LocationID {
			t.Fatalf("%s not with leader: %+v", nick, c)
		}
	}
	var chronicle, bobNotice string
	for _, m := range w.outbox(t) {
		if m.Kind == model.OutboxChronicle {
			chronicle = m.Text
		}
		if m.Kind == model.OutboxPrivate && m.TelegramChatID == model.TelegramChatID(11) {
			bobNotice = m.Text
		}
	}
	if !strings.Contains(chronicle, "группа держится вместе и уходит из «Рыночная площадь» в «Зал Серой башни»; Боб (@Боб) хотел в «Рыночная площадь», Кэт (@Кэт) хотел в «Рыночная площадь»") {
		t.Fatalf("chronicle = %q", chronicle)
	}
	// Осмотр Боба разрешён уже в зале: доски там нет — отказ с перечнем объектов, но заявка не потеряна.
	if !strings.Contains(bobNotice, "Заявка «осмотреть доску»: отклонена — в «Зал Серой башни» нет объекта") {
		t.Fatalf("bob notice = %q", bobNotice)
	}
	if lead, _ := partiesOf(t, w); *lead[0].LocationID != *chars["Элис"].LocationID {
		t.Fatalf("party location = %+v", lead[0])
	}
}
