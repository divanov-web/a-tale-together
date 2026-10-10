//go:build integration

package postgres

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

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
	if _, err := s.pool.Exec(ctx, `TRUNCATE telegram_updates, users, chats, chat_moderators, dialogs, games, seasons, factions, game_members, characters, character_stats CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
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
	if genre.Template.CharacterModel != "basic_fantasy" || len(genre.Template.Factions) != 3 {
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
	if game.PlayFrom != "10:00" || game.PlayTo != "22:00" || game.Settings.MaxParties != 3 {
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
	if err != nil || len(factions) != 3 || factions[0].Code != model.FactionGood || factions[0].Relations["evil"] != "hostile" {
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
}
