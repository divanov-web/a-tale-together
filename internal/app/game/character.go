package game

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"taletogether/internal/model"
	"taletogether/internal/service/rules"
)

// Лимиты полей персонажа (docs/game/character.md).
const (
	NicknameMin = 2
	NicknameMax = 30
	BioMax      = 100
)

// CharacterModel возвращает модель персонажа жанра игры.
func (s *Service) CharacterModel(ctx context.Context, game model.Game) (model.CharacterModel, error) {
	genre, err := s.store.GetGenre(ctx, game.GenreID)
	if err != nil {
		return model.CharacterModel{}, fmt.Errorf("game.CharacterModel: genre: %w", err)
	}
	m, err := s.store.GetCharacterModel(ctx, genre.CharacterModelID)
	if err != nil {
		return model.CharacterModel{}, fmt.Errorf("game.CharacterModel: %w", err)
	}
	return m, nil
}

// Character возвращает персонажа пользователя в активном сезоне.
func (s *Service) Character(ctx context.Context, game model.Game, user model.UserID) (model.Character, error) {
	season, err := s.store.ActiveSeason(ctx, game.ID)
	if err != nil {
		return model.Character{}, fmt.Errorf("game.Character: season: %w", err)
	}
	c, err := s.store.GetCharacter(ctx, game.ID, season.ID, user)
	if errors.Is(err, model.ErrNotFound) {
		return model.Character{}, ErrNoCharacter
	}
	if err != nil {
		return model.Character{}, fmt.Errorf("game.Character: %w", err)
	}
	return c, nil
}

// ValidateNickname проверяет длину и уникальность никнейма в активном сезоне.
func (s *Service) ValidateNickname(ctx context.Context, game model.Game, nickname string) error {
	nickname = strings.TrimSpace(nickname)
	if n := len([]rune(nickname)); n < NicknameMin || n > NicknameMax {
		return fmt.Errorf("%w: никнейм от %d до %d символов", ErrBadValue, NicknameMin, NicknameMax)
	}
	if strings.HasPrefix(nickname, "/") {
		return fmt.Errorf("%w: никнейм не может начинаться с «/»", ErrBadValue)
	}
	season, err := s.store.ActiveSeason(ctx, game.ID)
	if err != nil {
		return fmt.Errorf("game.ValidateNickname: %w", err)
	}
	taken, err := s.store.NicknameTaken(ctx, game.ID, season.ID, nickname)
	if err != nil {
		return fmt.Errorf("game.ValidateNickname: %w", err)
	}
	if taken {
		return ErrNicknameTaken
	}
	return nil
}

// ValidateBio проверяет длину описания.
func ValidateBio(text string) error {
	if len([]rune(strings.TrimSpace(text))) > BioMax {
		return fmt.Errorf("%w: описание до %d символов", ErrBadValue, BioMax)
	}
	return nil
}

// CreateCharacter создаёт персонажа активного участника по черновику: валидация по модели,
// HP по формуле, стартовый инвентарь архетипа из каталога жанра; персонаж и характеристики в одной транзакции.
func (s *Service) CreateCharacter(ctx context.Context, game model.Game, user model.User, d model.CharacterDraft) (model.Character, error) {
	if game.Status == model.GameFinished {
		return model.Character{}, ErrGameFinished
	}
	m, err := s.store.GetMember(ctx, game.ID, user.ID)
	if errors.Is(err, model.ErrNotFound) || (err == nil && m.Status != model.MemberActive) {
		return model.Character{}, ErrNotMember
	}
	if err != nil {
		return model.Character{}, fmt.Errorf("game.CreateCharacter: %w", err)
	}
	season, err := s.store.ActiveSeason(ctx, game.ID)
	if err != nil {
		return model.Character{}, fmt.Errorf("game.CreateCharacter: season: %w", err)
	}
	if _, err := s.store.GetCharacter(ctx, game.ID, season.ID, user.ID); err == nil {
		return model.Character{}, ErrHasCharacter
	} else if !errors.Is(err, model.ErrNotFound) {
		return model.Character{}, fmt.Errorf("game.CreateCharacter: %w", err)
	}
	if err := s.ValidateNickname(ctx, game, d.Nickname); err != nil {
		return model.Character{}, err
	}
	if err := ValidateBio(d.PublicBio); err != nil {
		return model.Character{}, err
	}
	if err := ValidateBio(d.SecretBio); err != nil {
		return model.Character{}, err
	}
	genre, err := s.store.GetGenre(ctx, game.GenreID)
	if err != nil {
		return model.Character{}, fmt.Errorf("game.CreateCharacter: genre: %w", err)
	}
	cm, err := s.store.GetCharacterModel(ctx, genre.CharacterModelID)
	if err != nil {
		return model.Character{}, fmt.Errorf("game.CreateCharacter: %w", err)
	}
	if err := rules.ValidateDraft(cm.Definition, d); err != nil {
		return model.Character{}, fmt.Errorf("%w: %s", ErrBadValue, strings.TrimPrefix(err.Error(), rules.ErrInvalid.Error()+": "))
	}
	hp, err := rules.HP(cm.Definition, d.Attributes)
	if err != nil {
		return model.Character{}, fmt.Errorf("game.CreateCharacter: %w", err)
	}
	factions, err := s.store.ListFactions(ctx, game.ID)
	if err != nil {
		return model.Character{}, fmt.Errorf("game.CreateCharacter: %w", err)
	}
	var faction model.Faction
	for _, f := range factions {
		if f.Code == d.FactionCode {
			faction = f
		}
	}
	if faction.ID == 0 {
		return model.Character{}, fmt.Errorf("%w: нет фракции %q", ErrBadValue, d.FactionCode)
	}
	archetype, _ := cm.Definition.Archetype(d.Archetype)
	inventory := make([]model.Item, 0, len(archetype.Items))
	for _, key := range archetype.Items {
		it, ok := genre.Template.Item(key)
		if !ok {
			it = model.ItemTemplate{Key: key, Title: key}
		}
		inventory = append(inventory, model.Item{Key: it.Key, Title: it.Title, Tags: it.Tags})
	}
	c := model.Character{
		GameID: game.ID, SeasonID: season.ID, UserID: user.ID, ModelID: cm.ID,
		Nickname: strings.TrimSpace(d.Nickname), Archetype: d.Archetype,
		PublicBio: strings.TrimSpace(d.PublicBio), SecretBio: strings.TrimSpace(d.SecretBio),
		Status: model.CharacterActive, FactionID: faction.ID, HP: hp, Conditions: []string{}, Inventory: inventory,
		Attributes: d.Attributes, Skills: d.Skills,
	}
	var created model.Character
	err = s.store.InTx(ctx, func(ctx context.Context) error {
		var err error
		created, err = s.store.CreateCharacter(ctx, c)
		return err
	})
	if err != nil {
		return model.Character{}, fmt.Errorf("game.CreateCharacter: %w", err)
	}
	s.logger.Info("character created", slog.Int64("game_id", int64(game.ID)), slog.Int64("character_id", int64(created.ID)))
	return created, nil
}

// SetBio меняет открытое или скрытое описание персонажа.
func (s *Service) SetBio(ctx context.Context, game model.Game, user model.UserID, secret bool, text string) (model.Character, error) {
	if err := ValidateBio(text); err != nil {
		return model.Character{}, err
	}
	c, err := s.Character(ctx, game, user)
	if err != nil {
		return model.Character{}, err
	}
	text = strings.TrimSpace(text)
	if secret {
		c.SecretBio = text
	} else {
		c.PublicBio = text
	}
	if err := s.store.UpdateCharacterBio(ctx, game.ID, c.ID, c.PublicBio, c.SecretBio); err != nil {
		return model.Character{}, fmt.Errorf("game.SetBio: %w", err)
	}
	return c, nil
}
