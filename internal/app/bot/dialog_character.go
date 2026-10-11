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
	stepGender       = "gender"
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
	Gender     model.Gender      `json:"gender,omitempty"`
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
	return s.replyPrivateKeyboard(ctx, user, "Создание персонажа в игре «"+g.Title+"». Прервать: /cancel\n\n"+q.text, q.kb)
}

// characterStep применяет ответ к шагу и возвращает следующий вопрос или карточку созданного персонажа.
func (s *Service) characterStep(ctx context.Context, req *request, d *model.Dialog, answer string) (question, bool, error) {
	g, err := s.dialogGame(ctx, d)
	if err != nil {
		return question{}, false, err
	}
	var data characterData
	if err := loadData(d, &data); err != nil {
		return question{}, false, err
	}
	cm, err := s.games.CharacterModel(ctx, g)
	if err != nil {
		return question{}, false, err
	}
	def := cm.Definition
	answer = strings.TrimSpace(answer)
	next := ""
	switch d.Step {
	case stepFaction:
		factions, err := s.games.Factions(ctx, g.ID)
		if err != nil {
			return question{}, false, err
		}
		f, ok := findFaction(factions, answer)
		if !ok {
			return question{}, false, fmt.Errorf("%w: выберите фракцию номером или названием", game.ErrBadValue)
		}
		data.Faction = f.Code
		next = stepNickname
	case stepNickname:
		if err := s.games.ValidateNickname(ctx, g, answer); err != nil {
			return question{}, false, err
		}
		data.Nickname = answer
		next = stepGender
	case stepGender:
		gender, ok := parseGender(answer)
		if !ok {
			return question{}, false, fmt.Errorf("%w: ответьте 1 (мужской), 2 (женский) или 3 (не указывать)", game.ErrBadValue)
		}
		data.Gender = gender
		next = stepArchetype
	case stepArchetype:
		a, ok := rules.FindArchetype(def, answer)
		if !ok {
			return question{}, false, fmt.Errorf("%w: выберите архетип номером или названием", game.ErrBadValue)
		}
		data.Archetype = a.Key
		switch g.Mode {
		case model.ModeNarrative:
			if data.Attributes, data.Skills, err = rules.AutoDistribute(def, a.Key); err != nil {
				return question{}, false, err
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
				return question{}, false, err
			}
			next = stepPublicBio
		case "2", "вручную", "сам", "сама":
			next = stepAttributes
		default:
			return question{}, false, fmt.Errorf("%w: ответьте 1 (авто) или 2 (вручную)", game.ErrBadValue)
		}
	case stepAttributes:
		attrs, err := rules.ParseAttributes(def, answer)
		if err != nil {
			return question{}, false, err
		}
		if err := rules.ValidateAttributes(def, attrs); err != nil {
			return question{}, false, err
		}
		data.Attributes = attrs
		next = stepSkills
	case stepSkills:
		skills, err := rules.ParseSkills(def, answer)
		if err != nil {
			return question{}, false, err
		}
		if err := rules.ValidateSkills(def, skills); err != nil {
			return question{}, false, err
		}
		data.Skills = skills
		next = stepPublicBio
	case stepPublicBio:
		if answer != setupSkip {
			if err := game.ValidateBio(answer); err != nil {
				return question{}, false, err
			}
			data.PublicBio = answer
		}
		next = stepSecretBio
	case stepSecretBio:
		if answer != setupSkip {
			if err := game.ValidateBio(answer); err != nil {
				return question{}, false, err
			}
			data.SecretBio = answer
		}
		c, err := s.games.CreateCharacter(ctx, g, req.user, model.CharacterDraft{
			FactionCode: data.Faction, Nickname: data.Nickname, Gender: data.Gender, Archetype: data.Archetype,
			Attributes: data.Attributes, Skills: data.Skills, PublicBio: data.PublicBio, SecretBio: data.SecretBio,
		})
		if err != nil {
			return question{}, false, err
		}
		card, err := s.renderCharacter(ctx, g, c)
		if err != nil {
			return question{}, false, err
		}
		return question{text: "Персонаж создан!\n\n" + strings.TrimRight(card, "\n") + "\n\nЖдите старта игры: хроника появится в групповом чате, личные итоги — здесь."}, true, nil
	default:
		return question{}, false, fmt.Errorf("bot.characterStep: unknown step %q", d.Step)
	}
	if err := saveData(d, data); err != nil {
		return question{}, false, err
	}
	d.Step = next
	q, err := s.characterQuestion(ctx, g, next)
	return q, false, err
}

// characterQuestion — вопрос шага с подсказкой по модели персонажа и кнопками ответов (ADR-0015): фракция, пол,
// архетип, распределение и пропуск описаний; никнейм, характеристики и навыки вводятся текстом.
func (s *Service) characterQuestion(ctx context.Context, g model.Game, step string) (question, error) {
	cm, err := s.games.CharacterModel(ctx, g)
	if err != nil {
		return question{}, err
	}
	def := cm.Definition
	switch step {
	case stepFaction:
		factions, err := s.games.Factions(ctx, g.ID)
		if err != nil {
			return question{}, err
		}
		var b strings.Builder
		b.WriteString("Выберите фракцию (кнопкой, номером или названием):\n")
		var answers [][2]string
		for i, f := range factions {
			fmt.Fprintf(&b, "%d. %s — %s\n", i+1, f.Title, f.Description)
			answers = append(answers, [2]string{f.Title, strconv.Itoa(i + 1)})
		}
		return question{text: b.String(), kb: answerButtons(step, 1, answers...)}, nil
	case stepNickname:
		return question{text: fmt.Sprintf("Никнейм персонажа (от %d до %d символов, уникален в игре):", game.NicknameMin, game.NicknameMax), kb: model.Keyboard{cancelRow()}}, nil
	case stepGender:
		return question{text: "Пол персонажа: 1 — мужской, 2 — женский, 3 — не указывать.",
			kb: answerButtons(step, 3, [2]string{"Мужской", "1"}, [2]string{"Женский", "2"}, [2]string{"Не указывать", "3"})}, nil
	case stepArchetype:
		var answers [][2]string
		for i, a := range def.Archetypes {
			answers = append(answers, [2]string{a.Title, strconv.Itoa(i + 1)})
		}
		return question{text: "Архетип (кнопкой, номером или названием):\n" + archetypesHelp(def), kb: answerButtons(step, 3, answers...)}, nil
	case stepDistribution:
		return question{text: "Распределение очков: 1 — автоматически по архетипу, 2 — вручную.",
			kb: answerButtons(step, 2, [2]string{"Автоматически", "1"}, [2]string{"Вручную", "2"})}, nil
	case stepAttributes:
		var names []string
		for _, a := range def.Attributes {
			names = append(names, fmt.Sprintf("%s (%d–%d)", a.Title, a.Min, a.Max))
		}
		return question{text: fmt.Sprintf("Характеристики, сумма %d. Введите числа по порядку: %s.\nНапример: %s", def.AttributePoints, strings.Join(names, ", "), exampleAttributes(def)), kb: model.Keyboard{cancelRow()}}, nil
	case stepSkills:
		var names []string
		for _, sk := range def.Skills {
			names = append(names, sk.Title)
		}
		return question{text: fmt.Sprintf("Навыки: %d очков, не более %d в один. Доступны: %s.\nВведите пары «навык значение», например: %s", def.SkillPoints, def.SkillMax, strings.Join(names, ", "), exampleSkills(def)), kb: model.Keyboard{cancelRow()}}, nil
	case stepPublicBio:
		return question{text: fmt.Sprintf("Открытое описание персонажа (до %d символов, видно всем) или «%s»:", game.BioMax, setupSkip),
			kb: answerButtons(step, 1, [2]string{"Пропустить", setupSkip})}, nil
	case stepSecretBio:
		return question{text: fmt.Sprintf("Скрытое описание (до %d символов, видит только рассказчик) или «%s»:", game.BioMax, setupSkip),
			kb: answerButtons(step, 1, [2]string{"Пропустить", setupSkip})}, nil
	}
	return question{}, fmt.Errorf("bot.characterQuestion: unknown step %q", step)
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

// parseGender разбирает ответ шага пола: номер, слово или сокращение; «не указывать» — пустое значение.
func parseGender(text string) (model.Gender, bool) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "1", "м", "муж", "мужской", "мужчина", "male", "m":
		return model.GenderMale, true
	case "2", "ж", "жен", "женский", "женщина", "female", "f":
		return model.GenderFemale, true
	case "3", "не указывать", "нет", "пропустить", setupSkip:
		return "", true
	}
	return "", false
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
