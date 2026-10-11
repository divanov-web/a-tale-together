package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"taletogether/internal/app/bot/templates"
	"taletogether/internal/app/game"
	"taletogether/internal/model"
)

// character — /character: карточка персонажа в текущей игре.
func (s *Service) character(ctx context.Context, req *request) error {
	g, err := s.games.Current(ctx, req.user)
	if err != nil {
		return err
	}
	c, err := s.games.Character(ctx, g, req.user.ID)
	if errors.Is(err, game.ErrNoCharacter) {
		return s.reply(ctx, req.in, "Персонажа в игре «"+g.Title+"» ещё нет. Создать: /create")
	}
	if err != nil {
		return err
	}
	text, err := s.renderCharacter(ctx, g, c)
	if err != nil {
		return err
	}
	return s.reply(ctx, req.in, text)
}

// renderCharacter собирает карточку персонажа по шаблону character.
func (s *Service) renderCharacter(ctx context.Context, g model.Game, c model.Character) (string, error) {
	cm, err := s.games.CharacterModel(ctx, g)
	if err != nil {
		return "", err
	}
	factions, err := s.games.Factions(ctx, g.ID)
	if err != nil {
		return "", err
	}
	faction := ""
	for _, f := range factions {
		if f.ID == c.FactionID {
			faction = f.Title
		}
	}
	archetype := c.Archetype
	if a, ok := cm.Definition.Archetype(c.Archetype); ok {
		archetype = a.Title
	}
	var attrs, skills, items []string
	for _, a := range cm.Definition.Attributes {
		attrs = append(attrs, fmt.Sprintf("%s %d", a.Title, c.Attributes[a.Key]))
	}
	for _, sk := range cm.Definition.Skills {
		if v := c.Skills[sk.Key]; v > 0 {
			skills = append(skills, fmt.Sprintf("%s %d", sk.Title, v))
		}
	}
	for _, it := range c.Inventory {
		items = append(items, it.Title)
	}
	if len(skills) == 0 {
		skills = []string{"нет"}
	}
	location := "нигде"
	if c.LocationID != nil {
		if locs, err := s.games.Locations(ctx, g.ID); err == nil {
			for _, l := range locs {
				if l.ID == *c.LocationID {
					location = l.Name
				}
			}
		}
	}
	knowledge := c.Knowledge
	if len(knowledge) > 5 {
		knowledge = knowledge[len(knowledge)-5:]
	}
	death := ""
	if c.Status == model.CharacterDead {
		death = fmt.Sprintf("выбыл, до возвращения раундов: %d", c.DeathRoundsLeft)
	}
	return templates.Render("character", map[string]any{
		"Location": location, "Knowledge": knowledge, "Death": death,
		"Nickname": c.Nickname, "Gender": c.Gender.Word(), "Archetype": archetype, "Faction": faction, "Status": string(c.Status), "Game": g.Title,
		"HP": c.HP, "Conditions": strings.Join(c.Conditions, ", "), "Attributes": strings.Join(attrs, ", "),
		"Skills": strings.Join(skills, ", "), "Inventory": strings.Join(items, ", "), "PublicBio": c.PublicBio, "SecretBio": c.SecretBio,
	})
}

// characterCreate — /create в личке: запуск диалога создания персонажа в текущей игре.
func (s *Service) characterCreate(ctx context.Context, req *request) error {
	g, err := s.games.Current(ctx, req.user)
	if err != nil {
		return err
	}
	if _, err := s.games.Character(ctx, g, req.user.ID); err == nil {
		return game.ErrHasCharacter
	} else if !errors.Is(err, game.ErrNoCharacter) {
		return err
	}
	return s.startCharacterCreate(ctx, req.user, g)
}

// characterBio — /bio <текст>.
func (s *Service) characterBio(ctx context.Context, req *request) error {
	return s.setBio(ctx, req, false)
}

// characterSecret — /secret <текст>.
func (s *Service) characterSecret(ctx context.Context, req *request) error {
	return s.setBio(ctx, req, true)
}

func (s *Service) setBio(ctx context.Context, req *request, secret bool) error {
	text := strings.TrimSpace(req.in.Args)
	if text == "" {
		return s.reply(ctx, req.in, fmt.Sprintf("Формат: /%s <текст до %d символов>", req.in.Command, game.BioMax))
	}
	g, err := s.games.Current(ctx, req.user)
	if err != nil {
		return err
	}
	if _, err := s.games.SetBio(ctx, g, req.user.ID, secret, text); err != nil {
		return err
	}
	if secret {
		return s.reply(ctx, req.in, "Скрытое описание сохранено. Его увидит только рассказчик.")
	}
	return s.reply(ctx, req.in, "Открытое описание сохранено.")
}

// archetypesHelp — список архетипов модели с рекомендуемыми характеристиками.
func archetypesHelp(def model.CharacterModelDef) string {
	var b strings.Builder
	for i, a := range def.Archetypes {
		var parts []string
		for _, at := range def.Attributes {
			parts = append(parts, fmt.Sprintf("%s %d", at.Title, a.Attributes[at.Key]))
		}
		fmt.Fprintf(&b, "%d. %s: %s\n", i+1, a.Title, strings.Join(parts, ", "))
	}
	return b.String()
}
