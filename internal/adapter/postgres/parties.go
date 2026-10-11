package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"taletogether/internal/model"
)

const partyColumns = `id, game_id, season_id, name, status, leader_character_id, location_id, motto, summary, created_at`

func scanParty(row pgx.Row) (model.Party, error) {
	var p model.Party
	err := row.Scan(&p.ID, &p.GameID, &p.SeasonID, &p.Name, &p.Status, &p.LeaderCharacterID, &p.LocationID, &p.Motto, &p.Summary, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Party{}, model.ErrNotFound
	}
	return p, err
}

// CreateParty создаёт группу; при заданном ID (зарезервированном в снимке раунда) использует его.
func (s *Store) CreateParty(ctx context.Context, p model.Party) (model.Party, error) {
	if p.Status == "" {
		p.Status = model.PartyActive
	}
	var row pgx.Row
	if p.ID != 0 {
		row = s.db(ctx).QueryRow(ctx, `INSERT INTO parties (id, game_id, season_id, name, status, leader_character_id, location_id, motto, summary)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+partyColumns, p.ID, p.GameID, p.SeasonID, p.Name, p.Status, p.LeaderCharacterID, p.LocationID, p.Motto, p.Summary)
	} else {
		row = s.db(ctx).QueryRow(ctx, `INSERT INTO parties (game_id, season_id, name, status, leader_character_id, location_id, motto, summary)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+partyColumns, p.GameID, p.SeasonID, p.Name, p.Status, p.LeaderCharacterID, p.LocationID, p.Motto, p.Summary)
	}
	out, err := scanParty(row)
	if err != nil {
		return model.Party{}, fmt.Errorf("postgres.CreateParty: %w", err)
	}
	return out, nil
}

// UpdateParty сохраняет имя, статус, лидера, локацию, девиз и сводку группы.
func (s *Store) UpdateParty(ctx context.Context, p model.Party) error {
	const q = `UPDATE parties SET name = $3, status = $4, leader_character_id = $5, location_id = $6, motto = $7, summary = $8, updated_at = now()
WHERE game_id = $1 AND id = $2`
	if _, err := s.db(ctx).Exec(ctx, q, p.GameID, p.ID, p.Name, p.Status, p.LeaderCharacterID, p.LocationID, p.Motto, p.Summary); err != nil {
		return fmt.Errorf("postgres.UpdateParty: %w", err)
	}
	return nil
}

// ListParties возвращает группы сезона (активные и распущенные) по порядку создания.
func (s *Store) ListParties(ctx context.Context, game model.GameID, season model.SeasonID) ([]model.Party, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+partyColumns+` FROM parties WHERE game_id = $1 AND season_id = $2 ORDER BY id`, game, season)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListParties: %w", err)
	}
	defer rows.Close()
	var out []model.Party
	for rows.Next() {
		p, err := scanParty(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres.ListParties: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetCharacterParty записывает группу персонажа; nil — без группы.
func (s *Store) SetCharacterParty(ctx context.Context, game model.GameID, id model.CharacterID, party *model.PartyID) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE characters SET party_id = $3, updated_at = now() WHERE game_id = $1 AND id = $2`, game, id, party); err != nil {
		return fmt.Errorf("postgres.SetCharacterParty: %w", err)
	}
	return nil
}

// ReservePartyIDs выдаёт n идентификаторов групп из последовательности таблицы: снимок раунда хранит их для
// групп, которые движок создаст при разделении, чтобы повтор раунда дал те же события. Пропуски в нумерации допустимы.
func (s *Store) ReservePartyIDs(ctx context.Context, n int) ([]model.PartyID, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db(ctx).Query(ctx, `SELECT nextval(pg_get_serial_sequence('parties', 'id')) FROM generate_series(1, $1)`, n)
	if err != nil {
		return nil, fmt.Errorf("postgres.ReservePartyIDs: %w", err)
	}
	defer rows.Close()
	var out []model.PartyID
	for rows.Next() {
		var id model.PartyID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres.ReservePartyIDs: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
