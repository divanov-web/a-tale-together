package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"taletogether/internal/app/game"
	"taletogether/internal/model"
	"taletogether/internal/service/rules"
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
	return s.replyFormatted(ctx, req.in, text)
}

// renderCharacter собирает краткую карточку персонажа: одно поле на строку, ключи жирным (B40); внешность (B41)
// строкой, когда задана; описания и знания — отдельными командами (B28).
func (s *Service) renderCharacter(ctx context.Context, g model.Game, c model.Character) (model.FormattedText, error) {
	cm, err := s.games.CharacterModel(ctx, g)
	if err != nil {
		return model.FormattedText{}, err
	}
	factions, err := s.games.Factions(ctx, g.ID)
	if err != nil {
		return model.FormattedText{}, err
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
	} else if c.Away {
		death = "уходит из истории; вернуться — /back"
	}
	// Группа — спутники по составу, без названия (решение руководителя 2026-10-13).
	party := ""
	if c.PartyID != nil {
		if parties, err := s.games.Parties(ctx, g); err == nil {
			for _, p := range parties {
				if p.Party.ID != *c.PartyID {
					continue
				}
				var mates []string
				for _, m := range p.Members {
					if m.ID != c.ID {
						mates = append(mates, m.Nickname)
					}
				}
				party = "только вы"
				if len(mates) > 0 {
					party = "с " + strings.Join(mates, ", ")
				}
			}
		}
	}
	status := string(c.Status)
	if death != "" {
		status += " (" + death + ")"
	}
	if party == "" {
		party = "нет"
	}
	conditions := strings.Join(c.Conditions, ", ")
	if conditions == "" {
		conditions = "нет"
	}
	inventory := strings.Join(items, ", ")
	if inventory == "" {
		inventory = "пусто"
	}
	knowledge := " (пока нет)"
	if len(c.Knowledge) > 0 {
		knowledge = fmt.Sprintf(" (%d)", len(c.Knowledge))
	}
	return characterCard(cardRows{
		{"", c.Nickname + " — " + archetype},
		{"Пол", c.Gender.Word()},
		{"Фракция", "«" + faction + "»"},
		{"Статус", status},
		{"Игра", "«" + g.Title + "»"},
		{"Локация", location},
		{"Группа", party + " (/party)"},
		{"HP", strconv.Itoa(c.HP)},
		{"Состояния", conditions},
		{"Характеристики", strings.Join(attrs, ", ")},
		{"Навыки", strings.Join(skills, ", ")},
		{"Инвентарь", inventory},
		{"Внешность", c.Appearance},
		{"Описания", "/bio"},
		{"Знания", "/knowledge" + knowledge},
	}), nil
}

// cardRow — строка карточки «ключ: значение»; пустой ключ — строка без ключа (заголовок), пустое значение при ключе —
// строка пропускается.
type cardRow struct{ key, value string }

// cardRows — строки карточки по порядку.
type cardRows []cardRow

// characterCard собирает карточку из строк: ключи жирным (B40), одно поле на строку, без завершающего перевода строки.
func characterCard(rows cardRows) model.FormattedText {
	var b model.TextBuilder
	for _, r := range rows {
		if r.key != "" && r.value == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		if r.key != "" {
			b.Key(r.key)
		}
		b.WriteString(r.value)
	}
	return b.Build()
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

// characterBios — /bio, /secret или /appearance без текста: внешность (B41), открытое и скрытое описание персонажа (B28),
// остаток перезарядки описаний (пункт 34, ADR-0033).
func (s *Service) characterBios(ctx context.Context, req *request) error {
	g, c, ok, err := s.currentCharacter(ctx, req)
	if !ok || err != nil {
		return err
	}
	cooldowns, err := s.games.Cooldowns(ctx, g, c)
	if err != nil {
		return err
	}
	appearance, public, secret := c.Appearance, c.PublicBio, c.SecretBio
	if appearance == "" {
		appearance = "нет"
	}
	if public == "" {
		public = "нет"
	}
	if secret == "" {
		secret = "нет"
	}
	var b model.TextBuilder
	b.WriteString(c.Nickname + "\n\n")
	b.Key("Внешность")
	b.WriteString(appearance + "\n")
	b.Key("Открытое описание")
	b.WriteString(public + "\n")
	b.Key("Скрытое описание (видит только рассказчик)")
	b.WriteString(secret + "\n\n")
	b.Key("Изменить")
	b.Printf("/appearance <текст> — до %d символов; /bio <текст>, /secret <текст> — до %d символов, каждое не чаще раза в %d раундов.",
		game.AppearanceMax, game.BioMax, rules.CooldownRounds[model.CooldownBio])
	if line := cooldownLine(cooldowns); line != "" {
		b.WriteString("\n")
		b.Key("Перезарядка")
		b.WriteString(line)
	}
	return s.replyFormatted(ctx, req.in, b.Build())
}

// cooldownLine — остаток перезарядки описаний для `/bio`; пусто, если оба описания можно менять сейчас.
func cooldownLine(states map[model.CooldownAction]rules.CooldownState) string {
	var parts []string
	for _, item := range []struct {
		action model.CooldownAction
		title  string
	}{{model.CooldownBio, "открытое"}, {model.CooldownSecret, "скрытое"}} {
		st := states[item.action]
		if st.Ready {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s описание — через %s (с раунда %d)", item.title, model.Plural(st.Left, "раунд", "раунда", "раундов"), st.ReadyAt))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + "."
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
		if err := s.replyFormatted(ctx, req.in, text); err != nil {
			return err
		}
	}
	return nil
}

// knowledgeMessages раскладывает знания списком «•» по сообщениям не длиннее limit символов; первое с заголовком
// «Знания <ник> (N)» жирным (B40).
func knowledgeMessages(nickname string, knowledge []string, limit int) []model.FormattedText {
	var out []model.FormattedText
	var b model.TextBuilder
	b.Header(fmt.Sprintf("Знания %s (%d)", nickname, len(knowledge)))
	b.WriteString("\n")
	for _, k := range knowledge {
		line := "• " + k + "\n"
		if b.Len() > 0 && b.Len()+len([]rune(line)) > limit {
			out = append(out, trimRight(b.Build()))
			b = model.TextBuilder{}
		}
		b.WriteString(line)
	}
	return append(out, trimRight(b.Build()))
}

// trimRight снимает завершающие переводы строки у размеченного текста; выделения за концом текста не остаются.
func trimRight(f model.FormattedText) model.FormattedText {
	trimmed := strings.TrimRight(f.Text, "\n")
	return f.Cut(0, len([]rune(trimmed)))
}

// characterBio — /bio <текст>.
func (s *Service) characterBio(ctx context.Context, req *request) error {
	return s.setBio(ctx, req, false)
}

// characterSecret — /secret <текст>.
func (s *Service) characterSecret(ctx context.Context, req *request) error {
	return s.setBio(ctx, req, true)
}

// characterAppearance — /appearance <текст>: описание внешности (B41); без текста — как /bio.
func (s *Service) characterAppearance(ctx context.Context, req *request) error {
	text := strings.TrimSpace(req.in.Args)
	if text == "" {
		return s.characterBios(ctx, req)
	}
	g, err := s.games.Current(ctx, req.user)
	if err != nil {
		return err
	}
	if _, err := s.games.SetAppearance(ctx, g, req.user.ID, text); err != nil {
		return err
	}
	return s.reply(ctx, req.in, "Описание внешности сохранено.")
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
