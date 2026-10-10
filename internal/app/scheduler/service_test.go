package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type counter struct {
	n    atomic.Int32
	done chan struct{}
}

func (c *counter) Tick(context.Context, time.Time) error {
	if c.n.Add(1) == 3 {
		close(c.done)
	}
	return errors.New("tick error is logged, not fatal")
}

type clock struct{}

func (clock) Now() time.Time { return time.Now() }

func TestRun_TicksUntilCancelled(t *testing.T) {
	t.Parallel()
	c := &counter{done: make(chan struct{})}
	svc := New(c, clock{}, time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not tick 3 times")
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Run returned %v", err)
	}
}
