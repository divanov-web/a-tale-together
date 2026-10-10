// Package chatlog — файловый журнал чатов для разбора ошибок ИИ (ADR-0012): папка на чат Telegram, файл на день,
// одна запись `model.ChatLogRecord` в строке JSONL. Ошибка записи уходит в лог и не ломает сценарий.
package chatlog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"taletogether/internal/model"
)

// Settings — расположение и срок хранения журнала.
type Settings struct {
	Dir       string        // корень журнала; пусто — журнал выключен
	Retention time.Duration // файлы старше удаляет Cleanup; 0 — не удалять
}

// Writer пишет записи в `Dir/<chat_id>/<ГГГГ-ММ-ДД>.jsonl`; потокобезопасен.
type Writer struct {
	dir       string
	retention time.Duration
	logger    *slog.Logger
	mu        sync.Mutex
}

// New создаёт писатель; папки создаются лениво при первой записи.
func New(s Settings, logger *slog.Logger) *Writer {
	return &Writer{dir: s.Dir, retention: s.Retention, logger: logger}
}

// Dir — корень журнала.
func (w *Writer) Dir() string { return w.dir }

// Write дописывает запись строкой в файл дня чата; при ошибке пишет предупреждение в лог.
func (w *Writer) Write(_ context.Context, rec model.ChatLogRecord) {
	if w == nil {
		return
	}
	if err := w.write(rec); err != nil {
		w.logger.Warn("chat log write failed", slog.Int64("chat_id", int64(rec.Chat)), slog.Any("err", err))
	}
}

func (w *Writer) write(rec model.ChatLogRecord) error {
	if rec.TS.IsZero() {
		rec.TS = time.Now()
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("chatlog.Write: marshal: %w", err)
	}
	dir := filepath.Join(w.dir, strconv.FormatInt(int64(rec.Chat), 10))
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("chatlog.Write: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, rec.TS.Format("2006-01-02")+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("chatlog.Write: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("chatlog.Write: %w", err)
	}
	return nil
}

// Cleanup удаляет файлы дней старше срока хранения во всех папках чатов; возвращает число удалённых.
// Без срока хранения или без папки журнала ничего не делает.
func (w *Writer) Cleanup(now time.Time) (int, error) {
	if w == nil || w.retention <= 0 {
		return 0, nil
	}
	chats, err := os.ReadDir(w.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("chatlog.Cleanup: %w", err)
	}
	cutoff := now.Add(-w.retention)
	removed := 0
	for _, c := range chats {
		if !c.IsDir() {
			continue
		}
		days, err := os.ReadDir(filepath.Join(w.dir, c.Name()))
		if err != nil {
			return removed, fmt.Errorf("chatlog.Cleanup: %w", err)
		}
		for _, d := range days {
			day, err := time.ParseInLocation("2006-01-02.jsonl", d.Name(), now.Location())
			if err != nil || !day.Add(24*time.Hour).Before(cutoff) {
				continue
			}
			if err := os.Remove(filepath.Join(w.dir, c.Name(), d.Name())); err != nil {
				return removed, fmt.Errorf("chatlog.Cleanup: %w", err)
			}
			removed++
		}
	}
	return removed, nil
}
