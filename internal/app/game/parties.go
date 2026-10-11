package game

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"taletogether/internal/model"
	"taletogether/internal/service/party"
)

// Ошибки сценариев групп (пункт 19); текст пригоден для ответа игроку.
var (
	ErrNoParty        = errors.New("вы не в группе")
	ErrPartyNotFound  = errors.New("группы с таким именем нет: список в /party")
	ErrPartyLimit     = errors.New("новых групп больше создать нельзя: вступите в существующую (/party join <имя>)")
	ErrPartyNameTaken = errors.New("группа с таким именем уже есть")
	ErrSameParty      = errors.New("вы уже в этой группе")
	ErrPartyFar       = errors.New("группа в другой локации: присоединиться можно, находясь там же")
)

// PartyInfo — группа с участниками для ответов бота; лидер не раскрывается.
type PartyInfo struct {
	Party   model.Party
	Members []model.Character
}

// Parties возвращает активные группы активного сезона с участниками по порядку создания.
func (s *Service) Parties(ctx context.Context, game model.Game) ([]PartyInfo, error) {
	season, err := s.store.ActiveSeason(ctx, game.ID)
	if err != nil {
		return nil, fmt.Errorf("game.Parties: season: %w", err)
	}
	parties, err := s.store.ListParties(ctx, game.ID, season.ID)
	if err != nil {
		return nil, fmt.Errorf("game.Parties: %w", err)
	}
	chars, err := s.store.ListCharacters(ctx, game.ID, season.ID)
	if err != nil {
		return nil, fmt.Errorf("game.Parties: %w", err)
	}
	var out []PartyInfo
	for _, p := range parties {
		if p.Status != model.PartyActive {
			continue
		}
		info := PartyInfo{Party: p}
		for _, c := range chars {
			if c.PartyID != nil && *c.PartyID == p.ID {
				info.Members = append(info.Members, c)
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// CreateParty создаёт группу с персонажем пользователя как единственным участником и лидером; прежняя группа
// покидается. Имя 2–30 символов, уникально среди активных групп сезона; сверх `max_parties` — ErrPartyLimit.
func (s *Service) CreateParty(ctx context.Context, game model.Game, user model.UserID, name string) (model.Party, error) {
	name = strings.TrimSpace(name)
	if err := party.ValidateName(name); err != nil {
		return model.Party{}, fmt.Errorf("%w: %s", ErrBadValue, err.Error())
	}
	c, err := s.Character(ctx, game, user)
	if err != nil {
		return model.Party{}, err
	}
	if game.Status == model.GameFinished {
		return model.Party{}, ErrGameFinished
	}
	parties, err := s.store.ListParties(ctx, game.ID, c.SeasonID)
	if err != nil {
		return model.Party{}, fmt.Errorf("game.CreateParty: %w", err)
	}
	if _, taken := party.Find(parties, name); taken {
		return model.Party{}, ErrPartyNameTaken
	}
	active := party.Active(parties)
	if c.PartyID != nil {
		// Персонаж уходит из своей группы; если она пуста без него, место в лимите освобождается.
		if n, err := s.memberCount(ctx, game, c.SeasonID, *c.PartyID); err != nil {
			return model.Party{}, err
		} else if n == 1 {
			active--
		}
	}
	if game.Settings.MaxParties > 0 && active >= game.Settings.MaxParties {
		return model.Party{}, ErrPartyLimit
	}
	var created model.Party
	err = s.store.InTx(ctx, func(ctx context.Context) error {
		if err := s.leaveParty(ctx, game, c); err != nil {
			return err
		}
		id := c.ID
		var err error
		created, err = s.store.CreateParty(ctx, model.Party{GameID: game.ID, SeasonID: c.SeasonID, Name: name, Status: model.PartyActive, LeaderCharacterID: &id, LocationID: c.LocationID})
		if err != nil {
			return err
		}
		return s.store.SetCharacterParty(ctx, game.ID, c.ID, &created.ID)
	})
	if err != nil {
		return model.Party{}, fmt.Errorf("game.CreateParty: %w", err)
	}
	s.logger.Info("party created", slog.Int64("game_id", int64(game.ID)), slog.Int64("character_id", int64(c.ID)), slog.String("party", name))
	return created, nil
}

// JoinParty переводит персонажа в активную группу по имени или ссылке `#id`; в идущей игре группа должна быть в той же
// локации (участники группы держатся вместе), прежняя группа покидается.
func (s *Service) JoinParty(ctx context.Context, game model.Game, user model.UserID, ref string) (model.Party, error) {
	c, err := s.Character(ctx, game, user)
	if err != nil {
		return model.Party{}, err
	}
	if game.Status == model.GameFinished {
		return model.Party{}, ErrGameFinished
	}
	parties, err := s.store.ListParties(ctx, game.ID, c.SeasonID)
	if err != nil {
		return model.Party{}, fmt.Errorf("game.JoinParty: %w", err)
	}
	target, ok := party.Find(parties, ref)
	if !ok {
		return model.Party{}, ErrPartyNotFound
	}
	if c.PartyID != nil && *c.PartyID == target.ID {
		return model.Party{}, ErrSameParty
	}
	if game.Status != model.GameDraft && target.LocationID != nil && c.LocationID != nil && *target.LocationID != *c.LocationID {
		return model.Party{}, ErrPartyFar
	}
	err = s.store.InTx(ctx, func(ctx context.Context) error {
		if err := s.leaveParty(ctx, game, c); err != nil {
			return err
		}
		return s.store.SetCharacterParty(ctx, game.ID, c.ID, &target.ID)
	})
	if err != nil {
		return model.Party{}, fmt.Errorf("game.JoinParty: %w", err)
	}
	return target, nil
}

// LeaveParty выводит персонажа из группы; лидерство переходит по правилу преемственности, пустая группа распускается.
func (s *Service) LeaveParty(ctx context.Context, game model.Game, user model.UserID) (model.Party, error) {
	c, err := s.Character(ctx, game, user)
	if err != nil {
		return model.Party{}, err
	}
	if c.PartyID == nil {
		return model.Party{}, ErrNoParty
	}
	parties, err := s.store.ListParties(ctx, game.ID, c.SeasonID)
	if err != nil {
		return model.Party{}, fmt.Errorf("game.LeaveParty: %w", err)
	}
	var left model.Party
	for _, p := range parties {
		if p.ID == *c.PartyID {
			left = p
		}
	}
	err = s.store.InTx(ctx, func(ctx context.Context) error {
		if err := s.leaveParty(ctx, game, c); err != nil {
			return err
		}
		return s.store.SetCharacterParty(ctx, game.ID, c.ID, nil)
	})
	if err != nil {
		return model.Party{}, fmt.Errorf("game.LeaveParty: %w", err)
	}
	return left, nil
}

// leaveParty снимает с группы следы ухода персонажа: новый лидер среди оставшихся или роспуск; party_id персонажа
// меняет вызывающий.
func (s *Service) leaveParty(ctx context.Context, game model.Game, c model.Character) error {
	if c.PartyID == nil {
		return nil
	}
	parties, err := s.store.ListParties(ctx, game.ID, c.SeasonID)
	if err != nil {
		return err
	}
	chars, err := s.store.ListCharacters(ctx, game.ID, c.SeasonID)
	if err != nil {
		return err
	}
	for _, p := range parties {
		if p.ID != *c.PartyID {
			continue
		}
		var rest []model.Character
		for _, m := range chars {
			if m.ID != c.ID && m.PartyID != nil && *m.PartyID == p.ID {
				rest = append(rest, m)
			}
		}
		if len(rest) == 0 {
			p.Status, p.LeaderCharacterID = model.PartyDisbanded, nil
		} else if p.LeaderCharacterID != nil && *p.LeaderCharacterID == c.ID {
			leader, _ := party.Leader(rest, game.CreatedByUserID)
			p.LeaderCharacterID = &leader
		}
		return s.store.UpdateParty(ctx, p)
	}
	return nil
}

func (s *Service) memberCount(ctx context.Context, game model.Game, season model.SeasonID, id model.PartyID) (int, error) {
	chars, err := s.store.ListCharacters(ctx, game.ID, season)
	if err != nil {
		return 0, fmt.Errorf("game.memberCount: %w", err)
	}
	n := 0
	for _, c := range chars {
		if c.PartyID != nil && *c.PartyID == id {
			n++
		}
	}
	return n, nil
}

// assignParty определяет группу нового персонажа в идущей игре (пункт 19): группа его стартовой локации, иначе новая
// группа в пределах `max_parties` с ним как лидером, иначе ближайшая по выходам группа — персонаж входит в историю
// в её локации. В черновике группы собираются при `/begin`.
func (s *Service) assignParty(ctx context.Context, game model.Game, genre model.Genre, c *model.Character) error {
	if game.Status == model.GameDraft || c.LocationID == nil {
		return nil
	}
	parties, err := s.store.ListParties(ctx, game.ID, c.SeasonID)
	if err != nil {
		return err
	}
	var active []model.Party
	for _, p := range parties {
		if p.Status == model.PartyActive {
			active = append(active, p)
		}
	}
	for _, p := range active {
		if p.LocationID != nil && *p.LocationID == *c.LocationID {
			c.PartyID = &p.ID
			return nil
		}
	}
	if game.Settings.MaxParties <= 0 || len(active) < game.Settings.MaxParties {
		taken := make([]string, 0, len(parties))
		for _, p := range parties {
			if p.Status == model.PartyActive {
				taken = append(taken, p.Name)
			}
		}
		created, err := s.store.CreateParty(ctx, model.Party{GameID: game.ID, SeasonID: c.SeasonID, Name: party.NextName(genre.Template.PartyNames, taken), Status: model.PartyActive, LocationID: c.LocationID})
		if err != nil {
			return err
		}
		c.PartyID = &created.ID
		return nil
	}
	locations, err := s.store.ListLocations(ctx, game.ID)
	if err != nil {
		return err
	}
	var candidates []model.LocationID
	for _, p := range active {
		if p.LocationID != nil {
			candidates = append(candidates, *p.LocationID)
		}
	}
	target := active[0]
	if loc, ok := party.Nearest(locations, *c.LocationID, candidates); ok {
		for _, p := range active {
			if p.LocationID != nil && *p.LocationID == loc {
				target = p
				break
			}
		}
	}
	c.PartyID = &target.ID
	if target.LocationID != nil {
		c.LocationID = target.LocationID
	}
	return nil
}
