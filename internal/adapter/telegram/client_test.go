package telegram

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-telegram/bot"

	"taletogether/internal/model"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		err       error
		rejected  bool
		after     time.Duration
		transient bool
	}{
		{name: "forbidden", err: fmt.Errorf("%w, bot was blocked", bot.ErrorForbidden), rejected: true},
		{name: "bad_request", err: fmt.Errorf("%w, chat not found", bot.ErrorBadRequest), rejected: true},
		{name: "migrate", err: &bot.MigrateError{Message: "group upgraded", MigrateToChatID: -1001}, rejected: true},
		{name: "too_many", err: &bot.TooManyRequestsError{Message: "too many requests", RetryAfter: 7}, after: 7 * time.Second},
		{name: "too_many_without_retry_after", err: &bot.TooManyRequestsError{Message: "too many requests"}, after: time.Minute},
		{name: "network", err: errors.New("dial tcp: i/o timeout"), transient: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classify(tc.err)
			if errors.Is(got, model.ErrSendRejected) != tc.rejected {
				t.Fatalf("rejected = %v; want %v (%v)", !tc.rejected, tc.rejected, got)
			}
			var ra *model.RetryAfterError
			if errors.As(got, &ra) != (tc.after > 0) || (ra != nil && ra.After != tc.after) {
				t.Fatalf("retry after = %v; want %v", ra, tc.after)
			}
			if tc.transient && got != tc.err {
				t.Fatalf("transient error changed: %v", got)
			}
		})
	}
}
