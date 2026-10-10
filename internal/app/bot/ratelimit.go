package bot

import (
	"sync"
	"time"

	"taletogether/internal/model"
)

// Лимиты входящих: сообщений на пользователя за окно и длина текста.
const (
	rateCapacity = 20
	rateWindow   = time.Minute
	maxTextLen   = 1000
)

// limiter — token bucket на пользователя: capacity сообщений, пополнение равномерно за window.
type limiter struct {
	mu       sync.Mutex
	clock    Clock
	capacity float64
	window   time.Duration
	buckets  map[model.TelegramUserID]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(clock Clock, capacity int, window time.Duration) *limiter {
	return &limiter{clock: clock, capacity: float64(capacity), window: window, buckets: map[model.TelegramUserID]*bucket{}}
}

// allow списывает токен; false — лимит исчерпан.
func (l *limiter) allow(user model.TelegramUserID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	b, ok := l.buckets[user]
	if !ok {
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[user] = b
	}
	b.tokens += now.Sub(b.last).Seconds() / l.window.Seconds() * l.capacity
	if b.tokens > l.capacity {
		b.tokens = l.capacity
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
