package bot

import (
	"context"
	"testing"

	"taletogether/internal/model"
)

// fakeJournal собирает записи журнала чатов.
type fakeJournal struct{ recs []model.ChatLogRecord }

func (j *fakeJournal) Write(_ context.Context, rec model.ChatLogRecord) { j.recs = append(j.recs, rec) }

func TestHandle_Journal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	j := &fakeJournal{}
	h.svc.Journal = j
	h.addBot(owner)
	h.send(owner, groupChat, "/ping")
	// Повтор update: запись с пометкой дубля, сценарий не выполняется.
	in := model.Incoming{UpdateID: h.nextID, ChatID: groupChat, ChatType: model.ChatSupergroup, From: owner, Text: "/ping", Command: "ping"}
	if err := h.svc.Handle(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(j.recs) != 4 {
		t.Fatalf("records = %+v", j.recs)
	}
	if r := j.recs[0]; r.Kind != model.ChatLogIn || r.Event != "my_chat_member" || r.Text != string(model.BotMember) || r.Chat != groupChat || r.User == nil || r.User.Username != "owner" {
		t.Fatalf("chat member = %+v", r)
	}
	if r := j.recs[1]; r.Kind != model.ChatLogIn || r.Command != "ping" || r.Text != "/ping" || r.UpdateID != h.nextID || r.Duplicate || r.TS.IsZero() {
		t.Fatalf("in = %+v", r)
	}
	if r := j.recs[2]; r.Kind != model.ChatLogOut || r.Chat != groupChat || r.UpdateID != h.nextID || r.Text != h.tg.last(groupChat) || r.Error != "" {
		t.Fatalf("out = %+v", r)
	}
	if r := j.recs[3]; r.Kind != model.ChatLogIn || !r.Duplicate || r.UpdateID != h.nextID || len(h.tg.sent[groupChat]) != 1 {
		t.Fatalf("duplicate = %+v, sent %d", r, len(h.tg.sent[groupChat]))
	}
}
