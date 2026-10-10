package delivery

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"taletogether/internal/adapter/postgres/fake"
	"taletogether/internal/model"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

// sender — фейковый Telegram: падает, пока fail > 0.
type sender struct {
	fail int
	sent []string
}

func (s *sender) Send(_ context.Context, _ model.TelegramChatID, text string) (int64, error) {
	if s.fail > 0 {
		s.fail--
		return 0, errors.New("network")
	}
	s.sent = append(s.sent, text)
	return int64(100 + len(s.sent)), nil
}

func TestDeliver_RetryThenSent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	store := fake.New(c.Now)
	snd := &sender{fail: 1}
	svc := New(store, snd, c, Settings{MaxAttempts: 2}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := store.EnqueueOutbox(ctx, model.OutboxMessage{GameID: 1, TelegramChatID: -100, Kind: model.OutboxSystem, Text: "привет", SendAfter: c.t}); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.Deliver(ctx); err != nil || n != 0 {
		t.Fatalf("first pass = %d, %v; want retry", n, err)
	}
	msgs, _ := store.ListOutbox(ctx, 1)
	if msgs[0].Status != model.OutboxPending || msgs[0].Attempts != 1 || !msgs[0].SendAfter.Equal(c.t.Add(5*time.Second)) || msgs[0].LastError != "network" {
		t.Fatalf("after failure = %+v", msgs[0])
	}
	if n, _ := svc.Deliver(ctx); n != 0 {
		t.Fatal("sent before send_after")
	}
	c.t = c.t.Add(5 * time.Second)
	if n, err := svc.Deliver(ctx); err != nil || n != 1 {
		t.Fatalf("second pass = %d, %v", n, err)
	}
	msgs, _ = store.ListOutbox(ctx, 1)
	if msgs[0].Status != model.OutboxSent || msgs[0].TelegramMessageID == nil || *msgs[0].TelegramMessageID != 101 || msgs[0].SentAt == nil {
		t.Fatalf("after success = %+v", msgs[0])
	}
	if n, _ := svc.Deliver(ctx); n != 0 || len(snd.sent) != 1 {
		t.Fatalf("message sent twice: %v", snd.sent)
	}
}

func TestDeliver_FailedAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	store := fake.New(c.Now)
	snd := &sender{fail: 10}
	svc := New(store, snd, c, Settings{MaxAttempts: 2}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_ = store.EnqueueOutbox(ctx, model.OutboxMessage{GameID: 1, TelegramChatID: -100, Kind: model.OutboxSystem, Text: "x", SendAfter: c.t})
	for i := 0; i < 2; i++ {
		if _, err := svc.Deliver(ctx); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(time.Minute)
	}
	msgs, _ := store.ListOutbox(ctx, 1)
	if msgs[0].Status != model.OutboxFailed || msgs[0].Attempts != 2 {
		t.Fatalf("after max attempts = %+v", msgs[0])
	}
}

func TestBackoff(t *testing.T) {
	t.Parallel()
	for attempt, want := range map[int]time.Duration{1: 5 * time.Second, 2: 10 * time.Second, 3: 20 * time.Second, 10: 5 * time.Minute} {
		if got := backoff(attempt); got != want {
			t.Errorf("backoff(%d) = %v; want %v", attempt, got, want)
		}
	}
}

// fakeJournal собирает записи журнала чатов.
type fakeJournal struct{ recs []model.ChatLogRecord }

func (j *fakeJournal) Write(_ context.Context, rec model.ChatLogRecord) { j.recs = append(j.recs, rec) }

func TestDeliver_Journal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	store := fake.New(c.Now)
	snd := &sender{fail: 1}
	svc := New(store, snd, c, Settings{MaxAttempts: 2}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	j := &fakeJournal{}
	svc.Journal = j
	round := model.RoundID(7)
	if err := store.EnqueueOutbox(ctx, model.OutboxMessage{GameID: 1, RoundID: &round, TelegramChatID: -100, Kind: model.OutboxChronicle, Part: 2, Text: "хроника", SendAfter: c.t}); err != nil {
		t.Fatal(err)
	}
	_, _ = svc.Deliver(ctx)
	c.t = c.t.Add(5 * time.Second)
	_, _ = svc.Deliver(ctx)
	if len(j.recs) != 2 {
		t.Fatalf("records = %+v", j.recs)
	}
	if r := j.recs[0]; r.Kind != model.ChatLogOut || r.Chat != -100 || r.GameID != 1 || r.RoundID != 7 || r.OutboxKind != model.OutboxChronicle || r.Part != 2 || r.Attempt != 1 || r.Error != "network" || r.MessageID != 0 || r.Text != "хроника" {
		t.Fatalf("failed attempt = %+v", r)
	}
	if r := j.recs[1]; r.Attempt != 2 || r.Error != "" || r.MessageID != 101 || r.OutboxID == 0 || !r.TS.Equal(c.t) {
		t.Fatalf("sent = %+v", r)
	}
}
