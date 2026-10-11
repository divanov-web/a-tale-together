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

	llmfake "taletogether/internal/adapter/llm/fake"
	"taletogether/internal/adapter/postgres/fake"
	"taletogether/internal/app/chat"
	"taletogether/internal/app/game"
	applm "taletogether/internal/app/llm"
	"taletogether/internal/app/round"
	"taletogether/internal/model"
)

// fakeTelegram записывает отправленные тексты по чатам; admins — кто админ в группах Telegram.
type fakeTelegram struct {
	sent      map[model.TelegramChatID][]string
	keyboards map[model.TelegramChatID][]model.Keyboard // клавиатура каждого отправленного сообщения; nil — без кнопок
	cleared   []int64                                   // сообщения, с которых сняты кнопки (ADR-0015)
	admins    map[model.TelegramUserID]bool
	menu      [][]model.BotCommand
	member    map[memberScope][]model.BotCommand // меню участников чатов (B20); nil — снято
}

// memberScope — область меню «участник чата».
type memberScope struct {
	chat model.TelegramChatID
	user model.TelegramUserID
}

func (f *fakeTelegram) SendText(_ context.Context, chat model.TelegramChatID, text string) error {
	return f.SendKeyboard(context.Background(), chat, text, nil)
}

func (f *fakeTelegram) SendKeyboard(_ context.Context, chat model.TelegramChatID, text string, kb model.Keyboard) error {
	f.sent[chat] = append(f.sent[chat], text)
	if f.keyboards == nil {
		f.keyboards = map[model.TelegramChatID][]model.Keyboard{}
	}
	f.keyboards[chat] = append(f.keyboards[chat], kb)
	return nil
}

func (f *fakeTelegram) ClearKeyboard(_ context.Context, _ model.TelegramChatID, messageID int64) error {
	f.cleared = append(f.cleared, messageID)
	return nil
}

// lastKeyboard — клавиатура последнего сообщения в чат; nil, если кнопок не было.
func (f *fakeTelegram) lastKeyboard(chat model.TelegramChatID) model.Keyboard {
	kbs := f.keyboards[chat]
	if len(kbs) == 0 {
		return nil
	}
	return kbs[len(kbs)-1]
}

// buttonData — данные всех кнопок клавиатуры по порядку.
func buttonData(kb model.Keyboard) []string {
	var out []string
	for _, row := range kb {
		for _, b := range row {
			out = append(out, b.Data)
		}
	}
	return out
}

func (f *fakeTelegram) IsChatAdmin(_ context.Context, _ model.TelegramChatID, user model.TelegramUserID) (bool, error) {
	return f.admins[user], nil
}

func (f *fakeTelegram) SetCommands(_ context.Context, private, group []model.BotCommand) error {
	f.menu = [][]model.BotCommand{private, group}
	return nil
}

func (f *fakeTelegram) SetMemberCommands(_ context.Context, chat model.TelegramChatID, user model.TelegramUserID, cmds []model.BotCommand) error {
	if f.member == nil {
		f.member = map[memberScope][]model.BotCommand{}
	}
	f.member[memberScope{chat, user}] = cmds
	return nil
}

// memberMenu — имена команд меню участника чата; nil, если меню снято или не ставилось.
func (f *fakeTelegram) memberMenu(chat model.TelegramChatID, user model.TelegramUserID) []string {
	cmds, ok := f.member[memberScope{chat, user}]
	if !ok || cmds == nil {
		return nil
	}
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	return names
}

// last — последний текст, отправленный в чат.
func (f *fakeTelegram) last(chat model.TelegramChatID) string {
	if len(f.sent[chat]) == 0 {
		return ""
	}
	return f.sent[chat][len(f.sent[chat])-1]
}

// fixedClock — управляемое время теста; advance сдвигает его вперёд.
type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// harness — собранный сервис с фейками и счётчиком update_id.
type harness struct {
	t      *testing.T
	svc    *Service
	store  *fake.Store
	tg     *fakeTelegram
	clock  *fixedClock
	rounds *round.Service
	llm    *llmfake.Client
	nextID int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clock := &fixedClock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	store := fake.New(clock.Now)
	tg := &fakeTelegram{sent: map[model.TelegramChatID][]string{}, admins: map[model.TelegramUserID]bool{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	chats := chat.New(store, logger)
	games := game.New(store, chats, clock, logger)
	rounds := round.New(store, games, clock, logger)
	rounds.AnnounceSkipped = true
	fakeLLM := llmfake.New()
	llm := applm.New(store, map[string]applm.Client{model.ProviderFake: fakeLLM, model.ProviderClaudeCLI: llmfake.New()}, clock, logger)
	svc := New(Settings{Version: "v0.1", Env: "dev", BotUsername: "tale_bot", OwnerID: owner.ID}, store, store, tg, chats, games, rounds, llm, clock, logger)
	return &harness{t: t, svc: svc, store: store, tg: tg, clock: clock, rounds: rounds, llm: fakeLLM}
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

// press имитирует нажатие кнопки с данными data на сообщении messageID в чате и возвращает последний ответ бота.
func (h *harness) press(from model.TelegramUser, chatID model.TelegramChatID, messageID int64, data string) string {
	h.t.Helper()
	h.nextID++
	command, args, step, text := model.ParseCallbackData(data)
	in := model.Incoming{UpdateID: h.nextID, ChatID: chatID, ChatType: model.ChatPrivate, From: from, Text: text, Command: command, Args: args,
		Callback: &model.Callback{ID: fmt.Sprint("q", h.nextID), MessageID: messageID, Step: step}}
	if chatID < 0 {
		in.ChatType = model.ChatSupergroup
	}
	if err := h.svc.Handle(context.Background(), in); err != nil {
		h.t.Fatalf("Handle(callback %q): %v", data, err)
	}
	return h.tg.last(chatID)
}

// clearedLast — снята ли клавиатура с сообщения messageID последним действием.
func (h *harness) clearedLast(messageID int64) bool {
	n := len(h.tg.cleared)
	return n > 0 && h.tg.cleared[n-1] == messageID
}

// private — чат пользователя с ботом.
func private(u model.TelegramUser) model.TelegramChatID { return model.TelegramChatID(u.ID) }

// createGame — владелец добавляет бота и создаёт мир; возвращает код приглашения.
func (h *harness) createGame() string {
	h.t.Helper()
	h.addBot(owner)
	h.send(owner, private(owner), "/start")
	reply := h.send(owner, groupChat, "/create")
	if !strings.Contains(reply, "создан") {
		h.t.Fatalf("create reply = %q", reply)
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
	mustContain(t, h.send(alice, private(alice), "/mod add @bob"), "работает в групповом чате")
	mustContain(t, h.send(alice, private(alice), "/create"), "Игры нет") // /create в личке — персонаж
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

	mustContain(t, h.send(alice, groupChat, "/create"), "Нет прав")
	mustContain(t, h.send(alice, groupChat, "/mod add @bob"), "Нет прав")
	mustContain(t, h.send(owner, groupChat, "/mod add @bob"), "неизвестен боту")
	h.send(bob, private(bob), "/start")
	mustContain(t, h.send(owner, groupChat, "/mod add @bob"), "@bob назначен модератором")
	mustContain(t, h.send(alice, groupChat, "/chat"), "Модераторы: @bob")
	mustContain(t, h.send(bob, groupChat, "/create"), "создан")
	mustContain(t, h.send(owner, groupChat, "/create"), "уже есть незавершённая игра")
	mustContain(t, h.send(owner, groupChat, "/mod remove @bob"), "больше не модератор")
	mustContain(t, h.send(owner, groupChat, "/owner @bob"), "Владелец чата теперь @bob")
	mustContain(t, h.send(owner, groupChat, "/owner @alice"), "Нет прав")
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
	mustContain(t, h.send(owner, private(owner), "10:00-22:00"), "Число раундов")
	mustContain(t, h.send(owner, private(owner), "30"), "Кворум раунда", "Сейчас: 2")
	mustContain(t, h.send(owner, private(owner), "3"), "Фракция добра")
	mustContain(t, h.send(owner, private(owner), "Орден"), "Фракция зла")
	final := h.send(owner, private(owner), "-")
	mustContain(t, final, "Настройка завершена", "Мир «Хроники Серой башни» — черновик", "Раунд: 20 мин", "окно: 10:00–22:00",
		"Границы: 30 раундов", "Кворум: 3", "Игроки: 1\n", "Орден (good)", "Гильдия теней (evil)")

	mustContain(t, h.send(alice, groupChat, "/status"), "Мир «Хроники Серой башни»")
	mustContain(t, h.send(owner, private(owner), "/set ends_at 2026-12-31"), "Сохранено")
	mustContain(t, h.send(alice, groupChat, "/status"), "до 2026-12-31, 30 раундов")
	mustContain(t, h.send(owner, private(owner), "/set bogus 1"), "неизвестный параметр")
	mustContain(t, h.send(owner, private(owner), "/set min_answers 0"), "кворум раунда от 1 до 20")
	mustContain(t, h.send(owner, private(owner), "/set min_answers 1"), "Сохранено")
	mustContain(t, h.send(alice, groupChat, "/status"), "Кворум: 1", "Следующий раунд: игра не запущена")
	mustContain(t, h.send(alice, groupChat, "/chat"), "Игра: «Хроники Серой башни» — черновик")
}

func TestGame_SetupViaDeepLink_WhenPrivateNotOpened(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.addBot(owner)
	reply := h.send(owner, groupChat, "/create")
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

	card := h.joinAndCreate(alice, code, "1", "Элис", "2", "воин", "1", "Высокая и шумная", "-")
	mustContain(t, card, "Персонаж создан", "Элис — Воин\nПол: женский\nФракция: «Орден Рассвета»", "HP: 9", "Сила 5, Ловкость 3, Разум 2, Воля 4",
		"Атлетика 3, Бой 3", "Меч, Щит", "Открытое описание: Высокая и шумная", "Скрытое описание: нет")

	card = h.joinAndCreate(bob, code, "Гильдия теней", "Боб", "1", "2", "2", "2 5 4 3", "воровство 3 скрытность 2 бой 1", "-", "Боится воды")
	mustContain(t, card, "Боб — Плут\nПол: мужской\nФракция: «Гильдия теней»", "HP: 6", "Бой 1, Воровство 3, Скрытность 2", "Кинжал, Отмычки", "Скрытое описание: Боится воды")

	mustContain(t, h.send(alice, private(alice), "/character"), "Элис — Воин")
	mustContain(t, h.send(alice, private(alice), "/bio Теперь тихая"), "сохранено")
	mustContain(t, h.send(alice, private(alice), "/character"), "Открытое описание: Теперь тихая")
	mustContain(t, h.send(alice, private(alice), "/create"), "уже есть")
	mustContain(t, h.send(alice, private(alice), "/start join_"+code), "Вы уже в игре")
	mustContain(t, h.send(alice, private(alice), "/me"), "@alice", "персонаж Элис", "Личка: открыта")
	mustContain(t, h.send(owner, groupChat, "/status"), "Игроки: 3\n", "Персонажей: 2", "Орден Рассвета (good): 1", "Элис — warrior, женский, Орден Рассвета", "Боб — rogue, мужской")
}

func TestCharacter_ValidationInDialog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	h.joinAndCreate(alice, code, "1", "Элис", "ж", "1", "1", "-", "-")

	reply := h.send(bob, private(bob), "/join "+code)
	mustContain(t, reply, "Выберите фракцию")
	mustContain(t, h.send(bob, private(bob), "9"), "выберите фракцию", "Повторите ответ")
	mustContain(t, h.send(bob, private(bob), "evil"), "Никнейм")
	mustContain(t, h.send(bob, private(bob), "элис"), "Никнейм уже занят")
	mustContain(t, h.send(bob, private(bob), "Б"), "от 2 до 30")
	mustContain(t, h.send(bob, private(bob), "Боб"), "Пол персонажа: 1 — мужской, 2 — женский, 3 — не указывать")
	mustContain(t, h.send(bob, private(bob), "0"), "ответьте 1 (мужской), 2 (женский) или 3 (не указывать)")
	mustContain(t, h.send(bob, private(bob), "не указывать"), "Архетип")
	mustContain(t, h.send(bob, private(bob), "бард"), "выберите архетип")
	mustContain(t, h.send(bob, private(bob), "маг"), "Распределение")
	mustContain(t, h.send(bob, private(bob), "2"), "Характеристики, сумма 14", "Например: 5 3 2 4")
	mustContain(t, h.send(bob, private(bob), "4 4 4 4"), "сумма характеристик должна быть 14")
	mustContain(t, h.send(bob, private(bob), "2 3 5 4"), "Навыки: 6 очков", "например: атлетика 3 бой 3")
	mustContain(t, h.send(bob, private(bob), "внимательность 4 убеждение 2"), "от 0 до 3")
	mustContain(t, h.send(bob, private(bob), "внимательность 3 убеждение 3"), "Открытое описание")
	mustContain(t, h.send(bob, private(bob), strings.Repeat("я", 101)), "до 100 символов")
	mustContain(t, h.send(bob, private(bob), "/cancel"), "Диалог прерван")
	mustContain(t, h.send(bob, private(bob), "/character"), "ещё нет", "/create")
	mustContain(t, h.send(bob, private(bob), "/create"), "Выберите фракцию")
}

func TestGame_JoinAndGames(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	mustContain(t, h.send(alice, private(alice), "/join "+code), "Выберите фракцию")
	mustContain(t, h.send(bob, private(bob), "/join "+code), "Выберите фракцию")
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
	if !strings.Contains(privateMenu, "character") || strings.Contains(privateMenu, "mod") || strings.Contains(privateMenu, "start") || strings.Contains(privateMenu, "ping") || strings.Contains(privateMenu, "llm") {
		t.Fatalf("private menu = %s", privateMenu)
	}
	// B20: меню группы для всех — только команды игры здесь и сейчас; /help по-прежнему перечисляет всё.
	if groupMenu != "[help status act]" {
		t.Fatalf("group menu = %s", groupMenu)
	}
	if adminMenu := names(h.svc.Menu(ctxGroup, true)); adminMenu != "[help chat mod owner create begin pause finish status act next replay round reset]" {
		t.Fatalf("admin menu = %s", adminMenu)
	}
	mustContain(t, h.send(alice, private(alice), "/help"), "в личке", "/join", "/cancel", "/ping")
	mustContain(t, h.send(alice, groupChat, "/help"), "в группе", "/create — создать мир", "/begin", "/ping")
	mustContain(t, h.send(alice, private(alice), "/help"), "/create — создать персонажа")
}

// TestCommands_ManagerMenu (B20): владелец и модераторы получают меню группы с административными командами в своём
// чате: при добавлении бота, регистрации чата командой, старте бота, `/mod add`, `/owner`; снятие — `/mod remove`
// и у бывшего владельца.
func TestCommands_ManagerMenu(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := "[help chat mod owner create begin pause finish status act next replay round reset]"
	h.addBot(owner)
	if got := fmt.Sprint(h.tg.memberMenu(groupChat, owner.ID)); got != admin {
		t.Fatalf("owner menu after add = %s", got)
	}
	if got := h.tg.memberMenu(groupChat, alice.ID); got != nil {
		t.Fatalf("alice menu = %v", got)
	}
	h.send(bob, private(bob), "/start")
	h.send(owner, groupChat, "/mod add @bob")
	if got := fmt.Sprint(h.tg.memberMenu(groupChat, bob.ID)); got != admin {
		t.Fatalf("moderator menu = %s", got)
	}
	h.send(owner, groupChat, "/mod remove @bob")
	if got := h.tg.memberMenu(groupChat, bob.ID); got != nil {
		t.Fatalf("removed moderator menu = %v", got)
	}
	h.send(owner, groupChat, "/owner @bob")
	if got := fmt.Sprint(h.tg.memberMenu(groupChat, bob.ID)); got != admin {
		t.Fatalf("new owner menu = %s", got)
	}
	if got := h.tg.memberMenu(groupChat, owner.ID); got != nil {
		t.Fatalf("former owner menu = %v", got)
	}
	// Старт бота: меню всем владельцам и модераторам зарегистрированных чатов.
	h.tg.member = nil
	if err := h.svc.RegisterMenu(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(h.tg.memberMenu(groupChat, bob.ID)); got != admin {
		t.Fatalf("owner menu after restart = %s", got)
	}
	// Регистрация чата первой командой администратора группы Telegram.
	h2 := newHarness(t)
	h2.tg.admins[owner.ID] = true
	h2.send(owner, groupChat, "/chat")
	if got := fmt.Sprint(h2.tg.memberMenu(groupChat, owner.ID)); got != admin {
		t.Fatalf("owner menu after command registration = %s", got)
	}
}

// outboxTexts — тексты очереди исходящих игры в порядке постановки.
func (h *harness) outboxTexts() []string {
	h.t.Helper()
	c, err := h.store.GetChatByTelegramID(context.Background(), groupChat)
	if err != nil {
		h.t.Fatal(err)
	}
	g, err := h.store.UnfinishedGameOfChat(context.Background(), c.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	msgs, err := h.store.ListOutbox(context.Background(), g.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, m := range msgs {
		if m.TelegramChatID == groupChat {
			out = append(out, m.Text)
		}
	}
	return out
}

func TestRound_BeginActPauseNext(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	h.send(owner, private(owner), "/cancel")
	mustContain(t, h.send(owner, private(owner), "/set min_answers 2"), "Сохранено")
	mustContain(t, h.send(owner, private(owner), "/set timezone UTC"), "Сохранено")

	mustContain(t, h.send(alice, groupChat, "/begin yes"), "Нет прав")
	mustContain(t, h.send(owner, groupChat, "/begin yes"), "нет персонажей")
	h.joinAndCreate(alice, code, "1", "Элис", "ж", "1", "1", "-", "-")
	h.joinAndCreate(bob, code, "2", "Боб", "3", "2", "1", "-", "-")
	mustContain(t, h.send(alice, private(alice), "/act иду в башню"), "Игра не запущена")
	mustContain(t, h.send(alice, private(alice), "иду в башню"), "Я понимаю только команды") // B17: игра не идёт — текст не заявка

	mustContain(t, h.send(owner, groupChat, "/begin yes"), "запущена")
	mustContain(t, h.send(owner, groupChat, "/begin yes"), "уже запущена")
	texts := h.outboxTexts()
	if len(texts) != 2 {
		t.Fatalf("outbox after begin = %q; want scene and round 1", texts)
	}
	mustContain(t, texts[0], "«Песочница» — начало истории", "В Песочница наступает осень")
	mustContain(t, texts[1], "Раунд 1 открыт", "Заявки до 12:15")
	mustContain(t, h.privateOutbox(private(alice), model.OutboxPrivateOpen), "«Песочница» — раунд 1 открыт", "Заявки до 12:15", "Варианты для вас (Рыночная площадь):\n• разобрать приписку (Разум/внимательность): параметр") // B15
	mustContain(t, h.send(alice, groupChat, "/status"), "идёт", "Текущий раунд: 1, заявки до 12:15; заявок 0 из 2", "Следующий раунд: сразу после текущего, в 12:15")

	mustContain(t, h.send(alice, private(alice), "/act"), "Формат: /act")
	mustContain(t, h.send(alice, private(alice), "/act "+strings.Repeat("я", 501)), "до 500 символов")
	mustContain(t, h.send(alice, private(alice), "/act иду в башню"), "Заявка принята: раунд 1, до 12:15")
	mustContain(t, h.send(alice, private(alice), "/act передумал"), "уже принята, изменить её нельзя")
	h.clock.advance(time.Minute)
	mustContain(t, h.send(alice, private(alice), "/act иду на рынок"), "уже принята")
	mustContain(t, h.send(nobody, private(nobody), "/act что-то"), "Игры нет")
	mustContain(t, h.send(owner, private(owner), "/act что-то"), "Персонажа ещё нет")
	mustContain(t, h.send(nobody, private(nobody), "что-то"), "Я понимаю только команды")
	mustContain(t, h.send(owner, private(owner), "что-то"), "Я понимаю только команды")
	mustContain(t, h.send(owner, groupChat, "/act что-то"), "Персонажа ещё нет") // B14: /act в группе — игра чата
	mustContain(t, h.send(bob, groupChat, "/act"), "Формат: /act")

	// Дедлайн: заявок 1 из 2 — раунд пропущен с сообщением, заявка переносится в раунд 2.
	h.clock.advance(15 * time.Minute)
	if err := h.rounds.Tick(context.Background(), h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	texts = h.outboxTexts()
	mustContain(t, texts[2], "Раунд 1\nНедостаточное количество участников. Раунд пропущен.\n\nРаунд 2 открыт", "одна на раунд", "Перенесено заявок из пропущенного раунда: 1")
	mustContain(t, h.send(alice, groupChat, "/status"), "Текущий раунд: 2, заявки до 12:31; заявок 1 из 2", "Предыдущий раунд: 1 пропущен: заявок 1 из 2")

	// Кворум собран: раунд 2 заморожен со снимком; Боб без заявки — нет, оба подали.
	mustContain(t, h.send(bob, groupChat, "/act слежу за гонцом"), "Заявка принята: раунд 2") // B14: открытая заявка в группе
	mustContain(t, h.send(bob, private(bob), "ещё раз"), "уже принята")                       // B17: повтор текстом — та же ошибка
	mustContain(t, h.send(owner, groupChat, "/next"), "Раунд закрыт, следующий открыт")
	texts = h.outboxTexts()
	mustContain(t, texts[3], "Раунд 2 — итоги")
	mustContain(t, texts[4], "Раунд 3 открыт")
	mustContain(t, h.send(alice, groupChat, "/status"), "Текущий раунд: 3", "Предыдущий раунд: 2 разрешён, заявок 2")

	// Пауза и возобновление: открытый раунд получает новый дедлайн.
	mustContain(t, h.send(owner, groupChat, "/pause"), "на паузе")
	mustContain(t, h.send(alice, private(alice), "/act жду"), "Игра на паузе")
	mustContain(t, h.send(alice, private(alice), "жду"), "Я понимаю только команды") // B17: на паузе текст не заявка
	mustContain(t, h.send(alice, groupChat, "/status"), "пауза", "Следующий раунд: игра на паузе")
	h.clock.advance(time.Hour)
	mustContain(t, h.send(owner, groupChat, "/pause"), "возобновлена")
	mustContain(t, h.send(alice, groupChat, "/status"), "Текущий раунд: 3, заявки до 13:31")
	mustContain(t, h.send(alice, private(alice), "иду дальше"), "Принято как заявка на раунд 3, до 13:31") // B17
	mustContain(t, h.send(alice, private(alice), "/act иду дальше"), "уже принята")
}

// privateOutbox — последнее сообщение outbox вида kind в личный чат.
func (h *harness) privateOutbox(chat model.TelegramChatID, kind model.OutboxKind) string {
	h.t.Helper()
	c, err := h.store.GetChatByTelegramID(context.Background(), groupChat)
	if err != nil {
		h.t.Fatal(err)
	}
	g, err := h.store.UnfinishedGameOfChat(context.Background(), c.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	msgs, err := h.store.ListOutbox(context.Background(), g.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	last := ""
	for _, m := range msgs {
		if m.TelegramChatID == chat && m.Kind == kind {
			last = m.Text
		}
	}
	return last
}

// TestRound_PauseAfterSkipped (B11): три пропущенных подряд раунда ставят игру на паузу; /next сообщает об этом,
// /status показывает причину, /pause возобновляет.
func TestRound_PauseAfterSkipped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	h.send(owner, private(owner), "/cancel")
	mustContain(t, h.send(owner, private(owner), "/set min_answers 2"), "Сохранено")
	mustContain(t, h.send(owner, private(owner), "/set timezone UTC"), "Сохранено")
	h.joinAndCreate(alice, code, "1", "Элис", "ж", "1", "1", "-", "-")
	h.joinAndCreate(bob, code, "2", "Боб", "3", "2", "1", "-", "-")
	mustContain(t, h.send(owner, groupChat, "/begin yes"), "запущена")
	mustContain(t, h.send(owner, groupChat, "/next"), "Раунд закрыт, следующий открыт")
	mustContain(t, h.send(owner, groupChat, "/next"), "Раунд закрыт, следующий открыт")
	mustContain(t, h.send(owner, groupChat, "/next"), "Раунд закрыт. Игра на паузе после пропущенных подряд раундов, возобновить: /pause")
	texts := h.outboxTexts()
	if last := texts[len(texts)-1]; last != "Игра на паузе\n3 раунда подряд без кворума. Возобновить: /pause." {
		t.Fatalf("pause message = %q", last)
	}
	mustContain(t, h.send(alice, groupChat, "/status"), "Предыдущий раунд: 3 пропущен: заявок 0 из 2", "Следующий раунд: игра на паузе; возобновить: /pause в группе")
	mustContain(t, h.send(alice, private(alice), "/act жду"), "Игра на паузе")
	mustContain(t, h.send(owner, groupChat, "/next"), "Игра на паузе")
	mustContain(t, h.send(owner, groupChat, "/pause"), "возобновлена")
	mustContain(t, h.send(alice, groupChat, "/status"), "Текущий раунд: 4, заявки до 12:15; заявок 0 из 2")
	mustContain(t, h.send(alice, private(alice), "иду дальше"), "Принято как заявка на раунд 4")
}

func TestRound_StatusTransitionsAndWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	h.send(owner, private(owner), "/cancel")
	mustContain(t, h.send(owner, private(owner), "/set min_answers 1"), "Сохранено")
	mustContain(t, h.send(owner, private(owner), "/set timezone UTC"), "Сохранено")
	mustContain(t, h.send(owner, private(owner), "/set play_window 13:00-14:00"), "Сохранено")
	h.joinAndCreate(alice, code, "1", "Элис", "ж", "1", "1", "-", "-")
	h.joinAndCreate(bob, code, "2", "Боб", "3", "2", "1", "-", "-")

	// Старт вне окна: первый раунд в начале окна.
	mustContain(t, h.send(owner, groupChat, "/begin yes"), "запущена")
	mustContain(t, h.outboxTexts()[1], "Первый раунд в 13:00")
	mustContain(t, h.send(alice, groupChat, "/status"), "Следующий раунд: в 13:00 (игровое окно 13:00–14:00)")
	mustContain(t, h.send(alice, private(alice), "/act рано"), "нет открытого раунда")
	h.clock.advance(30 * time.Minute)
	if err := h.rounds.Tick(context.Background(), h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	mustContain(t, h.send(alice, groupChat, "/status"), "Следующий раунд: в 13:00")
	h.clock.advance(30 * time.Minute)
	if err := h.rounds.Tick(context.Background(), h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	mustContain(t, h.send(alice, groupChat, "/status"), "Текущий раунд: 1, заявки до 13:15")

	// Три раунда подряд только Элис: Боб waiting → waiting → leaving (порог, один раунд; B13); заявка возвращает его в active.
	for i := 0; i < 3; i++ {
		mustContain(t, h.send(alice, private(alice), "/act иду"), "Заявка принята")
		h.clock.advance(15 * time.Minute)
		if err := h.rounds.Tick(context.Background(), h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		want := "waiting"
		if i == 2 {
			want = "leaving"
		}
		mustContain(t, h.send(bob, private(bob), "/character"), want)
	}
	// 13:45 — в окне, раунд 4 открыт; дедлайн 14:00 закрывает его, следующий раунд завтра в 13:00.
	mustContain(t, h.send(bob, private(bob), "/act вернулся"), "Заявка принята: раунд 4")
	mustContain(t, h.send(alice, private(alice), "/act иду"), "Заявка принята: раунд 4")
	h.clock.advance(15 * time.Minute)
	if err := h.rounds.Tick(context.Background(), h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	mustContain(t, h.send(bob, private(bob), "/character"), "active")
	mustContain(t, h.send(alice, groupChat, "/status"), "Предыдущий раунд: 4 разрешён, заявок 2", "Следующий раунд: в 11.10 13:00")
}

func TestLLM_OwnerCommands(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createGame()
	h.send(owner, private(owner), "/cancel")
	mustContain(t, h.send(alice, private(alice), "/llm"), "только владельцу бота")
	mustContain(t, h.send(owner, private(owner), "/llm"), "Профили LLM:", "нет; добавить", "Провайдеры: claude-cli, fake")
	mustContain(t, h.send(owner, private(owner), "/llm test"), "не настроен")
	mustContain(t, h.send(owner, private(owner), "/llm add"), "Формат: /llm add")
	mustContain(t, h.send(owner, private(owner), "/llm add local fake haiku sonnet"), "Профиль local создан (fake)")
	mustContain(t, h.send(owner, private(owner), "/llm add local fake haiku sonnet"), "уже есть")
	mustContain(t, h.send(owner, private(owner), "/llm add xx nope a b"), "Неизвестный провайдер")
	mustContain(t, h.send(owner, private(owner), "/llm use local"), "Профиль по умолчанию: local")
	mustContain(t, h.send(owner, private(owner), "/llm"), "• local: fake; интерпретатор haiku, рассказчик sonnet — по умолчанию", "Текущая игра «Песочница» (id 4): профиль по умолчанию")
	mustContain(t, h.send(owner, private(owner), "/llm use local 4"), "назначен игре 4")
	mustContain(t, h.send(owner, private(owner), "/llm"), "(id 4): профиль local")
	mustContain(t, h.send(owner, private(owner), "/llm use local 99"), "Игра не найдена")
	mustContain(t, h.send(owner, private(owner), "/llm reset 4"), "профиль по умолчанию")
	mustContain(t, h.send(owner, private(owner), "/llm test"), "Профиль local (fake): ок", "Модель: fake-haiku", "токены 10/5", "Ответ по схеме: {\"ok\":true}")
	mustContain(t, h.send(owner, private(owner), "/llm test missing"), "не найден")
	h.llm.Err = errors.New("cli down")
	mustContain(t, h.send(owner, private(owner), "/llm test"), "ошибка вызова — cli down")
	runs, _ := h.store.ListLLMRuns(context.Background(), 10)
	if len(runs) != 2 || runs[0].Error != "cli down" || runs[1].Role != model.RoleTest {
		t.Fatalf("runs = %+v", runs)
	}
}

// TestRound_ResolveTypedCommands: типовые команды разрешаются движком, хроника в группу, приватные итоги в личку, /replay совпадает.
func TestRound_ResolveTypedCommands(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	h.send(owner, private(owner), "/cancel")
	mustContain(t, h.send(owner, private(owner), "/set timezone UTC"), "Сохранено")
	mustContain(t, h.send(owner, private(owner), "/set min_answers 1"), "Сохранено")
	h.joinAndCreate(alice, code, "1", "Элис", "ж", "1", "1", "-", "-")
	h.joinAndCreate(bob, code, "2", "Боб", "3", "2", "1", "-", "-")
	mustContain(t, h.send(alice, private(alice), "/character"), "Локация: Рыночная площадь", "Знания: пока нет")
	mustContain(t, h.send(owner, groupChat, "/begin yes"), "запущена")
	mustContain(t, h.send(alice, private(alice), "/act идти к башне"), "Заявка принята: раунд 1")
	mustContain(t, h.send(bob, private(bob), "/act осмотреть доску"), "Заявка принята: раунд 1")
	mustContain(t, h.send(owner, groupChat, "/next"), "следующий открыт")

	texts := h.outboxTexts()
	mustContain(t, texts[2], "Раунд 1 — итоги", "• Элис (@alice) уходит из «Рыночная площадь» в «Зал Серой башни»")
	privates := map[model.TelegramChatID][]string{}
	c, _ := h.store.GetChatByTelegramID(context.Background(), groupChat)
	g, _ := h.store.UnfinishedGameOfChat(context.Background(), c.ID)
	msgs, _ := h.store.ListOutbox(context.Background(), g.ID)
	for _, m := range msgs {
		if m.Kind == model.OutboxPrivate {
			privates[m.TelegramChatID] = append(privates[m.TelegramChatID], m.Text)
		}
	}
	if len(privates[private(alice)]) != 1 || len(privates[private(bob)]) != 1 {
		t.Fatalf("private results = %+v", privates)
	}
	mustContain(t, privates[private(alice)][0], "раунд 1 — ваш итог", "Заявка «идти к башне»: переходит: Рыночная площадь → Зал Серой башни")
	mustContain(t, privates[private(bob)][0], "Заявка «осмотреть доску»: осматривает: доска объявлений", "Шанс был")
	mustContain(t, h.send(alice, private(alice), "/character"), "Локация: Зал Серой башни")
	bobCard := h.send(bob, private(bob), "/character")
	if strings.Contains(privates[private(bob)][0], "— успех") {
		mustContain(t, bobCard, "Знания:\n• Гонца видели у башни.")
	} else {
		mustContain(t, bobCard, "Знания: пока нет")
	}
	mustContain(t, h.send(alice, groupChat, "/status"), "Предыдущий раунд: 1 разрешён, заявок 2")
	mustContain(t, h.send(owner, groupChat, "/replay 1"), "Раунд 1 воспроизведён", "совпадают")
	mustContain(t, h.send(owner, groupChat, "/replay 9"), "Раунда с таким номером нет")
	mustContain(t, h.send(owner, groupChat, "/replay 2"), "ещё не разрешён")
	// Действие каталога без поддержки движком: заявка rejected с причиной, итог у игрока есть.
	mustContain(t, h.send(alice, private(alice), "/act крадусь вдоль стены и слушаю"), "Заявка принята: раунд 2")
	h.send(owner, groupChat, "/next")
	msgs, _ = h.store.ListOutbox(context.Background(), g.ID)
	var last model.OutboxMessage
	for _, m := range msgs {
		if m.Kind == model.OutboxPrivate {
			last = m
		}
	}
	mustContain(t, last.Text, "отклонена", "действие «красться» появится в пункте плана 12")
}

func TestRound_FinishResetAndDetail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	h.send(owner, private(owner), "/cancel")
	mustContain(t, h.send(owner, private(owner), "/set timezone UTC"), "Сохранено")
	mustContain(t, h.send(owner, private(owner), "/set min_answers 2"), "Сохранено")
	h.joinAndCreate(alice, code, "1", "Элис", "ж", "1", "1", "-", "-")
	h.joinAndCreate(bob, code, "2", "Боб", "3", "2", "1", "-", "-")
	mustContain(t, h.send(owner, groupChat, "/round"), "Раунда с таким номером нет")
	mustContain(t, h.send(owner, groupChat, "/finish yes"), "Игра не запущена")
	mustContain(t, h.send(owner, groupChat, "/begin yes"), "запущена")
	mustContain(t, h.send(alice, private(alice), "/act идти к башне"), "Заявка принята: раунд 1")
	mustContain(t, h.send(owner, groupChat, "/round"), "Раунд 1 — open", "Заявок: 1; итогов: 0; событий: 0", "• Элис (pending): «идти к башне»")
	mustContain(t, h.send(owner, groupChat, "/round x"), "Формат: /round")
	mustContain(t, h.send(alice, groupChat, "/finish yes"), "Нет прав")
	// Завершение с открытым раундом: одна заявка разрешается без кворума, финальная хроника в группе.
	c, _ := h.store.GetChatByTelegramID(context.Background(), groupChat)
	g, _ := h.store.UnfinishedGameOfChat(context.Background(), c.ID)
	mustContain(t, h.send(owner, groupChat, "/finish yes"), "Игра «Песочница» завершена")
	msgs, _ := h.store.ListOutbox(context.Background(), g.ID)
	var texts []string
	for _, m := range msgs {
		if m.TelegramChatID == groupChat {
			texts = append(texts, m.Text)
		}
	}
	mustContain(t, texts[2], "Раунд 1 — итоги", "Элис (@alice) уходит")
	mustContain(t, texts[len(texts)-1], "«Песочница» — история завершена", "Игру завершил администратор. Раундов сыграно: 1.", "• Элис — ", "Зал Серой башни")
	// Завершённая игра перестаёт быть игрой чата и текущей игрой игрока: ответы «игры нет», новая игра создаётся заново.
	mustContain(t, h.send(alice, private(alice), "/act ждать"), "Игры нет")
	mustContain(t, h.send(owner, groupChat, "/finish yes"), "Игры нет")
	mustContain(t, h.send(owner, groupChat, "/round 1"), "Игры нет")
	// Сброс в dev: игра в черновик, персонажи на месте, /begin работает снова.
	h.clock.advance(time.Minute) // лимит частоты команд владельца
	code2 := h.createGame()
	h.send(owner, private(owner), "/cancel")
	mustContain(t, h.send(owner, private(owner), "/set min_answers 1"), "Сохранено")
	h.joinAndCreate(alice, code2, "1", "Элис", "ж", "1", "1", "-", "-")
	mustContain(t, h.send(owner, groupChat, "/begin yes"), "запущена")
	mustContain(t, h.send(alice, private(alice), "/act идти к башне"), "Заявка принята")
	mustContain(t, h.send(owner, groupChat, "/next"), "следующий открыт")
	mustContain(t, h.send(alice, private(alice), "/character"), "Локация: Зал Серой башни")
	mustContain(t, h.send(owner, groupChat, "/round 1"), "Раунд 1 — published", "Метрики (попытка 1)", "запасной разбор: да", "• Элис (frozen): «идти к башне» → resolved move: переходит")
	h.clock.advance(time.Minute)
	mustContain(t, h.send(owner, groupChat, "/reset"), "сброшена в черновик")
	mustContain(t, h.send(alice, private(alice), "/character"), "Локация: Рыночная площадь")
	mustContain(t, h.send(alice, groupChat, "/status"), "черновик")
	mustContain(t, h.send(owner, groupChat, "/begin yes"), "запущена")
	mustContain(t, h.send(alice, groupChat, "/status"), "Текущий раунд: 1")
}

// TestButtons_CharacterDialog (ADR-0015): вопросы диалога создания персонажа несут кнопки с ответами шага; нажатие
// отвечает на вопрос, устаревшая кнопка не применяется, кнопка «Прервать» = /cancel, кнопка завершённого диалога не
// становится заявкой.
func TestButtons_CharacterDialog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()

	mustContain(t, h.send(alice, private(alice), "/start join_"+code), "Выберите фракцию (кнопкой, номером или названием)")
	kb := h.tg.lastKeyboard(private(alice))
	if got := buttonData(kb); len(got) != 3 || got[0] != "@faction 1" || got[1] != "@faction 2" || got[2] != "/cancel" || kb[0][0].Text != "Орден Рассвета" {
		t.Fatalf("faction keyboard = %+v", kb)
	}
	mustContain(t, h.press(alice, private(alice), 10, "@faction 1"), "Никнейм персонажа")
	if !h.clearedLast(10) {
		t.Fatal("faction keyboard not cleared after press")
	}
	if got := buttonData(h.tg.lastKeyboard(private(alice))); len(got) != 1 || got[0] != "/cancel" {
		t.Fatalf("nickname keyboard = %v; want only cancel", got)
	}
	// Повторное нажатие устаревшей кнопки: ответ не применяется, бот повторяет текущий вопрос с его кнопками.
	mustContain(t, h.press(alice, private(alice), 10, "@faction 2"), "Кнопка устарела", "Никнейм персонажа")
	mustContain(t, h.send(alice, private(alice), "Элис"), "Пол персонажа")
	if got := buttonData(h.tg.lastKeyboard(private(alice))); len(got) != 4 || got[1] != "@gender 2" {
		t.Fatalf("gender keyboard = %v", got)
	}
	mustContain(t, h.press(alice, private(alice), 11, "@gender 2"), "Архетип (кнопкой, номером или названием)")
	kb = h.tg.lastKeyboard(private(alice))
	if got := buttonData(kb); len(got) != 6 || got[0] != "@archetype 1" || got[4] != "@archetype 5" || kb[0][0].Text != "Воин" || len(kb[0]) != 3 {
		t.Fatalf("archetype keyboard = %+v", kb)
	}
	mustContain(t, h.press(alice, private(alice), 12, "@archetype 1"), "Распределение очков")
	mustContain(t, h.press(alice, private(alice), 13, "@distribution 1"), "Открытое описание")
	if got := buttonData(h.tg.lastKeyboard(private(alice))); len(got) != 2 || got[0] != "@public_bio -" {
		t.Fatalf("bio keyboard = %v", got)
	}
	mustContain(t, h.press(alice, private(alice), 14, "@public_bio -"), "Скрытое описание")
	card := h.press(alice, private(alice), 15, "@secret_bio -")
	mustContain(t, card, "Персонаж создан", "Элис — Воин\nПол: женский")
	if h.tg.lastKeyboard(private(alice)) != nil {
		t.Fatal("final card has keyboard")
	}
	// Кнопка завершённого диалога не заявка и не ошибка.
	mustContain(t, h.press(alice, private(alice), 15, "@secret_bio -"), "Этот диалог уже завершён")

	// «Прервать» кнопкой.
	mustContain(t, h.send(bob, private(bob), "/start join_"+code), "Выберите фракцию")
	mustContain(t, h.press(bob, private(bob), 20, "/cancel"), "Диалог прерван")
	mustContain(t, h.send(bob, private(bob), "/character"), "ещё нет")
}

// TestButtons_SetupDialog (ADR-0015): диалог настройки игры проходится кнопками: «Оставить как есть», типовые
// значения, часовые пояса и «Без окна».
func TestButtons_SetupDialog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createGame()
	kb := h.tg.lastKeyboard(private(owner))
	if got := buttonData(kb); len(got) != 2 || got[0] != "@title -" || got[1] != "/cancel" || kb[0][0].Text != "Оставить как есть" {
		t.Fatalf("title keyboard = %+v", kb)
	}
	mustContain(t, h.press(owner, private(owner), 30, "@title -"), "Длительность раунда")
	if got := buttonData(h.tg.lastKeyboard(private(owner))); len(got) != 6 || got[0] != "@round_minutes 10" || got[3] != "@round_minutes 60" || got[4] != "@round_minutes -" {
		t.Fatalf("round_minutes keyboard = %v", got)
	}
	mustContain(t, h.press(owner, private(owner), 31, "@round_minutes 20"), "Часовой пояс")
	if got := buttonData(h.tg.lastKeyboard(private(owner))); len(got) != 8 || got[1] != "@timezone Europe/Moscow" {
		t.Fatalf("timezone keyboard = %v", got)
	}
	mustContain(t, h.press(owner, private(owner), 32, "@timezone Asia/Yekaterinburg"), "Игровое окно")
	if got := buttonData(h.tg.lastKeyboard(private(owner))); len(got) != 3 || got[0] != "@play_window нет" {
		t.Fatalf("play_window keyboard = %v", got)
	}
	mustContain(t, h.press(owner, private(owner), 33, "@play_window нет"), "Число раундов")
	if got := buttonData(h.tg.lastKeyboard(private(owner))); len(got) != 6 || got[0] != "@max_rounds 0" || got[3] != "@max_rounds 30" {
		t.Fatalf("max_rounds keyboard = %v", got)
	}
	mustContain(t, h.press(owner, private(owner), 34, "@max_rounds 10"), "Кворум раунда")
	mustContain(t, h.press(owner, private(owner), 35, "@min_answers 2"), "Фракция добра")
	mustContain(t, h.press(owner, private(owner), 36, "@faction_good -"), "Фракция зла")
	mustContain(t, h.press(owner, private(owner), 37, "@faction_evil -"), "Настройка завершена", "Раунд: 20 мин", "Часовой пояс: Asia/Yekaterinburg", "Игровое окно: без окна", "Границы: 10 раундов", "Кворум: 2")
}

// TestButtons_ActOption (ADR-0015): под личным «раунд открыт» кнопки вариантов, нажатие подаёт заявку с названием
// подхода; повтор упирается в «уже принята»; кнопка закрытого раунда отвечает, что раунд закрыт; уведомление о заявке
// несёт кнопки следующего раунда.
func TestButtons_ActOption(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	h.send(owner, private(owner), "/cancel")
	mustContain(t, h.send(owner, private(owner), "/set timezone UTC"), "Сохранено")
	h.joinAndCreate(alice, code, "1", "Элис", "ж", "1", "1", "-", "-")
	h.joinAndCreate(bob, code, "2", "Боб", "3", "2", "1", "-", "-")
	mustContain(t, h.send(owner, groupChat, "/begin yes"), "запущена")

	c, _ := h.store.GetChatByTelegramID(context.Background(), groupChat)
	g, _ := h.store.UnfinishedGameOfChat(context.Background(), c.ID)
	msgs, _ := h.store.ListOutbox(context.Background(), g.ID)
	var open model.OutboxMessage
	for _, m := range msgs {
		if m.Kind == model.OutboxPrivateOpen && m.TelegramChatID == private(alice) {
			open = m
		}
	}
	if len(open.Keyboard) == 0 {
		t.Fatalf("private open without keyboard: %+v", open)
	}
	first := open.Keyboard[0][0]
	if first.Text != "разобрать приписку" || !strings.HasPrefix(first.Data, "/act #1:") {
		t.Fatalf("first option button = %+v", first)
	}
	mustContain(t, h.press(alice, private(alice), 40, first.Data), "Заявка принята: «разобрать приписку», раунд 1")
	if !h.clearedLast(40) {
		t.Fatal("options keyboard not cleared")
	}
	mustContain(t, h.press(alice, private(alice), 40, first.Data), "уже принята, изменить её нельзя")
	mustContain(t, h.send(nobody, private(nobody), "/act "+strings.TrimPrefix(first.Data, "/act ")), "Игры нет")
	mustContain(t, h.send(bob, private(bob), "/act жду у колодца"), "Заявка принята: раунд 1")

	// Раунд закрыт: кнопка старого сообщения устарела; уведомление Боба о заявке несёт кнопки раунда 2
	// (у Элис подход исчерпан: приписка разобрана, вариантов в локации не осталось).
	mustContain(t, h.send(owner, groupChat, "/next"), "следующий открыт")
	mustContain(t, h.press(alice, private(alice), 40, first.Data), "Кнопки этого раунда устарели")
	msgs, _ = h.store.ListOutbox(context.Background(), g.ID)
	var notice, aliceNotice model.OutboxMessage
	for _, m := range msgs {
		if m.Kind == model.OutboxPrivate && m.TelegramChatID == private(bob) {
			notice = m
		}
		if m.Kind == model.OutboxPrivate && m.TelegramChatID == private(alice) {
			aliceNotice = m
		}
	}
	if len(notice.Keyboard) == 0 || !strings.HasPrefix(notice.Keyboard[0][0].Data, "/act #2:") {
		t.Fatalf("notice keyboard = %+v (text %q)", notice.Keyboard, notice.Text)
	}
	// Без модели заявка по кнопке разобрана запасным разбором в подход объекта (не «ждёт»).
	mustContain(t, aliceNotice.Text, "Заявка «разобрать приписку»: осматривает: доска объявлений", "Шанс был 20%")
	if strings.Contains(aliceNotice.Text, "ждёт") {
		t.Fatalf("alice notice = %+v", aliceNotice)
	}
	mustContain(t, h.press(bob, private(bob), 41, notice.Keyboard[0][0].Data), "Заявка принята: «разобрать приписку», раунд 2")
	// Несуществующий подход по ссылке правильного раунда.
	mustContain(t, h.send(owner, groupChat, "/next"), "следующий открыт")
	mustContain(t, h.send(alice, private(alice), "/act #3:1:nothing:0"), "Этот вариант уже недоступен")
}

// TestButtons_ConfirmBeginFinishPause (ADR-0015): /begin и /finish без аргумента спрашивают подтверждение кнопками;
// нажатие без прав не снимает кнопки; «Отмена» отменяет; ответ о паузе несёт кнопку «Возобновить».
func TestButtons_ConfirmBeginFinishPause(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code := h.createGame()
	h.send(owner, private(owner), "/cancel")
	h.joinAndCreate(alice, code, "1", "Элис", "ж", "1", "1", "-", "-")

	mustContain(t, h.send(owner, groupChat, "/begin"), "Начать кампанию «Песочница»?")
	kb := h.tg.lastKeyboard(groupChat)
	if got := buttonData(kb); len(got) != 2 || got[0] != "/begin yes" || got[1] != "/begin no" || kb[0][0].Text != "Начать" {
		t.Fatalf("begin keyboard = %+v", kb)
	}
	mustContain(t, h.press(alice, groupChat, 50, "/begin yes"), "Нет прав")
	if len(h.tg.cleared) != 0 {
		t.Fatal("keyboard cleared after forbidden press")
	}
	mustContain(t, h.press(owner, groupChat, 50, "/begin no"), "Запуск отменён")
	if !h.clearedLast(50) {
		t.Fatal("keyboard not cleared after cancel")
	}
	mustContain(t, h.send(alice, groupChat, "/status"), "черновик")
	mustContain(t, h.press(owner, groupChat, 51, "/begin yes"), "запущена")
	mustContain(t, h.send(alice, groupChat, "/status"), "идёт")

	mustContain(t, h.send(owner, groupChat, "/pause"), "на паузе")
	if got := buttonData(h.tg.lastKeyboard(groupChat)); len(got) != 1 || got[0] != "/pause" {
		t.Fatalf("pause keyboard = %v", got)
	}
	mustContain(t, h.press(owner, groupChat, 52, "/pause"), "возобновлена")
	if h.tg.lastKeyboard(groupChat) != nil {
		t.Fatal("resume reply has keyboard")
	}

	mustContain(t, h.send(owner, groupChat, "/finish"), "Завершить игру «Песочница»?")
	if got := buttonData(h.tg.lastKeyboard(groupChat)); len(got) != 2 || got[0] != "/finish yes" {
		t.Fatalf("finish keyboard = %v", got)
	}
	mustContain(t, h.press(owner, groupChat, 53, "/finish no"), "Завершение отменено")
	mustContain(t, h.press(owner, groupChat, 54, "/finish yes"), "Игра «Песочница» завершена")
}

// TestButtons_HelpAndGames (ADR-0015): под /help кнопки-ссылки на руководство (без GuideURL их нет); /games с одной
// игрой без кнопок.
func TestButtons_HelpAndGames(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.svc.settings.GuideURL = "https://example.org/guide/"
	code := h.createGame()
	h.send(owner, private(owner), "/cancel")
	h.send(alice, private(alice), "/start join_"+code)
	mustContain(t, h.send(alice, private(alice), "/help"), "Команды в личке")
	kb := h.tg.lastKeyboard(private(alice))
	if len(kb) != 3 || kb[0][0].URL != "https://example.org/guide/commands.md" || kb[0][0].Data != "" || kb[2][0].URL != "https://example.org/guide/checks/checks.md" {
		t.Fatalf("help keyboard = %+v", kb)
	}
	h.svc.settings.GuideURL = ""
	mustContain(t, h.send(alice, groupChat, "/help"), "Команды в группе")
	if h.tg.lastKeyboard(groupChat) != nil {
		t.Fatal("help keyboard without GuideURL")
	}
	mustContain(t, h.send(alice, private(alice), "/games"), "Ваши игры", "1. «Песочница»")
	if h.tg.lastKeyboard(private(alice)) != nil {
		t.Fatal("games keyboard with one game")
	}
}
