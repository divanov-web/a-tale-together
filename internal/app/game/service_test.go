package game

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"taletogether/internal/adapter/postgres/fake"
	"taletogether/internal/model"
)

type allowAll struct{}

func (allowAll) CanManage(context.Context, model.Chat, model.UserID) (bool, error) { return true, nil }

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

// TestCreate_Lair (S22): `/create` даёт силам зла клетку логова, выбранную кодом из допустимых углов, у добрых логова
// нет; на карте до `/begin` только стартовая клетка; вариант стартового мира и смена жанра координаты не трогают.
func TestCreate_Lair(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := &clock{t: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	store := fake.New(c.Now)
	user, _ := store.UpsertUser(ctx, model.TelegramUser{ID: 1, Username: "owner"})
	chat, _ := store.CreateChat(ctx, model.Chat{TelegramChatID: -100, Title: "Песочница", OwnerUserID: user.ID, BotStatus: model.BotMember})
	svc := New(store, allowAll{}, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	picked := model.LairCorners[1]
	svc.newLair = func() model.Coord { return picked }
	g, err := svc.Create(ctx, chat, user)
	if err != nil {
		t.Fatal(err)
	}
	factions, err := store.ListFactions(ctx, g.ID)
	if err != nil || len(factions) != 2 {
		t.Fatalf("factions = %+v, %v", factions, err)
	}
	if good := factions[0]; good.Code != model.FactionGood || good.Lair != nil {
		t.Fatalf("good = %+v", good)
	}
	if evil := factions[1]; evil.Code != model.FactionEvil || evil.Lair == nil || *evil.Lair != picked {
		t.Fatalf("evil = %+v", evil)
	}
	if locs, _ := store.ListLocations(ctx, g.ID); len(locs) != 1 || !locs[0].Start {
		t.Fatalf("locations before begin = %+v", locs)
	}
	// Переименование фракции и смена жанра логово не трогают.
	if _, err := svc.Set(ctx, g, user.ID, "faction_evil", "Гильдия теней"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Set(ctx, g, user.ID, "genre", DefaultGenre); err != nil {
		t.Fatal(err)
	}
	factions, _ = store.ListFactions(ctx, g.ID)
	if evil := factions[1]; evil.Title != "Гильдия теней" || evil.Lair == nil || *evil.Lair != picked {
		t.Fatalf("evil after set = %+v", evil)
	}
}

// TestRandomLair (S22): случайный выбор всегда из множества допустимых углов и не прибит к одному значению.
func TestRandomLair(t *testing.T) {
	t.Parallel()
	allowed := map[model.Coord]bool{}
	for _, c := range model.LairCorners {
		allowed[c] = true
	}
	seen := map[model.Coord]bool{}
	for i := 0; i < 200; i++ {
		got := randomLair()
		if !allowed[got] {
			t.Fatalf("lair %v outside %v", got, model.LairCorners)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Fatalf("200 draws gave only %v", seen)
	}
}
