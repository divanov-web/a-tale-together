package bot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"taletogether/internal/adapter/postgres/fake"
	"taletogether/internal/app/chat"
	"taletogether/internal/app/game"
	"taletogether/internal/model"
)

// fakeTelegram записывает отправленные тексты по чатам; admins — кто админ в группах Telegram.
type fakeTelegram struct {
	sent   map[model.TelegramChatID][]string
	admins map[model.TelegramUserID]bool
	menu   [][]model.BotCommand
}

func (f *fakeTelegram) SendText(_ context.Context, chat model.TelegramChatID, text string) error {
	f.sent[chat] = append(f.sent[chat], text)
	return nil
}

func (f *fakeTelegram) IsChatAdmin(_ context.Context, _ model.TelegramChatID, user model.TelegramUserID) (bool, error) {
	return f.admins[user], nil
}

func (f *fakeTelegram) SetCommands(_ context.Context, private, group []model.BotCommand) error {
	f.menu = [][]model.BotCommand{private, group}
	return nil
}

// last — последний текст, отправленный в чат.
func (f *fakeTelegram) last(chat model.TelegramChatID) string {
	if len(f.sent[chat]) == 0 {
		return ""
	}
	return f.sent[chat][len(f.sent[chat])-1]
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// harness — собранный сервис с фейками и счётчиком update_id.
type harness struct {
	t      *testing.T
	svc    *Service
	store  *fake.Store
	tg     *fakeTelegram
	nextID int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clock := fixedClock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	store := fake.New(clock.Now)
	tg := &fakeTelegram{sent: map[model.TelegramChatID][]string{}, admins: map[model.TelegramUserID]bool{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	chats := chat.New(store, logger)
	games := game.New(store, chats, clock, logger)
	svc := New(Settings{Version: "v0.1", Env: "dev", BotUsername: "tale_bot"}, store, store, tg, chats, games, clock, logger)
	return &harness{t: t, svc: svc, store: store, tg: tg}
}

const groupChat model.TelegramChatID = -100500

var (
	owner  = model.TelegramUser{ID: 1, Username: "owner", FirstName: "Owner"}
	alice  = model.TelegramUser{ID: 2, Username: "alice", FirstName: "Alice"}
	bob    = model.TelegramUser{ID: 3, Username: "bob", FirstName: "Bob"}
	nobody = model.TelegramUser{ID: 4, Username: "nobody", FirstName: "Nobody"}
)

// addBot имитирует my_chat_member: пользователь добавил бота в группу.
func (h *harness) addBot(by model.TelegramUser) {
	h.t.Helper()
	h.nextID++
	err := h.svc.HandleChatMember(context.Background(), model.ChatMemberChange{
		UpdateID: h.nextID, ChatID: groupChat, ChatType: model.ChatSupergroup, ChatTitle: "Песочница", From: by, NewStatus: model.BotMember,
	})
	if err != nil {
		h.t.Fatalf("HandleChatMember: %v", err)
	}
}

// send отправляет сообщение от пользователя в чат и возвращает последний ответ бота в этот чат.
func (h *harness) send(from model.TelegramUser, chatID model.TelegramChatID, text string) string {
	h.t.Helper()
	h.nextID++
	in := model.Incoming{UpdateID: h.nextID, ChatID: chatID, ChatType: model.ChatPrivate, From: from, Text: text}
	if chatID < 0 {
		in.ChatType = model.ChatSupergroup
	}
	if strings.HasPrefix(text, "/") {
		cmd, args, _ := strings.Cut(text[1:], " ")
		in.Command, in.Args = strings.ToLower(cmd), strings.TrimSpace(args)
	}
	if err := h.svc.Handle(context.Background(), in); err != nil {
		h.t.Fatalf("Handle(%q): %v", text, err)
	}
	return h.tg.last(chatID)
}

// private — чат пользователя с ботом.
func private(u model.TelegramUser) model.TelegramChatID { return model.TelegramChatID(u.ID) }

// createGame — владелец добавляет бота и создаёт мир; возвращает код приглашения.
func (h *harness) createGame() string {
	h.t.Helper()
	h.addBot(owner)
	h.send(owner, private(owner), "/start")
	reply := h.send(owner, groupChat, "/game_create")
	if !strings.Contains(reply, "создан") {
		h.t.Fatalf("game_create reply = %q", reply)
	}
	c, err := h.store.GetChatByTelegramID(context.Background(), groupChat)
	if err != nil {
		h.t.Fatalf("chat not stored: %v", err)
	}
	g, err := h.store.UnfinishedGameOfChat(context.Background(), c.ID)
	if err != nil {
		h.t.Fatalf("game not stored: %v", err)
	}
	return g.JoinCode
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Fatalf("reply %q\nwant substring %q", got, w)
		}
	}
}

func TestHandle_Ping_DuplicateAndUnknown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if got := h.send(alice, private(alice), "/ping"); !strings.HasPrefix(got, "pong\nверсия: v0.1 (dev)\nвремя: 2026-10-10T12:00:00Z") {
		t.Fatalf("ping reply = %q", got)
	}
	dup := model.Incoming{UpdateID: h.nextID, ChatID: private(alice), ChatType: model.ChatPrivate, From: alice, Text: "/ping", Command: "ping"}
	if err := h.svc.Handle(context.Background(), dup); err != nil {
		t.Fatal(err)
	}
	if n := len(h.tg.sent[private(alice)]); n != 1 {
		t.Fatalf("duplicate update produced %d replies; want 1", n)
	}
	mustContain(t, h.send(alice, private(alice), "/foo"), "Неизвестная команда")
	h.send(alice, groupChat, "/foo")
	if len(h.tg.sent[groupChat]) != 0 {
		t.Fatalf("unknown command in group answered: %q", h.tg.sent[groupChat])
	}
}

func TestHandle_WrongContextHint(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mustContain(t, h.send(alice, groupChat, "/character"), "работает в личке")
	mustContain(t, h.send(alice, private(alice), "/game_create"), "работает в групповом чате")
}

func TestHandle_StoreError_Returned(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	wantErr := errors.New("db down")
	h.store.Err = wantErr
	err := h.svc.Handle(context.Background(), model.Incoming{UpdateID: 1, From: alice, Command: "ping"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle() error = %v; want wrapping %v", err, wantErr)
	}
}

func TestChat_RegistrationAndRights(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.addBot(owner)
	mustContain(t, h.send(alice, groupChat, "/chat"), "Владелец: @owner", "Модераторы: нет", "Игра: нет")

	mustContain(t, h.send(alice, groupChat, "/game_create"), "Нет прав")
	mustContain(t, h.send(alice, groupChat, "/chat_mod add @bob"), "Нет прав")
	mustContain(t, h.send(owner, groupChat, "/chat_mod add @bob"), "неизвестен боту")
	h.send(bob, private(bob), "/start")
	mustContain(t, h.send(owner, groupChat, "/chat_mod add @bob"), "@bob назначен модератором")
	mustContain(t, h.send(alice, groupChat, "/chat"), "Модераторы: @bob")
	mustContain(t, h.send(bob, groupChat, "/game_create"), "создан")
	mustContain(t, h.send(owner, groupChat, "/game_create"), "уже есть незавершённая игра")
	mustContain(t, h.send(owner, groupChat, "/chat_mod remove @bob"), "больше не модератор")
	mustContain(t, h.send(owner, groupChat, "/chat_owner @bob"), "Владелец чата теперь @bob")
	mustContain(t, h.send(owner, groupChat, "/chat_owner @alice"), "Нет прав")
}

func TestChat_RegisteredByAdminCommand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mustContain(t, h.send(alice, groupChat, "/chat"), "не зарегистрирован")
	h.tg.admins[owner.ID] = true
	mustContain(t, h.send(owner, groupChat, "/chat"), "Владелец: @owner")
}

func TestGame_CreateSetupAndStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	mustContain(t, h.tg.last(groupChat), "Ссылка для игроков: https://t.me/tale_bot?start=join_"+code, "отправлены администратору в личку")
	mustContain(t, h.tg.last(private(owner)), "Настройка мира", "Название мира")

	mustContain(t, h.send(owner, private(owner), "Хроники Серой башни"), "Длительность раунда")
	mustContain(t, h.send(owner, private(owner), "5"), "от 10 до 60", "Повторите ответ")
	mustContain(t, h.send(owner, private(owner), "20"), "Часовой пояс")
	mustContain(t, h.send(owner, private(owner), "-"), "Игровое окно")
	mustContain(t, h.send(owner, private(owner), "10:00-22:00"), "Дата окончания")
	mustContain(t, h.send(owner, private(owner), "2026-12-31"), "Число раундов")
	mustContain(t, h.send(owner, private(owner), "30"), "Лимит игроков")
	mustContain(t, h.send(owner, private(owner), "5"), "Лимит активных локаций")
	mustContain(t, h.send(owner, private(owner), "-"), "Кворум раунда", "Сейчас: 2")
	mustContain(t, h.send(owner, private(owner), "3"), "Фракция добра")
	mustContain(t, h.send(owner, private(owner), "Орден"), "Фракция зла")
	mustContain(t, h.send(owner, private(owner), "-"), "Нейтральная фракция")
	final := h.send(owner, private(owner), "Вольный люд")
	mustContain(t, final, "Настройка завершена", "Мир «Хроники Серой башни» — черновик", "Раунд: 20 мин", "окно: 10:00–22:00",
		"до 2026-12-31, 30 раундов", "кворум 3", "Игроки: 1 из 5", "Орден (good)", "Гильдия теней (evil)", "Вольный люд (neutral)")

	mustContain(t, h.send(alice, groupChat, "/status"), "Мир «Хроники Серой башни»")
	mustContain(t, h.send(owner, private(owner), "/game_set max_players 3"), "Сохранено")
	mustContain(t, h.send(owner, private(owner), "/game_set bogus 1"), "неизвестный параметр")
	mustContain(t, h.send(owner, private(owner), "/game_set min_answers 0"), "кворум раунда от 1 до 20")
	mustContain(t, h.send(owner, private(owner), "/game_set min_answers 1"), "Сохранено")
	mustContain(t, h.send(alice, groupChat, "/status"), "кворум 1", "Следующий раунд: нет — игра не запущена")
	mustContain(t, h.send(alice, groupChat, "/chat"), "Игра: «Хроники Серой башни» — черновик")
}

func TestGame_SetupViaDeepLink_WhenPrivateNotOpened(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.addBot(owner)
	reply := h.send(owner, groupChat, "/game_create")
	mustContain(t, reply, "Настройка в личке: откройте https://t.me/tale_bot?start=setup_")
	code := reply[strings.Index(reply, "setup_")+len("setup_") : strings.Index(reply, "setup_")+len("setup_")+8]
	mustContain(t, h.send(owner, private(owner), "/start setup_"+code), "Настройка мира")
	mustContain(t, h.send(alice, private(alice), "/start setup_"+code), "Нет прав")
}

// joinAndCreate проводит пользователя через вход и создание персонажа; answers — ответы по шагам.
func (h *harness) joinAndCreate(u model.TelegramUser, code string, answers ...string) string {
	h.t.Helper()
	reply := h.send(u, private(u), "/start join_"+code)
	mustContain(h.t, reply, "Выберите фракцию", "1. Орден Рассвета")
	for _, a := range answers {
		reply = h.send(u, private(u), a)
	}
	return reply
}

func TestCharacter_CreateAutoAndManual(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()

	card := h.joinAndCreate(alice, code, "1", "Элис", "воин", "1", "Высокая и шумная", "-")
	mustContain(t, card, "Персонаж создан", "Элис — Воин, фракция «Орден Рассвета»", "HP 8", "Сила 4, Ловкость 3, Разум 2, Воля 3",
		"Бой 3", "Меч, Щит", "Открытое описание: Высокая и шумная", "Скрытое описание: нет")

	card = h.joinAndCreate(bob, code, "Гильдия теней", "Боб", "2", "2", "2 4 3 3", "воровство 3 скрытность 2 бой 1", "-", "Боится воды")
	mustContain(t, card, "Боб — Плут, фракция «Гильдия теней»", "HP 6", "Воровство 3, Скрытность 2, Бой 1", "Кинжал, Отмычки", "Скрытое описание: Боится воды")

	mustContain(t, h.send(alice, private(alice), "/character"), "Элис — Воин")
	mustContain(t, h.send(alice, private(alice), "/character_bio Теперь тихая"), "сохранено")
	mustContain(t, h.send(alice, private(alice), "/character"), "Открытое описание: Теперь тихая")
	mustContain(t, h.send(alice, private(alice), "/character_create"), "уже есть")
	mustContain(t, h.send(alice, private(alice), "/start join_"+code), "Вы уже в игре")
	mustContain(t, h.send(alice, private(alice), "/me"), "@alice", "персонаж Элис", "Личка: открыта")
	mustContain(t, h.send(owner, groupChat, "/status"), "Игроки: 3 из 20", "персонажей: 2", "Орден Рассвета (good): 1", "Элис — warrior")
}

func TestCharacter_ValidationInDialog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	h.joinAndCreate(alice, code, "1", "Элис", "1", "1", "-", "-")

	reply := h.send(bob, private(bob), "/join "+code)
	mustContain(t, reply, "Выберите фракцию")
	mustContain(t, h.send(bob, private(bob), "9"), "выберите фракцию", "Повторите ответ")
	mustContain(t, h.send(bob, private(bob), "neutral"), "Никнейм")
	mustContain(t, h.send(bob, private(bob), "элис"), "Никнейм уже занят")
	mustContain(t, h.send(bob, private(bob), "Б"), "от 2 до 30")
	mustContain(t, h.send(bob, private(bob), "Боб"), "Архетип")
	mustContain(t, h.send(bob, private(bob), "бард"), "выберите архетип")
	mustContain(t, h.send(bob, private(bob), "маг"), "Распределение")
	mustContain(t, h.send(bob, private(bob), "2"), "Характеристики, сумма 12")
	mustContain(t, h.send(bob, private(bob), "4 4 4 4"), "сумма характеристик должна быть 12")
	mustContain(t, h.send(bob, private(bob), "2 2 4 4"), "Навыки: 6 очков")
	mustContain(t, h.send(bob, private(bob), "внимательность 4 убеждение 2"), "от 0 до 3")
	mustContain(t, h.send(bob, private(bob), "внимательность 3 убеждение 3"), "Открытое описание")
	mustContain(t, h.send(bob, private(bob), strings.Repeat("я", 101)), "до 100 символов")
	mustContain(t, h.send(bob, private(bob), "/cancel"), "Диалог прерван")
	mustContain(t, h.send(bob, private(bob), "/character"), "ещё нет", "/character_create")
	mustContain(t, h.send(bob, private(bob), "/character_create"), "Выберите фракцию")
}

func TestGame_JoinLimitsAndGames(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	mustContain(t, h.send(owner, private(owner), "/game_set max_players 2"), "Сохранено")
	mustContain(t, h.send(alice, private(alice), "/join "+code), "Выберите фракцию")
	mustContain(t, h.send(bob, private(bob), "/join "+code), "лимит игроков")
	mustContain(t, h.send(bob, private(bob), "/join NOPE"), "Игры нет")
	mustContain(t, h.send(nobody, private(nobody), "/games"), "не участвуете")
	mustContain(t, h.send(alice, private(alice), "/games"), "1. «Песочница» (черновик) — текущая")
	mustContain(t, h.send(nobody, private(nobody), "/character"), "Игры нет")
}

func TestHandle_RateLimitAndLength(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for i := 0; i < rateCapacity; i++ {
		h.send(alice, private(alice), "/ping")
	}
	h.send(alice, private(alice), "/ping")
	if n := len(h.tg.sent[private(alice)]); n != rateCapacity {
		t.Fatalf("replies = %d; want %d (rate limited)", n, rateCapacity)
	}
	mustContain(t, h.send(bob, private(bob), strings.Repeat("x", maxTextLen+1)), "Слишком длинное")
}

func TestCommands_MenuByContext(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.svc.RegisterMenu(context.Background()); err != nil {
		t.Fatal(err)
	}
	names := func(cmds []model.BotCommand) string {
		var out []string
		for _, c := range cmds {
			out = append(out, c.Name)
		}
		return fmt.Sprint(out)
	}
	privateMenu, groupMenu := names(h.tg.menu[0]), names(h.tg.menu[1])
	if !strings.Contains(privateMenu, "character") || strings.Contains(privateMenu, "game_create") || strings.Contains(privateMenu, "start") {
		t.Fatalf("private menu = %s", privateMenu)
	}
	if !strings.Contains(groupMenu, "game_create") || strings.Contains(groupMenu, "character") {
		t.Fatalf("group menu = %s", groupMenu)
	}
	mustContain(t, h.send(alice, private(alice), "/help"), "в личке", "/join", "/cancel")
	mustContain(t, h.send(alice, groupChat, "/help"), "в группе", "/game_create")
}
