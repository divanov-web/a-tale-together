package bot

import (
	"context"
	"strings"

	"taletogether/internal/model"
)

// cmdContext — где доступна команда.
type cmdContext uint8

// Контексты команд.
const (
	ctxPrivate cmdContext = 1 << iota
	ctxGroup
	ctxAny = ctxPrivate | ctxGroup
)

// command — запись реестра команд (docs/architecture/telegram-commands.md).
type command struct {
	name        string
	contexts    cmdContext
	description string
	run         func(ctx context.Context, req *request) error
	needsChat   bool // в группе нужен зарегистрированный чат (req.chat)
}

// allows сообщает, доступна ли команда в контексте сообщения.
func (c command) allows(in model.Incoming) bool {
	if in.IsPrivate() {
		return c.contexts&ctxPrivate != 0
	}
	return c.contexts&ctxGroup != 0
}

// hint — подсказка для команды в чужом контексте.
func (c command) hint() string {
	if c.contexts&ctxPrivate != 0 {
		return "Команда /" + c.name + " работает в личке с ботом."
	}
	return "Команда /" + c.name + " работает в групповом чате игры."
}

// registry — команды в порядке вывода в /help и меню.
func (s *Service) registry() []command {
	return []command{
		{name: "start", contexts: ctxPrivate, description: "открыть личку; с кодом — войти в игру", run: s.start},
		{name: "help", contexts: ctxAny, description: "список команд", run: s.help},
		{name: "me", contexts: ctxPrivate, description: "кто я: чаты и игры", run: s.me},
		{name: "ping", contexts: ctxAny, description: "проверка: версия и время сервера", run: s.ping},
		{name: "chat", contexts: ctxGroup, description: "владелец, модераторы, текущая игра", run: s.chatInfo, needsChat: true},
		{name: "chat_mod", contexts: ctxGroup, description: "add|remove @user — модераторы чата (владелец)", run: s.chatMod, needsChat: true},
		{name: "chat_owner", contexts: ctxGroup, description: "@user — передать владение чатом", run: s.chatOwner, needsChat: true},
		{name: "game_create", contexts: ctxGroup, description: "создать мир в этом чате", run: s.gameCreate, needsChat: true},
		{name: "game_status", contexts: ctxAny, description: "состояние игры", run: s.gameStatus, needsChat: true},
		{name: "game_set", contexts: ctxPrivate, description: "<параметр> <значение> — настройка игры", run: s.gameSet},
		{name: "join", contexts: ctxPrivate, description: "<код> — войти в игру", run: s.join},
		{name: "games", contexts: ctxPrivate, description: "[номер] — мои игры и выбор текущей", run: s.gamesList},
		{name: "character", contexts: ctxPrivate, description: "мой персонаж", run: s.character},
		{name: "character_create", contexts: ctxPrivate, description: "создать персонажа в текущей игре", run: s.characterCreate},
		{name: "character_bio", contexts: ctxPrivate, description: "<текст> — открытое описание до 100 символов", run: s.characterBio},
		{name: "character_secret", contexts: ctxPrivate, description: "<текст> — скрытое описание до 100 символов", run: s.characterSecret},
		{name: "cancel", contexts: ctxPrivate, description: "прервать диалог", run: s.cancel},
	}
}

func (s *Service) lookup(name string) (command, bool) {
	for _, c := range s.commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// Commands возвращает команды контекста для меню и /help.
func (s *Service) Commands(c cmdContext) []model.BotCommand {
	var out []model.BotCommand
	for _, cmd := range s.commands {
		if cmd.contexts&c != 0 && cmd.name != "start" {
			out = append(out, model.BotCommand{Name: cmd.name, Description: cmd.description})
		}
	}
	return out
}

// splitArgs делит аргументы команды на первое слово и остаток.
func splitArgs(args string) (first, rest string) {
	first, rest, _ = strings.Cut(strings.TrimSpace(args), " ")
	return strings.TrimSpace(first), strings.TrimSpace(rest)
}
