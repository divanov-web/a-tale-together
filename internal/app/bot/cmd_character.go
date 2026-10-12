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

// character — /character: краткая карточка персонажа в текущей игре; описания — /bio, знания — /knowledge (B28).
func (s *Service) character(ctx context.Context, req *request) error {
	g, c, ok, err := s.currentCharacter(ctx, req)
	if !ok || err != nil {
		return err
	}
	text, err := s.renderCharacter(ctx, g, c)
	if err != nil {
		return err
	}
	return s.reply(ctx, req.in, text)
}

// renderCharacter собирает краткую карточку персонажа по шаблону character.
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
					location = cellLabel(l)
				}
			}
		}
	}
	death := ""
	if c.Status == model.CharacterDead {
		death = fmt.Sprintf("выбыл, до возвращения раундов: %d", c.DeathRoundsLeft)
	}
	partyName := ""
	if c.PartyID != nil {
		if parties, err := s.games.Parties(ctx, g); err == nil {
			for _, p := range parties {
				if p.Party.ID == *c.PartyID {
					partyName = p.Party.Name
				}
			}
		}
	}
	return templates.Render("character", map[string]any{
		"Location": location, "Knowledge": len(c.Knowledge), "Death": death, "Party": partyName,
		"Nickname": c.Nickname, "Gender": c.Gender.Word(), "Archetype": archetype, "Faction": faction, "Status": string(c.Status), "Game": g.Title,
		"HP": c.HP, "Conditions": strings.Join(c.Conditions, ", "), "Attributes": strings.Join(attrs, ", "),
		"Skills": strings.Join(skills, ", "), "Inventory": strings.Join(items, ", "),
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

// currentCharacter — персонаж пользователя в текущей игре; без персонажа отвечает подсказкой /create (ok=false).
func (s *Service) currentCharacter(ctx context.Context, req *request) (model.Game, model.Character, bool, error) {
	g, err := s.games.Current(ctx, req.user)
	if err != nil {
		return model.Game{}, model.Character{}, false, err
	}
	c, err := s.games.Character(ctx, g, req.user.ID)
	if errors.Is(err, game.ErrNoCharacter) {
		return g, c, false, s.reply(ctx, req.in, "Персонажа в игре «"+g.Title+"» ещё нет. Создать: /create")
	}
	if err != nil {
		return g, c, false, err
	}
	return g, c, true, nil
}

// characterBios — /bio или /secret без текста: открытое и скрытое описание персонажа (B28).
func (s *Service) characterBios(ctx context.Context, req *request) error {
	_, c, ok, err := s.currentCharacter(ctx, req)
	if !ok || err != nil {
		return err
	}
	public, secret := c.PublicBio, c.SecretBio
	if public == "" {
		public = "нет"
	}
	if secret == "" {
		secret = "нет"
	}
	return s.reply(ctx, req.in, fmt.Sprintf("%s\n\nОткрытое описание: %s\nСкрытое описание (видит только рассказчик): %s\n\nИзменить: /bio <текст>, /secret <текст> — до %d символов.",
		c.Nickname, public, secret, game.BioMax))
}

// knowledgeChunk — предел длины одного сообщения со знаниями (лимит Telegram 4096 символов).
const knowledgeChunk = 3500

// characterKnowledge — /knowledge: полный список знаний персонажа, длинный — несколькими сообщениями (B28).
func (s *Service) characterKnowledge(ctx context.Context, req *request) error {
	_, c, ok, err := s.currentCharacter(ctx, req)
	if !ok || err != nil {
		return err
	}
	if len(c.Knowledge) == 0 {
		return s.reply(ctx, req.in, c.Nickname+": знаний пока нет. Они появляются после осмотра, испытаний и квестов.")
	}
	for _, text := range knowledgeMessages(c.Nickname, c.Knowledge, knowledgeChunk) {
		if err := s.reply(ctx, req.in, text); err != nil {
			return err
		}
	}
	return nil
}

// knowledgeMessages раскладывает знания списком «•» по сообщениям не длиннее limit символов; первое с заголовком.
func knowledgeMessages(nickname string, knowledge []string, limit int) []string {
	var out []string
	b := strings.Builder{}
	b.WriteString(fmt.Sprintf("Знания %s (%d):\n", nickname, len(knowledge)))
	for _, k := range knowledge {
		line := "• " + k + "\n"
		if b.Len() > 0 && len([]rune(b.String()))+len([]rune(line)) > limit {
			out = append(out, strings.TrimRight(b.String(), "\n"))
			b.Reset()
		}
		b.WriteString(line)
	}
	return append(out, strings.TrimRight(b.String(), "\n"))
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
		return s.characterBios(ctx, req)
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
