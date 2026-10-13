package round

import (
	"context"
	"errors"
	"testing"
	"time"

	"taletogether/internal/model"
)

// TestSay_Validation — S21 (пункт 32, ADR-0027): реплика принимается по правилам заявки — текст, статус игры и
// персонажа, открытый раунд, одна на раунд, до 200 символов; место заявки не занимает.
func TestSay_Validation(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings())
	ctx := context.Background()
	if _, _, err := w.svc.Say(ctx, w.game, w.chars[0], "рано"); !errors.Is(err, ErrNotActive) {
		t.Fatalf("draft: %v", err)
	}
	w.begin(t)
	w.reload(t)
	for _, tc := range []struct {
		text string
		want error
	}{{"", ErrEmpty}, {"   ", ErrEmpty}, {string(make([]rune, 201)), ErrSpeechTooLong}} {
		if _, _, err := w.svc.Say(ctx, w.game, w.chars[0], tc.text); !errors.Is(err, tc.want) {
			t.Fatalf("Say(%q) = %v; want %v", tc.text, err, tc.want)
		}
	}
	dead := w.chars[1]
	dead.Status, dead.DeathRoundsLeft = model.CharacterDead, 2
	if _, _, err := w.svc.Say(ctx, w.game, dead, "бу"); !errors.Is(err, ErrDead) {
		t.Fatalf("dead: %v", err)
	}
	gone := w.chars[1]
	gone.Status = model.CharacterInactive
	if _, _, err := w.svc.Say(ctx, w.game, gone, "я вернулся"); !errors.Is(err, ErrAbsent) {
		t.Fatalf("inactive: %v", err)
	}
	r, sp, err := w.svc.Say(ctx, w.game, w.chars[0], "Я здесь, не стреляйте!")
	if err != nil || r.RoundNo != 1 || sp.Text != "Я здесь, не стреляйте!" || sp.CharacterID != w.chars[0].ID || sp.ID == 0 {
		t.Fatalf("Say = %+v, %+v, %v", r, sp, err)
	}
	if _, _, err := w.svc.Say(ctx, w.game, w.chars[0], "ещё раз"); !errors.Is(err, ErrAlreadySaid) {
		t.Fatalf("second speech: %v", err)
	}
	// Реплика не заявка: заявка после неё принимается, заявок в раунде одна.
	if _, _, err := w.svc.Submit(ctx, w.game, w.chars[0], "иду"); err != nil {
		t.Fatalf("submit after say: %v", err)
	}
	info, _ := w.svc.Info(ctx, w.game)
	if info.Submissions != 1 {
		t.Fatalf("submissions = %d; want 1", info.Submissions)
	}
	w.clock.t = w.clock.t.Add(10 * time.Minute)
	if _, _, err := w.svc.Say(ctx, w.game, w.chars[1], "поздно"); !errors.Is(err, ErrNoOpenRound) {
		t.Fatalf("after deadline: %v", err)
	}
}

// TestSay_InSnapshotAndCarried — S21: реплики кворума не дают и пропусков не снимают; при пропуске раунда переносятся в
// следующий; при заморозке попадают в снимок v5 и в журнал (`speech`, `frozen`); карточка `/round` их перечисляет.
func TestSay_InSnapshotAndCarried(t *testing.T) {
	t.Parallel()
	w := newWorld(t, model.DefaultGameSettings()) // кворум 2
	ctx := context.Background()
	j := &fakeJournal{}
	w.svc.Journal = j
	w.begin(t)
	w.reload(t)
	if _, _, err := w.svc.Say(ctx, w.game, w.chars[0], "Я здесь, не стреляйте!"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Say(ctx, w.game, w.chars[1], "Кто идёт?"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	r1, _ := w.store.RoundByNo(ctx, w.game.ID, 1)
	if r1.Status != model.RoundSkipped || r1.SkipReason != "заявок 0 из 2" {
		t.Fatalf("round 1 = %s %q; want skipped: speeches give no quorum", r1.Status, r1.SkipReason)
	}
	for _, c := range w.chars {
		got, _ := w.store.GetCharacter(ctx, w.game.ID, c.SeasonID, c.UserID)
		if got.Status != model.CharacterActive || got.MissedRounds != 0 {
			t.Fatalf("%s after skipped = %s/%d", c.Nickname, got.Status, got.MissedRounds)
		}
	}
	r2, _ := w.store.OpenRound(ctx, w.game.ID)
	if carried, _ := w.store.ListSpeeches(ctx, w.game.ID, r2.ID); len(carried) != 2 || carried[0].Text != "Я здесь, не стреляйте!" {
		t.Fatalf("speeches carried to round 2 = %+v", carried)
	}
	if left, _ := w.store.ListSpeeches(ctx, w.game.ID, r1.ID); len(left) != 0 {
		t.Fatalf("speeches left in round 1 = %+v", left)
	}
	if _, _, err := w.svc.Say(ctx, w.game, w.chars[0], "снова"); !errors.Is(err, ErrAlreadySaid) {
		t.Fatalf("carried speech blocks a second one: %v", err)
	}
	// Кворум заявками: заморозка несёт реплики в снимке; у Кэт без заявки пропуск, реплика его не снимает.
	for _, c := range w.chars[:2] {
		if _, _, err := w.svc.Submit(ctx, w.game, c, "жду"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := w.svc.Say(ctx, w.game, w.chars[2], "Подождите меня!"); err != nil {
		t.Fatal(err)
	}
	w.tick(t, 10*time.Minute)
	r2, _ = w.store.RoundByNo(ctx, w.game.ID, 2)
	if r2.Status != model.RoundPublished || r2.Snapshot == nil || r2.Snapshot.SchemaVersion != model.SnapshotSchemaVersion {
		t.Fatalf("round 2 = %+v", r2)
	}
	if sp := r2.Snapshot.Speeches; len(sp) != 3 || sp[0].CharacterID != w.chars[0].ID || sp[0].Text != "Я здесь, не стреляйте!" || sp[2].Text != "Подождите меня!" || sp[0].SpeechID == 0 {
		t.Fatalf("snapshot speeches = %+v", sp)
	}
	cat, _ := w.store.GetCharacter(ctx, w.game.ID, w.chars[2].SeasonID, w.chars[2].UserID)
	if cat.Status != model.CharacterWaiting || cat.MissedRounds != 1 {
		t.Fatalf("Кэт with speech only = %s/%d; want waiting/1", cat.Status, cat.MissedRounds)
	}
	d, err := w.svc.Detail(ctx, w.game, 2)
	if err != nil || len(d.Speeches) != 3 || d.Names[d.Speeches[1].CharacterID] != "Боб" {
		t.Fatalf("Detail = %+v, %v", d, err)
	}
	var speech, frozen int
	for _, rec := range j.recs {
		switch rec.Event {
		case "speech":
			speech++
			if data := rec.Data.(map[string]any); data["text"] == "" || data["character"] == "" {
				t.Fatalf("speech journal data = %+v", data)
			}
		case "frozen":
			frozen = len(rec.Data.(map[string]any)["speeches"].([]map[string]any))
		}
	}
	if speech != 3 || frozen != 3 {
		t.Fatalf("journal: speech events %d, speeches in frozen %d; want 3 and 3", speech, frozen)
	}
}
