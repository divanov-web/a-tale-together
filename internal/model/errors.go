package model

import (
	"errors"
	"fmt"
	"time"
)

// ErrNotFound — запись не найдена в хранилище; проверяется через errors.Is.
var ErrNotFound = errors.New("not found")

// ErrSendRejected — Telegram отверг сообщение навсегда (чат не найден, бот заблокирован или удалён из группы,
// некорректный запрос): повтор бесполезен, воркер доставки сразу ставит `failed` (ADR-0013).
var ErrSendRejected = errors.New("send rejected")

// RetryAfterError — Telegram просит подождать (429 Too Many Requests); воркер доставки откладывает отправку на After.
type RetryAfterError struct {
	After time.Duration
	Err   error
}

func (e *RetryAfterError) Error() string {
	return fmt.Sprintf("retry after %s: %v", e.After, e.Err)
}

// Unwrap возвращает исходную ошибку библиотеки.
func (e *RetryAfterError) Unwrap() error { return e.Err }
