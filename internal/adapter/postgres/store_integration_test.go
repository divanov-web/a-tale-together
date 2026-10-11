//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"taletogether/internal/model"
)

// newTestStore подключается к DATABASE_URL_TEST, применяет миграции и очищает таблицы.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_TEST")
	if dsn == "" {
		t.Skip("DATABASE_URL_TEST is not set")
	}
	ctx := context.Background()
	s, err := New(ctx, Settings{DSN: dsn}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `TRUNCATE telegram_updates, users, chats, chat_moderators, dialogs, games, seasons, factions, game_members, characters, character_stats, rounds, action_submissions, messages_outbox, llm_runs, locations, world_events, resolved_actions CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM llm_profiles WHERE name <> 'local-claude'`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE app_settings SET value = 'local-claude' WHERE key = 'default_llm_profile'`); err != nil {
		t.Fatalf("reset settings: %v", err)
	}
	return s
}

func TestMarkUpdate_Duplicate_NotFresh(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	fresh, err := s.MarkUpdate(ctx, 42)
	if err != nil || !fresh {
		t.Fatalf("first MarkUpdate = %v, %v; want true, nil", fresh, err)
	}
	fresh, err = s.MarkUpdate(ctx, 42)
	if err != nil || fresh {
		t.Fatalf("second MarkUpdate = %v, %v; want false, nil", fresh, err)
	}
}

func TestUpsertUser_Twice_OneRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := model.TelegramUser{ID: 7, Username: "alice", FirstName: "Alice", LanguageCode: "ru"}

	first, err := s.UpsertUser(ctx, u)
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	u.Username = ""
	u.FirstName = "Alisa"
	second, err := s.UpsertUser(ctx, u)
	if err != nil {
		t.Fatalf("UpsertUser again: %v", err)
	}
	if first.ID != second.ID || second.Username != "" || second.FirstName != "Alisa" || second.StartedAt != nil {
		t.Fatalf("got %+v; want same id, empty username, Alisa, no started_at", second)
	}
	if err := s.MarkStarted(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUser(ctx, second.ID)
	if err != nil || got.StartedAt == nil {
		t.Fatalf("GetUser after MarkStarted = %+v, %v", got, err)
	}
	if _, err := s.GetUserByUsername(ctx, "ALICE"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("username cleared but found: %v", err)
	}
}

// TestGameFlow проходит путь пункта 02–04 на реальной схеме: чат → игра с сезоном и фракциями → участник → персонаж.
func TestGameFlow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	owner, err := s.UpsertUser(ctx, model.TelegramUser{ID: 1, Username: "owner", FirstName: "Owner"})
	if err != nil {
		t.Fatal(err)
	}
	player, err := s.UpsertUser(ctx, model.TelegramUser{ID: 2, Username: "alice", FirstName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateChat(ctx, model.Chat{TelegramChatID: -100, Type: model.ChatSupergroup, Title: "Песочница", OwnerUserID: owner.ID, BotStatus: model.BotMember})
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.CreateChat(ctx, model.Chat{TelegramChatID: -100, OwnerUserID: player.ID}); err != nil || again.ID != chat.ID || again.OwnerUserID != owner.ID {
		t.Fatalf("CreateChat again = %+v, %v; want same chat", again, err)
	}
	if err := s.AddModerator(ctx, chat.ID, player.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.IsModerator(ctx, chat.ID, player.ID); !ok {
		t.Fatal("moderator not stored")
	}
	chats, err := s.ChatsOfUser(ctx, player.ID)
	if err != nil || len(chats) != 1 {
		t.Fatalf("ChatsOfUser = %v, %v; want 1 chat", chats, err)
	}

	genre, err := s.GetGenreByCode(ctx, "fantasy")
	if err != nil {
		t.Fatalf("seed genre: %v", err)
	}
	if genre.Template.CharacterModel != "basic_fantasy" || len(genre.Template.Factions) != 2 {
		t.Fatalf("genre template = %+v", genre.Template)
	}
	cm, err := s.GetCharacterModel(ctx, genre.CharacterModelID)
	if err != nil || len(cm.Definition.Archetypes) != 5 || cm.Definition.HPFormula != "4 + strength" {
		t.Fatalf("character model = %+v, %v", cm, err)
	}

	var game model.Game
	var season model.Season
	err = s.InTx(ctx, func(ctx context.Context) error {
		var err error
		game, err = s.CreateGame(ctx, model.Game{ChatID: chat.ID, GenreID: genre.ID, Format: model.FormatNovel, Mode: model.ModeBalanced, Title: "Мир",
			Status: model.GameDraft, Timezone: "Europe/Moscow", PlayFrom: "10:00", PlayTo: "22:00", RoundMinutes: 15, Settings: model.DefaultGameSettings(),
			JoinCode: "ABCD2345", CreatedByUserID: owner.ID})
		if err != nil {
			return err
		}
		if season, err = s.CreateSeason(ctx, model.Season{GameID: game.ID, SeasonNo: 1, Status: model.SeasonActive}); err != nil {
			return err
		}
		for _, ft := range genre.Template.Factions {
			rel := map[model.FactionCode]string{}
			for k, v := range ft.Relations {
				rel[model.FactionCode(k)] = v
			}
			if _, err := s.CreateFaction(ctx, model.Faction{GameID: game.ID, Code: ft.Code, Title: ft.Title, Description: ft.Description, Tone: ft.Tone, Relations: rel}); err != nil {
				return err
			}
		}
		if err := s.AddMember(ctx, model.GameMember{GameID: game.ID, UserID: owner.ID, Role: model.RoleAdmin, Status: model.MemberActive}); err != nil {
			return err
		}
		return s.SetCurrentGame(ctx, owner.ID, &game.ID)
	})
	if err != nil {
		t.Fatalf("create game tx: %v", err)
	}
	if game.PlayFrom != "10:00" || game.PlayTo != "22:00" || game.Settings.MaxParties != 4 {
		t.Fatalf("game round trip = %+v", game)
	}
	if _, err := s.CreateGame(ctx, model.Game{ChatID: chat.ID, GenreID: genre.ID, Title: "Второй", Status: model.GameDraft, Timezone: "UTC", RoundMinutes: 15, JoinCode: "ZZZZ2345", CreatedByUserID: owner.ID}); err == nil {
		t.Fatal("second unfinished game in chat accepted")
	}
	if got, err := s.GetGameByJoinCode(ctx, "abcd2345"); err != nil || got.ID != game.ID {
		t.Fatalf("GetGameByJoinCode = %v, %v", got.ID, err)
	}
	if got, err := s.UnfinishedGameOfChat(ctx, chat.ID); err != nil || got.ID != game.ID {
		t.Fatalf("UnfinishedGameOfChat = %v, %v", got.ID, err)
	}
	if got, err := s.ActiveSeason(ctx, game.ID); err != nil || got.ID != season.ID {
		t.Fatalf("ActiveSeason = %v, %v", got.ID, err)
	}
	factions, err := s.ListFactions(ctx, game.ID)
	if err != nil || len(factions) != 2 || factions[0].Code != model.FactionGood || factions[0].Relations["evil"] != "hostile" {
		t.Fatalf("ListFactions = %+v, %v", factions, err)
	}
	factions[0].Title = "Орден"
	if err := s.UpdateFaction(ctx, factions[0]); err != nil {
		t.Fatal(err)
	}

	ends := game.CreatedAt.AddDate(0, 1, 0)
	game.Settings.EndsAt = &ends
	game.PlayFrom, game.PlayTo = "", ""
	if err := s.UpdateGame(ctx, game); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetGame(ctx, game.ID)
	if err != nil || got.Settings.EndsAt == nil || got.PlayFrom != "" {
		t.Fatalf("GetGame after update = %+v, %v", got, err)
	}

	// Персонаж до участия: нарушение FK.
	if _, err := s.CreateCharacter(ctx, model.Character{GameID: game.ID, SeasonID: season.ID, UserID: player.ID, ModelID: cm.ID, Nickname: "Элис", Archetype: "warrior", FactionID: factions[0].ID, HP: 8}); err == nil {
		t.Fatal("character without membership accepted")
	}
	if err := s.AddMember(ctx, model.GameMember{GameID: game.ID, UserID: player.ID, Role: model.RolePlayer, Status: model.MemberActive}); err != nil {
		t.Fatal(err)
	}
	members, err := s.ListMembers(ctx, game.ID)
	if err != nil || len(members) != 2 || members[0].User.Username != "owner" {
		t.Fatalf("ListMembers = %+v, %v", members, err)
	}
	games, err := s.GamesOfUser(ctx, player.ID)
	if err != nil || len(games) != 1 {
		t.Fatalf("GamesOfUser = %v, %v", games, err)
	}

	var created model.Character
	err = s.InTx(ctx, func(ctx context.Context) error {
		var err error
		created, err = s.CreateCharacter(ctx, model.Character{
			GameID: game.ID, SeasonID: season.ID, UserID: player.ID, ModelID: cm.ID, Nickname: "Элис", Archetype: "warrior", PublicBio: "шумная",
			Status: model.CharacterActive, FactionID: factions[0].ID, HP: 8, Inventory: []model.Item{{Key: "sword", Title: "Меч", Tags: []string{"weapon"}}},
			Attributes: map[string]int{"strength": 4, "agility": 3, "wits": 2, "will": 3}, Skills: map[string]int{"melee": 3, "survival": 2, "awareness": 1},
		})
		return err
	})
	if err != nil {
		t.Fatalf("CreateCharacter: %v", err)
	}
	loaded, err := s.GetCharacter(ctx, game.ID, season.ID, player.ID)
	if err != nil {
		t.Fatalf("GetCharacter: %v", err)
	}
	if loaded.ID != created.ID || loaded.Attributes["strength"] != 4 || loaded.Skills["melee"] != 3 || len(loaded.Inventory) != 1 || loaded.Inventory[0].Title != "Меч" || loaded.HP != 8 {
		t.Fatalf("character round trip = %+v", loaded)
	}
	if taken, _ := s.NicknameTaken(ctx, game.ID, season.ID, "элис"); !taken {
		t.Fatal("nickname uniqueness is case sensitive")
	}
	if err := s.UpdateCharacterBio(ctx, game.ID, created.ID, "тихая", "боится воды"); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListCharacters(ctx, game.ID, season.ID)
	if err != nil || len(list) != 1 || list[0].SecretBio != "боится воды" {
		t.Fatalf("ListCharacters = %+v, %v", list, err)
	}

	// Транзакция откатывается целиком.
	boom := errors.New("boom")
	err = s.InTx(ctx, func(ctx context.Context) error {
		if err := s.AddMember(ctx, model.GameMember{GameID: game.ID, UserID: owner.ID, Role: model.RolePlayer, Status: model.MemberActive}); err != nil {
			return err
		}
		if _, err := s.CreateCharacter(ctx, model.Character{GameID: game.ID, SeasonID: season.ID, UserID: owner.ID, ModelID: cm.ID, Nickname: "Босс", Archetype: "mage", FactionID: factions[1].ID, HP: 6}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx error = %v; want boom", err)
	}
	if taken, _ := s.NicknameTaken(ctx, game.ID, season.ID, "Босс"); taken {
		t.Fatal("rolled back character still exists")
	}

	// Диалог: сохранение, замена, удаление.
	if err := s.SaveDialog(ctx, model.Dialog{UserID: player.ID, Kind: model.DialogCharacterCreate, Step: "faction", GameID: &game.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDialog(ctx, model.Dialog{UserID: player.ID, Kind: model.DialogCharacterCreate, Step: "nickname", GameID: &game.ID, Data: []byte(`{"faction":"good"}`)}); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDialog(ctx, player.ID)
	if err != nil || d.Step != "nickname" || string(d.Data) != `{"faction": "good"}` && string(d.Data) != `{"faction":"good"}` {
		t.Fatalf("GetDialog = %+v, %v", d, err)
	}
	if err := s.DeleteDialog(ctx, player.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDialog(ctx, player.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("GetDialog after delete = %v; want ErrNotFound", err)
	}

	// Пункт 05: раунды, заявки, outbox на реальной схеме.
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	game.Status = model.GameActive
	game.NextRoundAt = &now
	if err := s.UpdateGame(ctx, game); err != nil {
		t.Fatal(err)
	}
	if ids, err := s.DueGameIDs(ctx, now); err != nil || len(ids) != 1 || ids[0] != game.ID {
		t.Fatalf("DueGameIDs = %v, %v", ids, err)
	}
	if ids, _ := s.DueGameIDs(ctx, now.Add(-time.Second)); len(ids) != 0 {
		t.Fatalf("DueGameIDs before next_round_at = %v", ids)
	}
	r1, err := s.CreateRound(ctx, model.Round{GameID: game.ID, SeasonID: season.ID, RoundNo: 1, Status: model.RoundOpen, OpensAt: now, DeadlineAt: now.Add(15 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRound(ctx, model.Round{GameID: game.ID, SeasonID: season.ID, RoundNo: 2, Status: model.RoundOpen, OpensAt: now, DeadlineAt: now}); err == nil {
		t.Fatal("second open round accepted")
	}
	if got, err := s.OpenRound(ctx, game.ID); err != nil || got.ID != r1.ID {
		t.Fatalf("OpenRound = %+v, %v", got, err)
	}
	sub, err := s.UpsertSubmission(ctx, model.Submission{GameID: game.ID, RoundID: r1.ID, CharacterID: created.ID, RawText: "иду", UpdatedAt: now})
	if err != nil || sub.Status != model.SubmissionPending || !sub.SubmittedAt.Equal(now) {
		t.Fatalf("UpsertSubmission = %+v, %v", sub, err)
	}
	again, err := s.UpsertSubmission(ctx, model.Submission{GameID: game.ID, RoundID: r1.ID, CharacterID: created.ID, RawText: "бегу", UpdatedAt: now.Add(time.Minute)})
	if err != nil || again.ID != sub.ID || again.RawText != "бегу" || !again.SubmittedAt.Equal(now) || !again.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("UpsertSubmission again = %+v, %v", again, err)
	}
	if got, err := s.GetSubmission(ctx, game.ID, r1.ID, created.ID); err != nil || got.RawText != "бегу" {
		t.Fatalf("GetSubmission = %+v, %v", got, err)
	}
	// Пропуск: заявка переезжает в раунд 2; снимок и seed замораживают раунд 2.
	closed := now.Add(15 * time.Minute)
	r1.Status, r1.SkipReason, r1.ClosedAt = model.RoundSkipped, "заявок 1 из 2", &closed
	if err := s.UpdateRound(ctx, r1); err != nil {
		t.Fatal(err)
	}
	r2, err := s.CreateRound(ctx, model.Round{GameID: game.ID, SeasonID: season.ID, RoundNo: 2, Status: model.RoundOpen, OpensAt: closed, DeadlineAt: closed.Add(15 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MoveSubmissions(ctx, game.ID, r1.ID, r2.ID); err != nil {
		t.Fatal(err)
	}
	subs, err := s.ListSubmissions(ctx, game.ID, r2.ID)
	if err != nil || len(subs) != 1 || subs[0].ID != sub.ID {
		t.Fatalf("ListSubmissions after move = %+v, %v", subs, err)
	}
	snap := &model.RoundSnapshot{SchemaVersion: 1, Characters: []model.SnapshotCharacter{{ID: created.ID, Nickname: "Элис", Status: model.CharacterActive, HP: 8, FactionID: factions[0].ID, Conditions: []string{}}},
		Submissions: []model.SnapshotSubmission{{SubmissionID: sub.ID, CharacterID: created.ID, Text: "бегу", SubmittedAt: now}}}
	r2.Status, r2.Seed, r2.Snapshot = model.RoundFrozen, 42, snap
	if err := s.UpdateRound(ctx, r2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSubmissionsStatus(ctx, game.ID, r2.ID, model.SubmissionFrozen); err != nil {
		t.Fatal(err)
	}
	last, err := s.LastRound(ctx, game.ID)
	if err != nil || last.ID != r2.ID || last.Seed != 42 || last.Snapshot == nil || len(last.Snapshot.Submissions) != 1 || last.Snapshot.Characters[0].Nickname != "Элис" {
		t.Fatalf("LastRound = %+v, %v", last, err)
	}
	if got, err := s.RoundByNo(ctx, game.ID, 1); err != nil || got.Status != model.RoundSkipped || got.SkipReason != "заявок 1 из 2" {
		t.Fatalf("RoundByNo = %+v, %v", got, err)
	}
	if _, err := s.OpenRound(ctx, game.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("OpenRound after freeze = %v", err)
	}
	if subs, _ = s.ListSubmissions(ctx, game.ID, r2.ID); subs[0].Status != model.SubmissionFrozen {
		t.Fatalf("submission status = %s", subs[0].Status)
	}
	if err := s.UpdateCharacterActivity(ctx, game.ID, created.ID, model.CharacterWaiting, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetCharacter(ctx, game.ID, season.ID, player.ID); got.Status != model.CharacterWaiting || got.MissedRounds != 1 {
		t.Fatalf("character activity = %+v", got)
	}
	// Группы (пункт 19): создание, явный зарезервированный id, участие персонажа, обновление лидера и статуса, сброс.
	leader := created.ID
	watch, err := s.CreateParty(ctx, model.Party{GameID: game.ID, SeasonID: season.ID, Name: "Ночной дозор", LeaderCharacterID: &leader, LocationID: created.LocationID})
	if err != nil || watch.ID == 0 || watch.Status != model.PartyActive {
		t.Fatalf("CreateParty = %+v, %v", watch, err)
	}
	if _, err := s.CreateParty(ctx, model.Party{GameID: game.ID, SeasonID: season.ID, Name: "ночной дозор"}); err == nil {
		t.Fatal("duplicate active party name accepted")
	}
	reserved, err := s.ReservePartyIDs(ctx, 2)
	if err != nil || len(reserved) != 2 || reserved[0] <= watch.ID || reserved[1] <= reserved[0] {
		t.Fatalf("ReservePartyIDs = %v, %v", reserved, err)
	}
	blades, err := s.CreateParty(ctx, model.Party{ID: reserved[1], GameID: game.ID, SeasonID: season.ID, Name: "Вольные клинки"})
	if err != nil || blades.ID != reserved[1] {
		t.Fatalf("CreateParty with id = %+v, %v", blades, err)
	}
	if err := s.SetCharacterParty(ctx, game.ID, created.ID, &watch.ID); err != nil {
		t.Fatal(err)
	}
	member, _ := s.GetCharacter(ctx, game.ID, season.ID, player.ID)
	if member.PartyID == nil || *member.PartyID != watch.ID {
		t.Fatalf("character party = %+v", member)
	}
	member.PartyID = &blades.ID
	if err := s.SaveCharacterState(ctx, game.ID, member); err != nil {
		t.Fatal(err)
	}
	watch.Status, watch.LeaderCharacterID = model.PartyDisbanded, nil
	if err := s.UpdateParty(ctx, watch); err != nil {
		t.Fatal(err)
	}
	parties, err := s.ListParties(ctx, game.ID, season.ID)
	if err != nil || len(parties) != 2 || parties[0].Status != model.PartyDisbanded || parties[0].LeaderCharacterID != nil || parties[1].Name != "Вольные клинки" {
		t.Fatalf("ListParties = %+v, %v", parties, err)
	}
	if member, _ = s.GetCharacter(ctx, game.ID, season.ID, player.ID); member.PartyID == nil || *member.PartyID != blades.ID {
		t.Fatalf("character party after SaveCharacterState = %+v", member)
	}
	if next, err := s.ReservePartyIDs(ctx, 0); err != nil || next != nil {
		t.Fatalf("ReservePartyIDs(0) = %v, %v", next, err)
	}
	// Блокировка игры внутри транзакции; из второй транзакции строка пропускается.
	err = s.InTx(ctx, func(txCtx context.Context) error {
		if _, err := s.LockGame(txCtx, game.ID); err != nil {
			return err
		}
		_, err := s.LockGame(ctx, game.ID) // пул, не транзакция: SKIP LOCKED
		if !errors.Is(err, model.ErrNotFound) {
			return fmt.Errorf("locked game visible: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("LockGame: %v", err)
	}
	// Outbox: дедуп по раунду и виду, claim с попыткой, финал.
	msg := model.OutboxMessage{GameID: game.ID, RoundID: &r2.ID, TelegramChatID: -100, Kind: model.OutboxRoundClosed, Text: "закрыт", SendAfter: now}
	for i := 0; i < 2; i++ {
		if err := s.EnqueueOutbox(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.EnqueueOutbox(ctx, model.OutboxMessage{GameID: game.ID, TelegramChatID: -100, Kind: model.OutboxScene, Text: "сцена", SendAfter: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	queued, err := s.ListOutbox(ctx, game.ID)
	if err != nil || len(queued) != 2 || queued[0].Part != 1 || queued[0].Status != model.OutboxPending {
		t.Fatalf("ListOutbox = %+v, %v", queued, err)
	}
	claimed, err := s.ClaimOutbox(ctx, now, 5*time.Minute, 10)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 1 || claimed[0].Status != model.OutboxSending || claimed[0].Text != "закрыт" {
		t.Fatalf("ClaimOutbox = %+v, %v", claimed, err)
	}
	if again, _ := s.ClaimOutbox(ctx, now, 5*time.Minute, 10); len(again) != 0 {
		t.Fatalf("claimed twice: %+v", again)
	}
	id := int64(777)
	sent := now.Add(time.Second)
	claimed[0].Status, claimed[0].TelegramMessageID, claimed[0].SentAt = model.OutboxSent, &id, &sent
	if err := s.FinishOutbox(ctx, claimed[0]); err != nil {
		t.Fatal(err)
	}
	// Застрявшее sending → unknown.
	stuck, _ := s.ClaimOutbox(ctx, now.Add(time.Hour), 5*time.Minute, 10) // сцена: send_after через час
	if len(stuck) != 1 || stuck[0].Kind != model.OutboxScene {
		t.Fatalf("scene claim = %+v", stuck)
	}
	if _, err := s.ClaimOutbox(ctx, now.Add(2*time.Hour), 5*time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	queued, _ = s.ListOutbox(ctx, game.ID)
	if queued[0].Status != model.OutboxSent || *queued[0].TelegramMessageID != 777 || queued[1].Status != model.OutboxUnknown {
		t.Fatalf("outbox final = %+v", queued)
	}

	// Пункт 06: профили LLM, настройка по умолчанию, профиль игры, учёт вызовов.
	profiles, err := s.ListLLMProfiles(ctx)
	if err != nil || len(profiles) != 1 || profiles[0].Name != "local-claude" || profiles[0].Provider != "claude-cli" || profiles[0].Params.Effort != "low" || profiles[0].Params.Parallelism != 2 {
		t.Fatalf("seed profiles = %+v, %v", profiles, err)
	}
	if v, err := s.GetAppSetting(ctx, model.SettingDefaultLLMProfile); err != nil || v != "local-claude" {
		t.Fatalf("default profile = %q, %v", v, err)
	}
	if _, err := s.GetAppSetting(ctx, "missing"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing setting: %v", err)
	}
	created2, err := s.CreateLLMProfile(ctx, model.LLMProfile{Name: "api", Provider: "anthropic-api", ModelInterpreter: "claude-haiku-5-5", ModelNarrator: "claude-opus-5-5", Params: model.LLMParams{MaxOutput: 2000}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateLLMProfile(ctx, model.LLMProfile{Name: "api", Provider: "x", ModelInterpreter: "a", ModelNarrator: "b"}); err == nil {
		t.Fatal("duplicate profile accepted")
	}
	if err := s.SetAppSetting(ctx, model.SettingDefaultLLMProfile, "api"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetAppSetting(ctx, model.SettingDefaultLLMProfile); v != "api" {
		t.Fatalf("setting after update = %q", v)
	}
	if err := s.SetGameLLMProfile(ctx, game.ID, &created2.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetGame(ctx, game.ID); got.LLMProfileID == nil || *got.LLMProfileID != created2.ID {
		t.Fatalf("game profile = %+v", got.LLMProfileID)
	}
	if err := s.SetGameLLMProfile(ctx, game.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetGame(ctx, game.ID); got.LLMProfileID != nil {
		t.Fatal("game profile not reset")
	}
	run, err := s.CreateLLMRun(ctx, model.LLMRun{GameID: &game.ID, RoundID: &r2.ID, ProfileID: created2.ID, Provider: "anthropic-api", Role: model.RoleInterpreter, Model: "claude-haiku-5-5",
		PromptVersion: "v1", TokensIn: 100, TokensOut: 20, CostUSD: 0.001234, Subscription: false, DurationMS: 1500, RequestID: "req-1"})
	if err != nil || run.ID == 0 {
		t.Fatalf("CreateLLMRun = %+v, %v", run, err)
	}
	if _, err := s.CreateLLMRun(ctx, model.LLMRun{ProfileID: created2.ID, Provider: "anthropic-api", Role: model.RoleTest, Model: "m", Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ListLLMRuns(ctx, 10)
	if err != nil || len(runs) != 2 || runs[0].Error != "boom" || runs[0].GameID != nil || runs[1].CostUSD != 0.001234 || runs[1].RoundID == nil || *runs[1].RoundID != r2.ID {
		t.Fatalf("ListLLMRuns = %+v, %v", runs, err)
	}

	// Пункт 07: локации, события, итоги заявок, состояние персонажа.
	if len(genre.Template.Locations) != 4 || !genre.Template.Locations[0].Start || !genre.Template.Locations[3].Objects[2].HiddenUntil {
		t.Fatalf("genre locations = %+v", genre.Template.Locations)
	}
	loc, err := s.CreateLocation(ctx, model.Location{GameID: game.ID, Name: "Подвал", Difficulty: 1, Links: []model.LocationLink{{To: "Зал", Title: "наверх"}},
		Objects: []model.LocationObject{{Key: "door", Title: "дверь", State: "locked", Approaches: []model.Approach{{Action: model.ActionInteract, Attribute: "strength", Difficulty: 2, OnSuccess: json.RawMessage(`{"state":"broken"}`)}}}}, Active: true, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateLocation(ctx, model.Location{GameID: game.ID, Name: "Подвал"}); err == nil {
		t.Fatal("duplicate location accepted")
	}
	locs, err := s.ListLocations(ctx, game.ID)
	if err != nil || len(locs) != 1 || locs[0].Objects[0].Approaches[0].Difficulty != 2 || locs[0].Links[0].To != "Зал" {
		t.Fatalf("ListLocations = %+v, %v", locs, err)
	}
	locs[0].Objects[0].State = "broken"
	if err := s.UpdateLocationObjects(ctx, game.ID, loc.ID, locs[0].Objects); err != nil {
		t.Fatal(err)
	}
	if locs, _ = s.ListLocations(ctx, game.ID); locs[0].Objects[0].State != "broken" {
		t.Fatalf("objects not updated: %+v", locs[0].Objects)
	}
	if seq, _ := s.NextEventSeq(ctx, game.ID); seq != 1 {
		t.Fatalf("NextEventSeq = %d", seq)
	}
	cid := created.ID
	events := []model.Event{
		{GameID: game.ID, RoundID: r2.ID, Seq: 1, Type: model.EventCharacterMoved, Payload: json.RawMessage(`{"character_id":1,"to":2}`), Visibility: model.VisibilityLocation, LocationID: &loc.ID, CharacterID: &cid},
		{GameID: game.ID, RoundID: r2.ID, Seq: 2, Type: model.EventFactLearned, Payload: json.RawMessage(`{"fact":"x"}`), Visibility: model.VisibilityPrivate, VisibleTo: []model.CharacterID{cid}},
	}
	if err := s.AppendEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvents(ctx, events[:1]); err == nil {
		t.Fatal("duplicate seq accepted")
	}
	evs, err := s.ListEvents(ctx, game.ID, r2.ID)
	if err != nil || len(evs) != 2 || evs[1].VisibleTo[0] != cid || evs[0].LocationID == nil || *evs[0].LocationID != loc.ID || evs[0].VisibleTo != nil {
		t.Fatalf("ListEvents = %+v, %v", got, err)
	}
	if seq, _ := s.NextEventSeq(ctx, game.ID); seq != 3 {
		t.Fatalf("NextEventSeq after append = %d", seq)
	}
	results := []model.ActionResult{{SubmissionID: sub.ID, CharacterID: cid, Intent: model.Intent{SubmissionID: sub.ID, CharacterID: cid, ActionType: model.ActionMove, Target: "зал"},
		Status: model.ResultResolved, Summary: "переходит", Check: &model.CheckInfo{Param: 3, Difficulty: 5, ChancePct: 30, Roll: 12, Outcome: model.OutcomeSuccess}, EventSeqs: []int64{1, 2}}}
	for i := 0; i < 2; i++ {
		if err := s.CreateResolvedActions(ctx, game.ID, r2.ID, results); err != nil {
			t.Fatal(err)
		}
	}
	acts, err := s.ListResolvedActions(ctx, game.ID, r2.ID)
	if err != nil || len(acts) != 1 || acts[0].Intent.Target != "зал" || acts[0].Check.Param != 3 || len(acts[0].EventSeqs) != 2 {
		t.Fatalf("ListResolvedActions = %+v, %v", acts, err)
	}
	diedAt := now
	if err := s.SaveCharacterState(ctx, game.ID, model.Character{ID: cid, LocationID: &loc.ID, HP: 0, Conditions: []string{"wounded"}, Knowledge: []string{"x"}, Status: model.CharacterDead, DeathRoundsLeft: 96, DiedAt: &diedAt}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetCharacter(ctx, game.ID, season.ID, player.ID); got.Status != model.CharacterDead || got.DeathRoundsLeft != 96 || got.DiedAt == nil || got.LocationID == nil || got.Knowledge[0] != "x" || got.Conditions[0] != "wounded" || got.Inventory[0].Title != "Меч" {
		t.Fatalf("character state = %+v", got)
	}
}
