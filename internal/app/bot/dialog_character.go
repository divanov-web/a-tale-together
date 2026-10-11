package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"taletogether/internal/app/game"
	"taletogether/internal/model"
	"taletogether/internal/service/rules"
)

// Шаги диалога создания персонажа.
const (
	stepFaction      = "faction"
	stepNickname     = "nickname"
	stepArchetype    = "archetype"
	stepDistribution = "distribution"
	stepAttributes   = "attributes"
	stepSkills       = "skills"
	stepPublicBio    = "public_bio"
	stepSecretBio    = "secret_bio"
)

// characterData — черновик персонажа в данных диалога.
type characterData struct {
	Faction    model.FactionCode `json:"faction"`
	Nickname   string            `json:"nickname"`
	Archetype  string            `json:"archetype"`
	Attributes map[string]int    `json:"attributes,omitempty"`
	Skills     map[string]int    `json:"skills,omitempty"`
	PublicBio  string            `json:"public_bio"`
	SecretBio  string            `json:"secret_bio"`
}

// startCharacterCreate начинает диалог создания персонажа в личке.
func (s *Service) startCharacterCreate(ctx context.Context, user model.User, g model.Game) error {
	d := model.Dialog{UserID: user.ID, Kind: model.DialogCharacterCreate, Step: stepFaction, GameID: &g.ID}
	if err := saveData(&d, characterData{}); err != nil {
		return err
	}
	if err := s.dialogs.SaveDialog(ctx, d); err != nil {
		return err
	}
	q, err := s.characterQuestion(ctx, g, stepFaction)
	if err != nil {
		return err
	}
	return s.replyPrivate(ctx, user, "Создание персонажа в игре «"+g.Title+"». Прервать: /cancel\n\n"+q)
}

// characterStep применяет ответ к шагу и возвращает следующий вопрос или карточку созданного персонажа.
func (s *Service) characterStep(ctx context.Context, req *request, d *model.Dialog, answer string) (string, bool, error) {
	g, err := s.dialogGame(ctx, d)
	if err != nil {
		return "", false, err
	}
	var data characterData
	if err := loadData(d, &data); err != nil {
		return "", false, err
	}
	cm, err := s.games.CharacterModel(ctx, g)
	if err != nil {
		return "", false, err
	}
	def := cm.Definition
	answer = strings.TrimSpace(answer)
	next := ""
	switch d.Step {
	case stepFaction:
		factions, err := s.games.Factions(ctx, g.ID)
		if err != nil {
			return "", false, err
		}
		f, ok := findFaction(factions, answer)
		if !ok {
			return "", false, fmt.Errorf("%w: выберите фракцию номером или названием", game.ErrBadValue)
		}
		data.Faction = f.Code
		next = stepNickname
	case stepNickname:
		if err := s.games.ValidateNickname(ctx, g, answer); err != nil {
			return "", false, err
		}
		data.Nickname = answer
		next = stepArchetype
	case stepArchetype:
		a, ok := rules.FindArchetype(def, answer)
		if !ok {
			return "", false, fmt.Errorf("%w: выберите архетип номером или названием", game.ErrBadValue)
		}
		data.Archetype = a.Key
		switch g.Mode {
		case model.ModeNarrative:
			if data.Attributes, data.Skills, err = rules.AutoDistribute(def, a.Key); err != nil {
				return "", false, err
			}
			next = stepPublicBio
		case model.ModeMechanics:
			next = stepAttributes
		default:
			next = stepDistribution
		}
	case stepDistribution:
		switch strings.ToLower(answer) {
		case "1", "авто", "автоматически":
			if data.Attributes, data.Skills, err = rules.AutoDistribute(def, data.Archetype); err != nil {
				return "", false, err
			}
			next = stepPublicBio
		case "2", "вручную", "сам", "сама":
			next = stepAttributes
		default:
			return "", false, fmt.Errorf("%w: ответьте 1 (авто) или 2 (вручную)", game.ErrBadValue)
		}
	case stepAttributes:
		attrs, err := rules.ParseAttributes(def, answer)
		if err != nil {
			return "", false, err
		}
		if err := rules.ValidateAttributes(def, attrs); err != nil {
			return "", false, err
		}
		data.Attributes = attrs
		next = stepSkills
	case stepSkills:
		skills, err := rules.ParseSkills(def, answer)
		if err != nil {
			return "", false, err
		}
		if err := rules.ValidateSkills(def, skills); err != nil {
			return "", false, err
		}
		data.Skills = skills
		next = stepPublicBio
	case stepPublicBio:
		if answer != setupSkip {
			if err := game.ValidateBio(answer); err != nil {
				return "", false, err
			}
			data.PublicBio = answer
		}
		next = stepSecretBio
	case stepSecretBio:
		if answer != setupSkip {
			if err := game.ValidateBio(answer); err != nil {
				return "", false, err
			}
			data.SecretBio = answer
		}
		c, err := s.games.CreateCharacter(ctx, g, req.user, model.CharacterDraft{
			FactionCode: data.Faction, Nickname: data.Nickname, Archetype: data.Archetype,
			Attributes: data.Attributes, Skills: data.Skills, PublicBio: data.PublicBio, SecretBio: data.SecretBio,
		})
		if err != nil {
			return "", false, err
		}
		card, err := s.renderCharacter(ctx, g, c)
		if err != nil {
			return "", false, err
		}
		return "Персонаж создан!\n\n" + card + "\nЖдите старта игры: хроника появится в групповом чате, личные итоги — здесь.", true, nil
	default:
		return "", false, fmt.Errorf("bot.characterStep: unknown step %q", d.Step)
	}
	if err := saveData(d, data); err != nil {
		return "", false, err
	}
	d.Step = next
	q, err := s.characterQuestion(ctx, g, next)
	return q, false, err
}

// characterQuestion — вопрос шага с подсказкой по модели персонажа.
func (s *Service) characterQuestion(ctx context.Context, g model.Game, step string) (string, error) {
	cm, err := s.games.CharacterModel(ctx, g)
	if err != nil {
		return "", err
	}
	def := cm.Definition
	switch step {
	case stepFaction:
		factions, err := s.games.Factions(ctx, g.ID)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString("Выберите фракцию (номер или название):\n")
		for i, f := range factions {
			fmt.Fprintf(&b, "%d. %s — %s\n", i+1, f.Title, f.Description)
		}
		return b.String(), nil
	case stepNickname:
		return fmt.Sprintf("Никнейм персонажа (от %d до %d символов, уникален в игре):", game.NicknameMin, game.NicknameMax), nil
	case stepArchetype:
		return "Архетип (номер или название):\n" + archetypesHelp(def), nil
	case stepDistribution:
		return "Распределение очков: 1 — автоматически по архетипу, 2 — вручную.", nil
	case stepAttributes:
		var names []string
		for _, a := range def.Attributes {
			names = append(names, fmt.Sprintf("%s (%d–%d)", a.Title, a.Min, a.Max))
		}
		return fmt.Sprintf("Характеристики, сумма %d. Введите числа по порядку: %s.\nНапример: %s", def.AttributePoints, strings.Join(names, ", "), exampleAttributes(def)), nil
	case stepSkills:
		var names []string
		for _, sk := range def.Skills {
			names = append(names, sk.Title)
		}
		return fmt.Sprintf("Навыки: %d очков, не более %d в один. Доступны: %s.\nВведите пары «навык значение», например: %s", def.SkillPoints, def.SkillMax, strings.Join(names, ", "), exampleSkills(def)), nil
	case stepPublicBio:
		return fmt.Sprintf("Открытое описание персонажа (до %d символов, видно всем) или «%s»:", game.BioMax, setupSkip), nil
	case stepSecretBio:
		return fmt.Sprintf("Скрытое описание (до %d символов, видит только рассказчик) или «%s»:", game.BioMax, setupSkip), nil
	}
	return "", fmt.Errorf("bot.characterQuestion: unknown step %q", step)
}

// exampleAttributes — пример ответа на шаг характеристик: числа первого архетипа модели в порядке характеристик.
func exampleAttributes(def model.CharacterModelDef) string {
	if len(def.Archetypes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(def.Attributes))
	for _, a := range def.Attributes {
		parts = append(parts, strconv.Itoa(def.Archetypes[0].Attributes[a.Key]))
	}
	return strings.Join(parts, " ")
}

// exampleSkills — пример ответа на шаг навыков: ненулевые навыки первого архетипа в порядке модели.
func exampleSkills(def model.CharacterModelDef) string {
	if len(def.Archetypes) == 0 {
		return ""
	}
	var parts []string
	for _, s := range def.Skills {
		if v := def.Archetypes[0].Skills[s.Key]; v > 0 {
			parts = append(parts, strings.ToLower(s.Title)+" "+strconv.Itoa(v))
		}
	}
	return strings.Join(parts, " ")
}

// findFaction находит фракцию по номеру, коду или названию.
func findFaction(factions []model.Faction, text string) (model.Faction, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	if n, err := strconv.Atoi(t); err == nil && n >= 1 && n <= len(factions) {
		return factions[n-1], true
	}
	for _, f := range factions {
		if t == string(f.Code) || t == strings.ToLower(f.Title) {
			return f, true
		}
	}
	return model.Faction{}, false
}
