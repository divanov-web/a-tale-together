package telegram

import (
	"strings"

	"github.com/go-telegram/bot/models"
)

// parseCommand выделяет команду из текста по entity bot_command в начале сообщения.
// Команда возвращается без слэша и без суффикса `@username`; чужая команда `/cmd@other_bot` в группе игнорируется.
func parseCommand(text string, entities []models.MessageEntity, botUsername string) (command, args string) {
	for _, e := range entities {
		if e.Type != models.MessageEntityTypeBotCommand || e.Offset != 0 {
			continue
		}
		runes := []rune(text)
		if e.Length > len(runes) {
			return "", ""
		}
		raw := string(runes[1:e.Length]) // без ведущего "/"
		name, mention, _ := strings.Cut(raw, "@")
		if mention != "" && !strings.EqualFold(mention, botUsername) {
			return "", ""
		}
		return strings.ToLower(name), strings.TrimSpace(string(runes[e.Length:]))
	}
	return "", ""
}
