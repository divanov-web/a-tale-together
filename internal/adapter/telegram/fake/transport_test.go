package fake

import (
	"context"
	"testing"
	"time"

	"taletogether/internal/model"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		text, command, args string
	}{
		{"/act иду на север", "act", "иду на север"},
		{"/Status", "status", ""},
		{"/act@tale_bot текст", "act", "текст"},
		{"/act@other_bot текст", "", ""},
		{"просто текст", "", ""},
		{"/", "", ""},
		{"/при вет", "", ""},
	}
	for _, c := range cases {
		cmd, args := parseCommand(c.text, "tale_bot")
		if cmd != c.command || args != c.args {
			t.Errorf("parseCommand(%q) = %q, %q; ожидалось %q, %q", c.text, cmd, args, c.command, c.args)
		}
	}
}

// TestTransport_Mailboxes: сообщения ложатся в ящик чата с растущими номерами, Messages отдаёт только новые,
// нажатие кнопки разбирается в команду и шаг диалога как в боевом клиенте.
func TestTransport_Mailboxes(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	tr := New("tale_bot", func() time.Time { return now })
	ctx := context.Background()
	const chat model.TelegramChatID = 42
	user := model.TelegramUser{ID: 42, Username: "alice"}
	if err := tr.SendText(ctx, chat, model.Plain("раз")); err != nil {
		t.Fatal(err)
	}
	id, err := tr.Send(ctx, chat, model.Plain("два"), model.Keyboard{{{Text: "Разведка", Data: model.CallbackCommand("scout", "")}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.SendReplyKeyboard(ctx, chat, model.Plain("три"), model.ReplyKeyboard{Rows: [][]string{{"Персонаж"}}}); err != nil {
		t.Fatal(err)
	}
	all := tr.Messages(chat, 0)
	if len(all) != 3 || all[0].ID != 1 || all[1].ID != id || all[2].Reply == nil || all[2].Reply.Rows[0][0] != "Персонаж" {
		t.Fatalf("ящик: %+v", all)
	}
	if tail := tr.Messages(chat, id); len(tail) != 1 || tail[0].Text.Text != "три" {
		t.Fatalf("после %d: %+v", id, tail)
	}
	if got := tr.Messages(7, 0); len(got) != 0 {
		t.Fatalf("чужой чат не пуст: %+v", got)
	}

	in := tr.Press(chat, model.ChatPrivate, user, id, all[1].Keyboard[0][0].Data)
	if in.Command != "scout" || in.Callback == nil || in.Callback.MessageID != id || in.Callback.Step != "" || in.UpdateID != 1 {
		t.Fatalf("нажатие команды: %+v", in)
	}
	step := tr.Press(chat, model.ChatPrivate, user, id, model.CallbackAnswer("gender", "1"))
	if step.Command != "" || step.Text != "1" || step.Callback.Step != "gender" || step.UpdateID != 2 {
		t.Fatalf("нажатие ответа диалога: %+v", step)
	}
	text := tr.Text(chat, model.ChatPrivate, user, "/act иду")
	if text.Command != "act" || text.Args != "иду" || text.UpdateID != 3 || text.ReceivedAt != now || text.Callback != nil {
		t.Fatalf("текст: %+v", text)
	}
	tr.SetUpdateBase(1000)
	if next := tr.Text(chat, model.ChatPrivate, user, "ещё"); next.UpdateID != 1001 {
		t.Fatalf("update после SetUpdateBase: %d", next.UpdateID)
	}
	if err := tr.ClearKeyboard(ctx, chat, id); err != nil || !tr.Cleared(id) || tr.Cleared(1) {
		t.Fatalf("снятие кнопок: err=%v cleared=%v", err, tr.Cleared(id))
	}
}

// TestTransport_ChatAPI: права по SetAdmin, ссылка-заглушка, меню по областям, my_chat_member с названием группы.
func TestTransport_ChatAPI(t *testing.T) {
	tr := New("tale_bot", nil)
	ctx := context.Background()
	const group model.TelegramChatID = -100
	owner := model.TelegramUser{ID: 1, Username: "owner"}
	if ok, _ := tr.IsChatAdmin(ctx, group, owner.ID); ok {
		t.Fatal("админ до SetAdmin")
	}
	tr.SetAdmin(group, owner.ID)
	if ok, _ := tr.IsChatAdmin(ctx, group, owner.ID); !ok {
		t.Fatal("админ после SetAdmin")
	}
	if link, err := tr.CreateInviteLink(ctx, group); err != nil || link != "https://t.me/+sim100" {
		t.Fatalf("ссылка: %q %v", link, err)
	}
	if err := tr.SetCommands(ctx, []model.BotCommand{{Name: "me"}}, []model.BotCommand{{Name: "status"}}); err != nil {
		t.Fatal(err)
	}
	if err := tr.SetMemberCommands(ctx, group, owner.ID, []model.BotCommand{{Name: "begin"}}); err != nil {
		t.Fatal(err)
	}
	if tr.Commands("private")[0].Name != "me" || tr.Commands("group")[0].Name != "status" || tr.Commands("member:-100:1")[0].Name != "begin" {
		t.Fatalf("меню: %+v", tr.commands)
	}
	if err := tr.SetMemberCommands(ctx, group, owner.ID, nil); err != nil || tr.Commands("member:-100:1") != nil {
		t.Fatal("меню участника не снято")
	}
	tr.SetTitle(group, "Песочница")
	ch := tr.ChatMember(group, model.ChatSupergroup, owner, model.BotAdmin)
	if ch.ChatTitle != "Песочница" || ch.NewStatus != model.BotAdmin || ch.UpdateID != 1 || ch.From.ID != owner.ID {
		t.Fatalf("my_chat_member: %+v", ch)
	}
}
