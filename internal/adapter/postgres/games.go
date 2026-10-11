package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

// gameCols — столбцы games с алиасом таблицы для scanGame; игровое окно как текст HH:MM.
func gameCols(a string) string {
	return fmt.Sprintf("%[1]sid, %[1]schat_id, %[1]sgenre_id, %[1]sformat, %[1]smode, %[1]stitle, %[1]sstatus, %[1]stimezone, "+
		"coalesce(to_char(%[1]splay_from, 'HH24:MI'), ''), coalesce(to_char(%[1]splay_to, 'HH24:MI'), ''), %[1]sround_minutes, %[1]ssettings, "+
		"%[1]srules_version, %[1]sllm_profile_id, %[1]sjoin_code, %[1]screated_by_user_id, %[1]snext_round_at, %[1]sskipped_streak, %[1]screated_at, %[1]supdated_at", a)
}

var gameColumns = gameCols("")

func scanGame(row pgx.Row) (model.Game, error) {
	var g model.Game
	err := row.Scan(&g.ID, &g.ChatID, &g.GenreID, &g.Format, &g.Mode, &g.Title, &g.Status, &g.Timezone, &g.PlayFrom, &g.PlayTo,
		&g.RoundMinutes, &g.Settings, &g.RulesVersion, &g.LLMProfileID, &g.JoinCode, &g.CreatedByUserID, &g.NextRoundAt, &g.SkippedStreak, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Game{}, model.ErrNotFound
	}
	return g, err
}

// CreateGame создаёт игру; нарушение «одна незавершённая игра на чат» возвращает ошибку БД.
func (s *Store) CreateGame(ctx context.Context, g model.Game) (model.Game, error) {
	q := `
INSERT INTO games (chat_id, genre_id, format, mode, title, status, timezone, play_from, play_to, round_minutes, settings, rules_version, join_code, created_by_user_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::time, NULLIF($9, '')::time, $10, $11, $12, $13, $14)
RETURNING ` + gameColumns
	out, err := scanGame(s.db(ctx).QueryRow(ctx, q, g.ChatID, g.GenreID, g.Format, g.Mode, g.Title, g.Status, g.Timezone, g.PlayFrom, g.PlayTo,
		g.RoundMinutes, g.Settings, g.RulesVersion, g.JoinCode, g.CreatedByUserID))
	if err != nil {
		return model.Game{}, fmt.Errorf("postgres.CreateGame: %w", err)
	}
	return out, nil
}

// UpdateGame сохраняет изменяемые поля игры: жанр, формат, режим, название, статус, окно, раунд, настройки, таймер и счётчик пропусков.
func (s *Store) UpdateGame(ctx context.Context, g model.Game) error {
	const q = `
UPDATE games SET genre_id = $2, format = $3, mode = $4, title = $5, status = $6, timezone = $7,
    play_from = NULLIF($8, '')::time, play_to = NULLIF($9, '')::time, round_minutes = $10, settings = $11,
    rules_version = $12, next_round_at = $13, skipped_streak = $14, updated_at = now()
WHERE id = $1`
	if _, err := s.db(ctx).Exec(ctx, q, g.ID, g.GenreID, g.Format, g.Mode, g.Title, g.Status, g.Timezone, g.PlayFrom, g.PlayTo,
		g.RoundMinutes, g.Settings, g.RulesVersion, g.NextRoundAt, g.SkippedStreak); err != nil {
		return fmt.Errorf("postgres.UpdateGame: %w", err)
	}
	return nil
}

// GetGame возвращает игру по id.
func (s *Store) GetGame(ctx context.Context, id model.GameID) (model.Game, error) {
	g, err := scanGame(s.db(ctx).QueryRow(ctx, `SELECT `+gameColumns+` FROM games WHERE id = $1`, id))
	if err != nil {
		return model.Game{}, fmt.Errorf("postgres.GetGame: %w", err)
	}
	return g, nil
}

// GetGameByJoinCode возвращает игру по коду приглашения без учёта регистра.
func (s *Store) GetGameByJoinCode(ctx context.Context, code string) (model.Game, error) {
	g, err := scanGame(s.db(ctx).QueryRow(ctx, `SELECT `+gameColumns+` FROM games WHERE upper(join_code) = upper($1)`, code))
	if err != nil {
		return model.Game{}, fmt.Errorf("postgres.GetGameByJoinCode: %w", err)
	}
	return g, nil
}

// UnfinishedGameOfChat возвращает незавершённую игру чата.
func (s *Store) UnfinishedGameOfChat(ctx context.Context, chat model.ChatID) (model.Game, error) {
	g, err := scanGame(s.db(ctx).QueryRow(ctx, `SELECT `+gameColumns+` FROM games WHERE chat_id = $1 AND status <> 'finished'`, chat))
	if err != nil {
		return model.Game{}, fmt.Errorf("postgres.UnfinishedGameOfChat: %w", err)
	}
	return g, nil
}

// GamesOfUser возвращает незавершённые игры, где пользователь активный участник, по порядку входа.
func (s *Store) GamesOfUser(ctx context.Context, user model.UserID) ([]model.Game, error) {
	q := `SELECT ` + gameCols("g.") + `
FROM game_members m JOIN games g ON g.id = m.game_id
WHERE m.user_id = $1 AND m.status = 'active' AND g.status <> 'finished'
ORDER BY m.joined_at`
	rows, err := s.db(ctx).Query(ctx, q, user)
	if err != nil {
		return nil, fmt.Errorf("postgres.GamesOfUser: %w", err)
	}
	defer rows.Close()
	var out []model.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.GamesOfUser: scan: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CreateSeason создаёт сезон игры.
func (s *Store) CreateSeason(ctx context.Context, se model.Season) (model.Season, error) {
	const q = `INSERT INTO seasons (game_id, season_no, title, status) VALUES ($1, $2, $3, $4)
RETURNING id, game_id, season_no, title, status, started_at, finished_at`
	var out model.Season
	err := s.db(ctx).QueryRow(ctx, q, se.GameID, se.SeasonNo, se.Title, se.Status).
		Scan(&out.ID, &out.GameID, &out.SeasonNo, &out.Title, &out.Status, &out.StartedAt, &out.FinishedAt)
	if err != nil {
		return model.Season{}, fmt.Errorf("postgres.CreateSeason: %w", err)
	}
	return out, nil
}

// ActiveSeason возвращает активный сезон игры.
func (s *Store) ActiveSeason(ctx context.Context, game model.GameID) (model.Season, error) {
	var out model.Season
	err := s.db(ctx).QueryRow(ctx, `SELECT id, game_id, season_no, title, status, started_at, finished_at FROM seasons WHERE game_id = $1 AND status = 'active' ORDER BY season_no DESC LIMIT 1`, game).
		Scan(&out.ID, &out.GameID, &out.SeasonNo, &out.Title, &out.Status, &out.StartedAt, &out.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Season{}, fmt.Errorf("postgres.ActiveSeason: %w", model.ErrNotFound)
	}
	if err != nil {
		return model.Season{}, fmt.Errorf("postgres.ActiveSeason: %w", err)
	}
	return out, nil
}

// CreateFaction создаёт фракцию игры.
func (s *Store) CreateFaction(ctx context.Context, f model.Faction) (model.Faction, error) {
	const q = `INSERT INTO factions (game_id, code, title, description, tone, relations) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`
	rel := f.Relations
	if rel == nil {
		rel = map[model.FactionCode]string{}
	}
	if err := s.db(ctx).QueryRow(ctx, q, f.GameID, f.Code, f.Title, f.Description, f.Tone, rel).Scan(&f.ID); err != nil {
		return model.Faction{}, fmt.Errorf("postgres.CreateFaction: %w", err)
	}
	return f, nil
}

// UpdateFaction сохраняет название, описание и тон фракции.
func (s *Store) UpdateFaction(ctx context.Context, f model.Faction) error {
	const q = `UPDATE factions SET title = $3, description = $4, tone = $5 WHERE game_id = $1 AND id = $2`
	if _, err := s.db(ctx).Exec(ctx, q, f.GameID, f.ID, f.Title, f.Description, f.Tone); err != nil {
		return fmt.Errorf("postgres.UpdateFaction: %w", err)
	}
	return nil
}

// ListFactions возвращает фракции игры: стартовые good, evil, затем созданные по сюжету по id.
func (s *Store) ListFactions(ctx context.Context, game model.GameID) ([]model.Faction, error) {
	const q = `SELECT id, game_id, code, title, description, tone, relations FROM factions WHERE game_id = $1
ORDER BY array_position(ARRAY['good', 'evil'], code), id`
	rows, err := s.db(ctx).Query(ctx, q, game)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListFactions: %w", err)
	}
	defer rows.Close()
	var out []model.Faction
	for rows.Next() {
		var f model.Faction
		if err := rows.Scan(&f.ID, &f.GameID, &f.Code, &f.Title, &f.Description, &f.Tone, &f.Relations); err != nil {
			return nil, fmt.Errorf("postgres.ListFactions: scan: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// AddMember добавляет участника; повторный вход возвращает статус active с прежней ролью.
func (s *Store) AddMember(ctx context.Context, m model.GameMember) error {
	const q = `INSERT INTO game_members (game_id, user_id, role, status) VALUES ($1, $2, $3, $4)
ON CONFLICT (game_id, user_id) DO UPDATE SET status = 'active'`
	if _, err := s.db(ctx).Exec(ctx, q, m.GameID, m.UserID, m.Role, m.Status); err != nil {
		return fmt.Errorf("postgres.AddMember: %w", err)
	}
	return nil
}

// GetMember возвращает участие пользователя в игре.
func (s *Store) GetMember(ctx context.Context, game model.GameID, user model.UserID) (model.GameMember, error) {
	var m model.GameMember
	err := s.db(ctx).QueryRow(ctx, `SELECT game_id, user_id, role, status, joined_at FROM game_members WHERE game_id = $1 AND user_id = $2`, game, user).
		Scan(&m.GameID, &m.UserID, &m.Role, &m.Status, &m.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.GameMember{}, fmt.Errorf("postgres.GetMember: %w", model.ErrNotFound)
	}
	if err != nil {
		return model.GameMember{}, fmt.Errorf("postgres.GetMember: %w", err)
	}
	return m, nil
}

// ListMembers возвращает активных участников игры с данными пользователей по порядку входа.
func (s *Store) ListMembers(ctx context.Context, game model.GameID) ([]model.MemberInfo, error) {
	q := `SELECT m.game_id, m.user_id, m.role, m.status, m.joined_at, ` + userCols("u.") + `
FROM game_members m JOIN users u ON u.id = m.user_id
WHERE m.game_id = $1 AND m.status = 'active' ORDER BY m.joined_at`
	rows, err := s.db(ctx).Query(ctx, q, game)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListMembers: %w", err)
	}
	defer rows.Close()
	var out []model.MemberInfo
	for rows.Next() {
		var mi model.MemberInfo
		var u model.User
		if err := rows.Scan(&mi.Member.GameID, &mi.Member.UserID, &mi.Member.Role, &mi.Member.Status, &mi.Member.JoinedAt,
			&u.ID, &u.TelegramID, &u.Username, &u.FirstName, &u.LanguageCode, &u.StartedAt, &u.CurrentGameID, &u.LastSeenAt); err != nil {
			return nil, fmt.Errorf("postgres.ListMembers: scan: %w", err)
		}
		mi.User = u
		out = append(out, mi)
	}
	return out, rows.Err()
}
