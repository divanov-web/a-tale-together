package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

const outboxColumns = `id, game_id, round_id, telegram_chat_id, kind, part, text, keyboard, status, telegram_message_id, attempts, send_after, claimed_at, last_error, created_at, sent_at`

func scanOutbox(row pgx.Row) (model.OutboxMessage, error) {
	var m model.OutboxMessage
	var kb []byte
	err := row.Scan(&m.ID, &m.GameID, &m.RoundID, &m.TelegramChatID, &m.Kind, &m.Part, &m.Text, &kb, &m.Status, &m.TelegramMessageID,
		&m.Attempts, &m.SendAfter, &m.ClaimedAt, &m.LastError, &m.CreatedAt, &m.SentAt)
	if err != nil {
		return m, err
	}
	if len(kb) > 0 {
		if err := json.Unmarshal(kb, &m.Keyboard); err != nil {
			return m, fmt.Errorf("keyboard: %w", err)
		}
	}
	return m, nil
}

// keyboardJSON — клавиатура сообщения для столбца jsonb; nil — NULL.
func keyboardJSON(kb model.Keyboard) ([]byte, error) {
	if len(kb) == 0 {
		return nil, nil
	}
	return json.Marshal(kb)
}

// EnqueueOutbox ставит сообщение в очередь; повтор того же вида для раунда и чата молча пропускается (дедуп).
func (s *Store) EnqueueOutbox(ctx context.Context, m model.OutboxMessage) error {
	part := m.Part
	if part == 0 {
		part = 1
	}
	kb, err := keyboardJSON(m.Keyboard)
	if err != nil {
		return fmt.Errorf("postgres.EnqueueOutbox: %w", err)
	}
	const q = `INSERT INTO messages_outbox (game_id, round_id, telegram_chat_id, kind, part, text, keyboard, send_after)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT DO NOTHING`
	if _, err := s.db(ctx).Exec(ctx, q, m.GameID, m.RoundID, m.TelegramChatID, m.Kind, part, m.Text, kb, m.SendAfter); err != nil {
		return fmt.Errorf("postgres.EnqueueOutbox: %w", err)
	}
	return nil
}

// ClaimOutbox берёт до limit ожидающих сообщений под отправку (`FOR UPDATE SKIP LOCKED`): статус `sending`,
// попытка засчитана. Сообщения, застрявшие в `sending` дольше stale, помечаются `unknown` и повторно не отправляются.
func (s *Store) ClaimOutbox(ctx context.Context, now time.Time, stale time.Duration, limit int) ([]model.OutboxMessage, error) {
	const stuck = `UPDATE messages_outbox SET status = 'unknown', last_error = 'исход отправки неизвестен' WHERE status = 'sending' AND claimed_at < $1`
	if _, err := s.db(ctx).Exec(ctx, stuck, now.Add(-stale)); err != nil {
		return nil, fmt.Errorf("postgres.ClaimOutbox: stale: %w", err)
	}
	const q = `UPDATE messages_outbox SET status = 'sending', attempts = attempts + 1, claimed_at = $1
WHERE id IN (SELECT id FROM messages_outbox WHERE status = 'pending' AND send_after <= $1 ORDER BY id LIMIT $2 FOR UPDATE SKIP LOCKED)
RETURNING ` + outboxColumns
	rows, err := s.db(ctx).Query(ctx, q, now, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres.ClaimOutbox: %w", err)
	}
	defer rows.Close()
	var out []model.OutboxMessage
	for rows.Next() {
		m, err := scanOutbox(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ClaimOutbox: scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// FinishOutbox записывает исход попытки: статус, message_id, время следующей попытки, ошибку.
func (s *Store) FinishOutbox(ctx context.Context, m model.OutboxMessage) error {
	const q = `UPDATE messages_outbox SET status = $2, telegram_message_id = $3, send_after = $4, last_error = $5, sent_at = $6 WHERE id = $1`
	if _, err := s.db(ctx).Exec(ctx, q, m.ID, m.Status, m.TelegramMessageID, m.SendAfter, m.LastError, m.SentAt); err != nil {
		return fmt.Errorf("postgres.FinishOutbox: %w", err)
	}
	return nil
}

// ListOutbox возвращает сообщения игры по порядку создания (диагностика и тесты).
func (s *Store) ListOutbox(ctx context.Context, game model.GameID) ([]model.OutboxMessage, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+outboxColumns+` FROM messages_outbox WHERE game_id = $1 ORDER BY id`, game)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListOutbox: %w", err)
	}
	defer rows.Close()
	var out []model.OutboxMessage
	for rows.Next() {
		m, err := scanOutbox(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ListOutbox: scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
