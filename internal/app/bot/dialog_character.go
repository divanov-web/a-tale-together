package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	stepAppearance   = "appearance"
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
	Appearance string            `json:"appearance,omitempty"`
	PublicBio  string            `json:"public_bio"`
	SecretBio  string            `json:"secret_bio"`
}

// startCharacterCreate начинает диалог создания персонажа в личке. В `setup` персонажи не создаются (ADR-0023);
// незавершённая настройка мира у того же пользователя откладывается с подсказкой, как продолжить.
func (s *Service) startCharacterCreate(ctx context.Context, user model.User, g model.Game) error {
	if g.Status == model.GameSetup {
		return game.ErrGameSetup
	}
	if prev, err := s.dialogs.GetDialog(ctx, user.ID); err == nil && prev.Kind == model.DialogGameSetup {
		if err := s.replyPrivate(ctx, user, setupPostponedText); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, model.ErrNotFound) {
		return err
	}
	d := model.Dialog{UserID: user.ID, Kind: model.DialogCharacterCreate, Step: stepFaction, GameID: &g.ID}
	data := characterData{}
	if s.settings.AutoFaction != "" {
		// Фракция назначается без вопроса (на время тестовых прогонов), шаг выбора пропускается.
		data.Faction, d.Step = s.settings.AutoFaction, stepNickname
	}
	if err := saveData(&d, data); err != nil {
		return err
	}
	if err := s.dialogs.SaveDialog(ctx, d); err != nil {
		return err
	}
	q, err := s.characterQuestion(ctx, g, d.Step)
	if err != nil {
		return err
	}
	return s.replyPrivateFormatted(ctx, user, prefixed("Создание персонажа в игре «"+g.Title+"».\n\n", q.text), q.kb)
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
		// Ручное распределение очков только при флаге правил `manual_attributes` (B30): без него характеристики и
		// навыки берутся из архетипа в любом режиме, шаги распределения пропускаются.
		switch {
		case !g.Settings.ManualAttributes || g.Mode == model.ModeNarrative:
			if data.Attributes, data.Skills, err = rules.AutoDistribute(def, a.Key); err != nil {
				return question{}, false, err
			}
			next = stepAppearance
		case g.Mode == model.ModeMechanics:
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
			next = stepAppearance
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
		next = stepAppearance
	case stepAppearance:
		// Внешность (B41): необязательное поле, рассказчику не передаётся.
		if answer != setupSkip {
			if err := game.ValidateAppearance(answer); err != nil {
				return question{}, false, err
			}
			data.Appearance = answer
		}
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
			Attributes: data.Attributes, Skills: data.Skills, Appearance: data.Appearance, PublicBio: data.PublicBio, SecretBio: data.SecretBio,
		})
		if err != nil {
			return question{}, false, err
		}
		card, err := s.renderCharacter(ctx, g, c)
		if err != nil {
			return question{}, false, err
		}
		// Что дальше — по статусу игры (B23): ждать старта, пауза или текущий раунд с вариантами хода кнопками.
		next, kb, err := s.rounds.Welcome(ctx, g, c)
		if err != nil {
			return question{}, false, err
		}
		if link := s.gameChatLink(ctx, g); link != "" {
			kb = append(kb, []model.Button{{Text: "Перейти в чат игры", URL: link}})
		}
		var b model.TextBuilder
		b.WriteString("Персонаж создан!\n\n")
		b.Append(card)
		b.WriteString("\n\n")
		b.Append(next)
		return question{text: b.Build(), kb: kb, before: playerKeyboardHint}, true, nil
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

// gameChatLink — ссылка-приглашение в группу игры для кнопки под итогом создания персонажа (B24, ADR-0022); пусто,
// если Telegram отказал (бот не администратор или без права приглашать): итог создания от этого не ломается.
func (s *Service) gameChatLink(ctx context.Context, g model.Game) string {
	c, err := s.games.Chat(ctx, g)
	if err != nil {
		s.logger.Warn("game chat not found", slog.Int64("game_id", int64(g.ID)), slog.Any("err", err))
		return ""
	}
	link, err := s.chats.InviteLink(ctx, c, func(ctx context.Context) (string, error) {
		return s.tg.CreateInviteLink(ctx, c.TelegramChatID)
	})
	if err != nil {
		s.logger.Info("game chat invite link unavailable", slog.Int64("game_id", int64(g.ID)), slog.Any("err", err))
		return ""
	}
	return link
}

// characterQuestion — вопрос шага с подсказкой по модели персонажа и кнопками ответов (ADR-0015): фракция, пол,
// архетип, распределение и пропуск внешности (B41) и описаний; никнейм, характеристики и навыки вводятся текстом,
// кнопок у них нет (B33).
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
		return ask(b.String(), answerButtons(step, 1, answers...)), nil
	case stepNickname:
		return ask(fmt.Sprintf("Никнейм персонажа (от %d до %d символов, уникален в игре):", game.NicknameMin, game.NicknameMax), nil), nil
	case stepGender:
		return ask("Пол персонажа: 1 — мужской, 2 — женский, 3 — не указывать.",
			answerButtons(step, 3, [2]string{"Мужской", "1"}, [2]string{"Женский", "2"}, [2]string{"Не указывать", "3"})), nil
	case stepArchetype:
		var answers [][2]string
		for i, a := range def.Archetypes {
			answers = append(answers, [2]string{a.Title, strconv.Itoa(i + 1)})
		}
		return ask("Архетип (кнопкой, номером или названием):\n"+archetypesHelp(def), answerButtons(step, 3, answers...)), nil
	case stepDistribution:
		return ask("Распределение очков: 1 — автоматически по архетипу, 2 — вручную.",
			answerButtons(step, 2, [2]string{"Автоматически", "1"}, [2]string{"Вручную", "2"})), nil
	case stepAttributes:
		var names []string
		for _, a := range def.Attributes {
			names = append(names, fmt.Sprintf("%s (%d–%d)", a.Title, a.Min, a.Max))
		}
		return ask(fmt.Sprintf("Характеристики, сумма %d. Введите числа по порядку: %s.\nНапример: %s", def.AttributePoints, strings.Join(names, ", "), exampleAttributes(def)), nil), nil
	case stepSkills:
		var names []string
		for _, sk := range def.Skills {
			names = append(names, sk.Title)
		}
		return ask(fmt.Sprintf("Навыки: %d очков, не более %d в один. Доступны: %s.\nВведите пары «навык значение», например: %s", def.SkillPoints, def.SkillMax, strings.Join(names, ", "), exampleSkills(def)), nil), nil
	case stepAppearance:
		return ask(fmt.Sprintf("Внешность персонажа (до %d символов, видно всем; рост, лицо, одежда, приметы) или «%s»:", game.AppearanceMax, setupSkip),
			answerButtons(step, 1, [2]string{"Пропустить", setupSkip})), nil
	case stepPublicBio:
		return ask(fmt.Sprintf("Открытое описание персонажа (до %d символов, видно всем) или «%s»:", game.BioMax, setupSkip),
			answerButtons(step, 1, [2]string{"Пропустить", setupSkip})), nil
	case stepSecretBio:
		return ask(fmt.Sprintf("Скрытое описание (до %d символов, видит только рассказчик) или «%s»:", game.BioMax, setupSkip),
			answerButtons(step, 1, [2]string{"Пропустить", setupSkip})), nil
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
