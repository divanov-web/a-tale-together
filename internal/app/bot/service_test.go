package bot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
)

type fakeStore struct {
	seen  map[int64]bool
	users []model.TelegramUser
	err   error
}

func (f *fakeStore) MarkUpdate(_ context.Context, id int64) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.seen[id] {
		return false, nil
	}
	f.seen[id] = true
	return true, nil
}

func (f *fakeStore) UpsertUser(_ context.Context, u model.TelegramUser) error {
	f.users = append(f.users, u)
	return nil
}

type fakeSender struct {
	sent []string
}

func (f *fakeSender) SendText(_ context.Context, _ model.ChatID, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func newService(store *fakeStore, sender *fakeSender) *Service {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	clock := fixedClock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	return New(Settings{Version: "v0.1", Env: "dev"}, store, sender, clock, logger)
}

func TestHandle(t *testing.T) {
	t.Parallel()
	user := model.TelegramUser{ID: 7, FirstName: "Alice"}
	ping := model.Incoming{UpdateID: 1, ChatID: -100, ChatType: model.ChatGroup, From: user, Text: "/ping", Command: "ping"}

	tests := []struct {
		name      string
		incoming  []model.Incoming
		wantSent  []string
		wantUsers int
	}{
		{
			name:      "ping replies with version and time",
			incoming:  []model.Incoming{ping},
			wantSent:  []string{"pong\nверсия: v0.1 (dev)\nвремя: 2026-10-10T12:00:00Z"},
			wantUsers: 1,
		},
		{
			name:      "duplicate update handled once",
			incoming:  []model.Incoming{ping, ping},
			wantSent:  []string{"pong\nверсия: v0.1 (dev)\nвремя: 2026-10-10T12:00:00Z"},
			wantUsers: 1,
		},
		{
			name:      "unknown command ignored but user recorded",
			incoming:  []model.Incoming{{UpdateID: 2, ChatID: 7, ChatType: model.ChatPrivate, From: user, Text: "/foo", Command: "foo"}},
			wantUsers: 1,
		},
		{
			name:     "plain text without sender ignored",
			incoming: []model.Incoming{{UpdateID: 3, ChatID: -100, ChatType: model.ChatGroup, Text: "hello"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := &fakeStore{seen: map[int64]bool{}}
			sender := &fakeSender{}
			svc := newService(store, sender)
			for _, in := range tt.incoming {
				if err := svc.Handle(context.Background(), in); err != nil {
					t.Fatalf("Handle(%d): %v", in.UpdateID, err)
				}
			}
			if strings.Join(sender.sent, "|") != strings.Join(tt.wantSent, "|") {
				t.Fatalf("sent = %q; want %q", sender.sent, tt.wantSent)
			}
			if len(store.users) != tt.wantUsers {
				t.Fatalf("users recorded = %d; want %d", len(store.users), tt.wantUsers)
			}
		})
	}
}

func TestHandle_StoreError_Returned(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("db down")
	svc := newService(&fakeStore{err: wantErr}, &fakeSender{})
	err := svc.Handle(context.Background(), model.Incoming{UpdateID: 1, Command: "ping"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle() error = %v; want wrapping %v", err, wantErr)
	}
}
