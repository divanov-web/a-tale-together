package telegram

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

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

func TestCallback_IncomingAndMarkup(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	c := &Client{now: func() time.Time { return now }}
	u := &models.Update{ID: 7, CallbackQuery: &models.CallbackQuery{
		ID: "q1", From: models.User{ID: 42, Username: "alice", FirstName: "Alice"}, Data: "/act #3:1:well:0",
		Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: 15, Chat: models.Chat{ID: 42, Type: "private"}}},
	}}
	in, ok := c.callback(u)
	if !ok || in.UpdateID != 7 || in.ChatID != 42 || !in.IsPrivate() || in.From.ID != 42 || in.Command != "act" || in.Args != "#3:1:well:0" || in.Text != "/act #3:1:well:0" {
		t.Fatalf("callback = %+v, %v", in, ok)
	}
	if in.Callback == nil || in.Callback.ID != "q1" || in.Callback.MessageID != 15 || in.Callback.Step != "" || !in.ReceivedAt.Equal(now) {
		t.Fatalf("callback meta = %+v", in.Callback)
	}
	u.CallbackQuery.Data = "@faction 2"
	u.CallbackQuery.Message = models.MaybeInaccessibleMessage{InaccessibleMessage: &models.InaccessibleMessage{Chat: models.Chat{ID: 42, Type: "private"}, MessageID: 16}}
	in, ok = c.callback(u)
	if !ok || in.Command != "" || in.Text != "2" || in.Callback.Step != "faction" || in.Callback.MessageID != 16 {
		t.Fatalf("dialog callback = %+v, %v", in, ok)
	}
	u.CallbackQuery.Message = models.MaybeInaccessibleMessage{}
	if _, ok := c.callback(u); ok {
		t.Fatal("callback without message accepted")
	}

	kb := model.Keyboard{{{Text: "Да", Data: "/begin yes"}, {Text: "Нет", Data: "/begin no"}}, {{Text: "Гайд", URL: "https://example.org"}}}
	m := markup(kb)
	if len(m.InlineKeyboard) != 2 || len(m.InlineKeyboard[0]) != 2 || m.InlineKeyboard[0][1].CallbackData != "/begin no" || m.InlineKeyboard[1][0].URL != "https://example.org" {
		t.Fatalf("markup = %+v", m.InlineKeyboard)
	}
}

// TestReplyMarkup (ADR-0021): ряды подписей — постоянная сжатая клавиатура с подсказкой ввода, без рядов — снятие.
func TestReplyMarkup(t *testing.T) {
	t.Parallel()
	m, ok := replyMarkup(model.ReplyKeyboard{Rows: [][]string{{"Персонаж", "Знания"}, {"Квесты"}}, Placeholder: "Опишите действие…"}).(*models.ReplyKeyboardMarkup)
	if !ok || len(m.Keyboard) != 2 || m.Keyboard[0][1].Text != "Знания" || !m.IsPersistent || !m.ResizeKeyboard || m.InputFieldPlaceholder != "Опишите действие…" {
		t.Fatalf("reply markup = %+v", m)
	}
	if r, ok := replyMarkup(model.ReplyKeyboard{}).(*models.ReplyKeyboardRemove); !ok || !r.RemoveKeyboard {
		t.Fatalf("remove markup = %+v", r)
	}
}

// TestEntities (ADR-0029): смещения из рун переводятся в единицы UTF-16 (эмодзи — две), отрезок за границами
// текста пропускается, без разметки — nil.
func TestEntities(t *testing.T) {
	t.Parallel()
	text := model.FormattedText{Text: "😀 на север: Лес", Entities: []model.TextEntity{
		{Kind: model.EntityBold, Offset: 2, Length: 8},
		{Kind: model.EntityBold, Offset: 12, Length: 3},
		{Kind: model.EntityBold, Offset: 14, Length: 5},
	}}
	got := entities(text)
	want := []models.MessageEntity{{Type: models.MessageEntityTypeBold, Offset: 3, Length: 8}, {Type: models.MessageEntityTypeBold, Offset: 13, Length: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entities = %+v, want %+v", got, want)
	}
	if got := entities(model.Plain("текст")); got != nil {
		t.Fatalf("plain entities = %+v", got)
	}
}
