//go:build integration

package postgres

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"taletogether/internal/model"
)

// newTestStore подключается к DATABASE_URL_TEST, применяет миграции и очищает таблицы.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_TEST")
	if dsn == "" {
		t.Skip("DATABASE_URL_TEST is not set")
	}
	ctx := context.Background()
	s, err := New(ctx, Settings{DSN: dsn}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `TRUNCATE telegram_updates, users`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return s
}

func TestMarkUpdate_Duplicate_NotFresh(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	fresh, err := s.MarkUpdate(ctx, 42)
	if err != nil || !fresh {
		t.Fatalf("first MarkUpdate = %v, %v; want true, nil", fresh, err)
	}
	fresh, err = s.MarkUpdate(ctx, 42)
	if err != nil || fresh {
		t.Fatalf("second MarkUpdate = %v, %v; want false, nil", fresh, err)
	}
}

func TestUpsertUser_Twice_OneRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := model.TelegramUser{ID: 7, Username: "alice", FirstName: "Alice", LanguageCode: "ru"}

	if err := s.UpsertUser(ctx, u); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	u.Username = ""
	u.FirstName = "Alisa"
	if err := s.UpsertUser(ctx, u); err != nil {
		t.Fatalf("UpsertUser again: %v", err)
	}

	var n int
	var username *string
	var firstName string
	err := s.pool.QueryRow(ctx, `SELECT count(*), min(username), min(first_name) FROM users WHERE telegram_id = 7`).Scan(&n, &username, &firstName)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if n != 1 || username != nil || firstName != "Alisa" {
		t.Fatalf("got n=%d username=%v first_name=%q; want 1, nil, Alisa", n, username, firstName)
	}
}
