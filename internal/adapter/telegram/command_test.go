package telegram

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestParseCommand(t *testing.T) {
	t.Parallel()
	cmd := func(length int) []models.MessageEntity {
		return []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: length}}
	}
	tests := []struct {
		name     string
		text     string
		entities []models.MessageEntity
		wantCmd  string
		wantArgs string
	}{
		{name: "plain command", text: "/ping", entities: cmd(5), wantCmd: "ping"},
		{name: "command with args", text: "/act иду на север", entities: cmd(4), wantCmd: "act", wantArgs: "иду на север"},
		{name: "command addressed to this bot", text: "/ping@tale_bot", entities: cmd(14), wantCmd: "ping"},
		{name: "command addressed to other bot", text: "/ping@other_bot", entities: cmd(15)},
		{name: "command case insensitive", text: "/PING@Tale_Bot", entities: cmd(14), wantCmd: "ping"},
		{name: "no entities", text: "/ping"},
		{name: "command not at start", text: "see /ping", entities: []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 4, Length: 5}}},
		{name: "plain text", text: "привет"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotCmd, gotArgs := parseCommand(tt.text, tt.entities, "tale_bot")
			if gotCmd != tt.wantCmd || gotArgs != tt.wantArgs {
				t.Fatalf("parseCommand() = %q, %q; want %q, %q", gotCmd, gotArgs, tt.wantCmd, tt.wantArgs)
			}
		})
	}
}
