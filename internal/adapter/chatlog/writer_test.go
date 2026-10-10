package chatlog

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"taletogether/internal/model"
)

func TestWrite_FilePerChatAndDay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := New(Settings{Dir: dir}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	day1 := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	w.Write(ctx, model.ChatLogRecord{TS: day1, Kind: model.ChatLogIn, Chat: -100, Text: "первая\nстрока и вторая", User: &model.ChatLogUser{ID: 7, Username: "bob"}})
	w.Write(ctx, model.ChatLogRecord{TS: day1.Add(time.Hour), Kind: model.ChatLogOut, Chat: -100, Text: "ответ", OutboxID: 5, Part: 2, MessageID: 42})
	w.Write(ctx, model.ChatLogRecord{TS: day1.Add(24 * time.Hour), Kind: model.ChatLogRound, Chat: -100, Event: "opened", RoundNo: 3, Data: map[string]any{"deadline": "13:00"}})
	w.Write(ctx, model.ChatLogRecord{TS: day1, Kind: model.ChatLogLLM, Chat: 7, LLM: &model.ChatLogCall{Role: model.RoleNarrator, System: "s", Input: "i", Output: "o"}})

	raw, err := os.ReadFile(filepath.Join(dir, "-100", "2026-10-10.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d: %q", len(lines), raw)
	}
	var rec model.ChatLogRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Kind != model.ChatLogIn || rec.Text != "первая\nстрока и вторая" || rec.User == nil || rec.User.Username != "bob" || !rec.TS.Equal(day1) {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(lines[1], `"outbox_id":5`) || !strings.Contains(lines[1], `"telegram_message_id":42`) || strings.Contains(lines[1], `"user"`) {
		t.Fatalf("out line = %s", lines[1])
	}
	for _, p := range []string{filepath.Join("-100", "2026-10-11.jsonl"), filepath.Join("7", "2026-10-10.jsonl")} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
}

func TestWrite_ErrorDoesNotPanic(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w := New(Settings{Dir: file}, slog.New(slog.NewTextHandler(io.Discard, nil))) // корень — файл: MkdirAll падает
	w.Write(context.Background(), model.ChatLogRecord{Kind: model.ChatLogIn, Chat: 1, Text: "x"})
	var nilWriter *Writer
	nilWriter.Write(context.Background(), model.ChatLogRecord{Kind: model.ChatLogIn, Chat: 1})
}

func TestCleanup_RemovesOldDays(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := New(Settings{Dir: dir, Retention: 3 * 24 * time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, d := range []int{-10, -4, -3, -1, 0} {
		w.Write(ctx, model.ChatLogRecord{TS: now.AddDate(0, 0, d), Kind: model.ChatLogIn, Chat: -100, Text: "x"})
	}
	w.Write(ctx, model.ChatLogRecord{TS: now.AddDate(0, 0, -30), Kind: model.ChatLogIn, Chat: 5, Text: "x"})
	if err := os.WriteFile(filepath.Join(dir, "-100", "notes.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := w.Cleanup(now)
	if err != nil || n != 3 {
		t.Fatalf("removed = %d, %v", n, err)
	}
	left, _ := os.ReadDir(filepath.Join(dir, "-100"))
	names := make([]string, 0, len(left))
	for _, e := range left {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, ","); got != "2026-10-07.jsonl,2026-10-09.jsonl,2026-10-10.jsonl,notes.txt" {
		t.Fatalf("left = %s", got)
	}
	if n, err := New(Settings{Dir: filepath.Join(dir, "missing"), Retention: time.Hour}, nil).Cleanup(now); err != nil || n != 0 {
		t.Fatalf("missing dir: %d, %v", n, err)
	}
}
