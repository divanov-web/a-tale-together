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
	groupDesc   string // описание для меню группы, если отличается (команда с разным действием по контексту)
	run         func(ctx context.Context, req *request) error
	needsChat   bool // в группе нужен зарегистрированный чат (req.chat)
	adminOnly   bool // в меню группы только владельцу и модераторам чата (B20); в /help видна всем
	hidden      bool // ни в одном меню Telegram; в /help видна
}

// describe — описание команды для контекста.
func (c command) describe(ctx cmdContext) string {
	if ctx == ctxGroup && c.groupDesc != "" {
		return c.groupDesc
	}
	return c.description
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
	cmds := []command{
		{name: "start", contexts: ctxPrivate, description: "открыть личку; с кодом — войти в игру", run: s.start},
		{name: "help", contexts: ctxAny, description: "список команд", run: s.help},
		{name: "me", contexts: ctxPrivate, description: "кто я: чаты и игры", run: s.me},
		{name: "ping", contexts: ctxAny, description: "проверка: версия и время сервера", run: s.ping, hidden: true},
		{name: "chat", contexts: ctxGroup, description: "владелец, модераторы, текущая игра", run: s.chatInfo, needsChat: true, adminOnly: true},
		{name: "mod", contexts: ctxGroup, description: "add|remove @user — модераторы чата (владелец)", run: s.chatMod, needsChat: true, adminOnly: true},
		{name: "owner", contexts: ctxGroup, description: "@user — передать владение чатом", run: s.chatOwner, needsChat: true, adminOnly: true},
		{name: "create", contexts: ctxAny, description: "создать персонажа в текущей игре", groupDesc: "создать мир в этом чате", run: s.create, needsChat: true, adminOnly: true},
		{name: "begin", contexts: ctxGroup, description: "начать кампанию: стартовая сцена и первый раунд", run: s.gameBegin, needsChat: true, adminOnly: true},
		{name: "pause", contexts: ctxGroup, description: "пауза и возобновление игры", run: s.gamePause, needsChat: true, adminOnly: true},
		{name: "finish", contexts: ctxGroup, description: "завершить игру: последний раунд и финальная хроника", run: s.gameFinish, needsChat: true, adminOnly: true},
		{name: "status", contexts: ctxAny, description: "состояние игры, раунд и причина простоя", run: s.gameStatus, needsChat: true},
		{name: "act", contexts: ctxAny, description: "<текст> — заявка на текущий раунд", groupDesc: "<текст> — открытая заявка на текущий раунд", run: s.act, needsChat: true},
		{name: "scout", contexts: ctxAny, description: "разведка: что в соседних клетках", run: s.scout, needsChat: true},
		{name: "set", contexts: ctxPrivate, description: "<параметр> <значение> — настройка игры", run: s.gameSet},
		{name: "join", contexts: ctxPrivate, description: "<код> — войти в игру", run: s.join},
		{name: "games", contexts: ctxPrivate, description: "[номер] — мои игры и выбор текущей", run: s.gamesList},
		{name: "character", contexts: ctxPrivate, description: "мой персонаж", run: s.character},
		{name: "party", contexts: ctxPrivate, description: "моя группа и остальные группы", run: s.party},
		{name: "quests", contexts: ctxPrivate, description: "цель и квесты моей фракции", run: s.quests},
		{name: "bio", contexts: ctxPrivate, description: "описания персонажа; с текстом — открытое описание до 100 символов", run: s.characterBio},
		{name: "secret", contexts: ctxPrivate, description: "<текст> — скрытое описание персонажа до 100 символов", run: s.characterSecret},
		{name: "knowledge", contexts: ctxPrivate, description: "все знания моего персонажа", run: s.characterKnowledge},
		{name: "cancel", contexts: ctxPrivate, description: "прервать диалог", run: s.cancel},
		{name: "llm", contexts: ctxPrivate, description: "профили LLM (владелец бота): use|add|test", run: s.llmCommand, hidden: true},
	}
	if s.settings.Env == "dev" {
		cmds = append(cmds, command{name: "next", contexts: ctxGroup, description: "закрыть раунд и открыть следующий (только dev)", run: s.roundNext, needsChat: true, adminOnly: true},
			command{name: "replay", contexts: ctxGroup, description: "<номер> — воспроизвести раунд по снимку и seed (только dev)", run: s.roundReplay, needsChat: true, adminOnly: true},
			command{name: "round", contexts: ctxGroup, description: "[номер] — карточка раунда: заявки, итоги, попытки, метрики (только dev)", run: s.roundDetail, needsChat: true, adminOnly: true},
			command{name: "reset", contexts: ctxGroup, description: "сбросить игру в черновик, персонажи остаются (только dev)", run: s.gameReset, needsChat: true, adminOnly: true})
	}
	return cmds
}

func (s *Service) lookup(name string) (command, bool) {
	for _, c := range s.commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// Commands — все команды контекста для /help (без /start: его подставляет сам Telegram).
func (s *Service) Commands(c cmdContext) []model.BotCommand {
	var out []model.BotCommand
	for _, cmd := range s.commands {
		if cmd.contexts&c != 0 && cmd.name != "start" {
			out = append(out, model.BotCommand{Name: cmd.name, Description: cmd.describe(c)})
		}
	}
	return out
}

// Menu — команды контекста для меню Telegram (B20): без скрытых; в группе административные (`adminOnly`) только
// при admin — это меню владельца и модераторов чата, остальным остаются команды игры здесь и сейчас.
func (s *Service) Menu(c cmdContext, admin bool) []model.BotCommand {
	var out []model.BotCommand
	for _, cmd := range s.commands {
		if cmd.contexts&c == 0 || cmd.name == "start" || cmd.hidden {
			continue
		}
		if c == ctxGroup && cmd.adminOnly && !admin {
			continue
		}
		out = append(out, model.BotCommand{Name: cmd.name, Description: cmd.describe(c)})
	}
	return out
}

// splitArgs делит аргументы команды на первое слово и остаток.
func splitArgs(args string) (first, rest string) {
	first, rest, _ = strings.Cut(strings.TrimSpace(args), " ")
	return strings.TrimSpace(first), strings.TrimSpace(rest)
}
