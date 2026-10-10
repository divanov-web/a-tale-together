// Package rules — правила игры как данные: модель персонажа, валидация значений, формулы.
package rules

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"taletogether/internal/model"
)

// ErrInvalid — ошибка значений, нарушающих модель персонажа; текст пригоден для ответа игроку.
var ErrInvalid = errors.New("значения не соответствуют модели персонажа")

// invalid оборачивает ErrInvalid пояснением для игрока.
func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// ValidateAttributes проверяет набор характеристик: все ключи модели, границы и сумма очков.
func ValidateAttributes(def model.CharacterModelDef, attrs map[string]int) error {
	if len(attrs) != len(def.Attributes) {
		return invalid("нужно задать %d характеристик", len(def.Attributes))
	}
	sum := 0
	for _, a := range def.Attributes {
		v, ok := attrs[a.Key]
		if !ok {
			return invalid("нет характеристики «%s»", a.Title)
		}
		if v < a.Min || v > a.Max {
			return invalid("«%s» должна быть от %d до %d", a.Title, a.Min, a.Max)
		}
		sum += v
	}
	if sum != def.AttributePoints {
		return invalid("сумма характеристик должна быть %d, сейчас %d", def.AttributePoints, sum)
	}
	return nil
}

// ValidateSkills проверяет навыки: ключи из модели, 0..SkillMax, сумма равна SkillPoints.
func ValidateSkills(def model.CharacterModelDef, skills map[string]int) error {
	sum := 0
	for key, v := range skills {
		s, ok := def.Skill(key)
		if !ok {
			return invalid("неизвестный навык «%s»", key)
		}
		if v < 0 || v > def.SkillMax {
			return invalid("«%s» должен быть от 0 до %d", s.Title, def.SkillMax)
		}
		sum += v
	}
	if sum != def.SkillPoints {
		return invalid("сумма навыков должна быть %d, сейчас %d", def.SkillPoints, sum)
	}
	return nil
}

// ValidateDraft проверяет черновик персонажа целиком: архетип, характеристики, навыки.
func ValidateDraft(def model.CharacterModelDef, d model.CharacterDraft) error {
	if _, ok := def.Archetype(d.Archetype); !ok {
		return invalid("неизвестный архетип «%s»", d.Archetype)
	}
	if err := ValidateAttributes(def, d.Attributes); err != nil {
		return err
	}
	return ValidateSkills(def, d.Skills)
}

// AutoDistribute возвращает характеристики и навыки архетипа; результат проходит валидацию модели.
func AutoDistribute(def model.CharacterModelDef, archetype string) (attrs, skills map[string]int, err error) {
	a, ok := def.Archetype(archetype)
	if !ok {
		return nil, nil, invalid("неизвестный архетип «%s»", archetype)
	}
	attrs = make(map[string]int, len(a.Attributes))
	for k, v := range a.Attributes {
		attrs[k] = v
	}
	skills = make(map[string]int, len(def.Skills))
	for _, s := range def.Skills {
		skills[s.Key] = a.Skills[s.Key]
	}
	if err := ValidateAttributes(def, attrs); err != nil {
		return nil, nil, fmt.Errorf("rules.AutoDistribute: archetype %s: %w", archetype, err)
	}
	if err := ValidateSkills(def, skills); err != nil {
		return nil, nil, fmt.Errorf("rules.AutoDistribute: archetype %s: %w", archetype, err)
	}
	return attrs, skills, nil
}

var hpFormulaRe = regexp.MustCompile(`^\s*(\d+)\s*\+\s*([a-z_]+)\s*$`)

// HP вычисляет стартовое здоровье по формуле модели вида «N + attribute».
func HP(def model.CharacterModelDef, attrs map[string]int) (int, error) {
	m := hpFormulaRe.FindStringSubmatch(def.HPFormula)
	if m == nil {
		return 0, fmt.Errorf("rules.HP: формула %q не поддерживается", def.HPFormula)
	}
	base, _ := strconv.Atoi(m[1])
	v, ok := attrs[m[2]]
	if !ok {
		return 0, fmt.Errorf("rules.HP: в формуле %q нет характеристики %s", def.HPFormula, m[2])
	}
	return base + v, nil
}

// ParseAttributes разбирает ответ игрока: числа в порядке характеристик модели («4 3 2 3»)
// или пары «название значение» в любом порядке («сила 4, ловкость 3 ...»).
func ParseAttributes(def model.CharacterModelDef, text string) (map[string]int, error) {
	tokens := tokens(text)
	attrs := make(map[string]int, len(def.Attributes))
	if allNumbers(tokens) {
		if len(tokens) != len(def.Attributes) {
			return nil, invalid("нужно %d чисел в порядке: %s", len(def.Attributes), attributeOrder(def))
		}
		for i, a := range def.Attributes {
			attrs[a.Key], _ = strconv.Atoi(tokens[i])
		}
		return attrs, nil
	}
	pairs, err := pairs(tokens)
	if err != nil {
		return nil, err
	}
	for name, v := range pairs {
		key, ok := findAttribute(def, name)
		if !ok {
			return nil, invalid("нет характеристики «%s»; доступны: %s", name, attributeOrder(def))
		}
		attrs[key] = v
	}
	return attrs, nil
}

// ParseSkills разбирает ответ игрока: пары «название значение» («бой 3 выживание 2 внимательность 1»);
// не названные навыки получают 0.
func ParseSkills(def model.CharacterModelDef, text string) (map[string]int, error) {
	pairs, err := pairs(tokens(text))
	if err != nil {
		return nil, err
	}
	skills := make(map[string]int, len(def.Skills))
	for _, s := range def.Skills {
		skills[s.Key] = 0
	}
	for name, v := range pairs {
		key, ok := findSkill(def, name)
		if !ok {
			return nil, invalid("нет навыка «%s»; доступны: %s", name, skillOrder(def))
		}
		skills[key] = v
	}
	return skills, nil
}

// FindArchetype находит архетип по ключу, названию или порядковому номеру.
func FindArchetype(def model.CharacterModelDef, text string) (model.ArchetypeDef, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	if n, err := strconv.Atoi(t); err == nil && n >= 1 && n <= len(def.Archetypes) {
		return def.Archetypes[n-1], true
	}
	for _, a := range def.Archetypes {
		if t == a.Key || t == strings.ToLower(a.Title) {
			return a, true
		}
	}
	return model.ArchetypeDef{}, false
}

// SortedKeys возвращает ключи карты значений в порядке модели (для стабильного вывода).
func SortedKeys(order []string, values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for _, k := range order {
		if _, ok := values[k]; ok {
			keys = append(keys, k)
		}
	}
	rest := make([]string, 0)
	for k := range values {
		found := false
		for _, o := range order {
			if o == k {
				found = true
				break
			}
		}
		if !found {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func tokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ' ' || r == ',' || r == ';' || r == ':' || r == '=' || r == '\n' || r == '\t'
	})
}

func allNumbers(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, t := range tokens {
		if _, err := strconv.Atoi(t); err != nil {
			return false
		}
	}
	return true
}

// pairs собирает «название значение» из токенов; название может состоять из одного слова.
func pairs(tokens []string) (map[string]int, error) {
	if len(tokens) == 0 || len(tokens)%2 != 0 {
		return nil, invalid("ожидаются пары «название значение»")
	}
	out := make(map[string]int, len(tokens)/2)
	for i := 0; i < len(tokens); i += 2 {
		v, err := strconv.Atoi(tokens[i+1])
		if err != nil {
			return nil, invalid("«%s» — не число", tokens[i+1])
		}
		out[tokens[i]] = v
	}
	return out, nil
}

func findAttribute(def model.CharacterModelDef, name string) (string, bool) {
	for _, a := range def.Attributes {
		if name == a.Key || name == strings.ToLower(a.Title) {
			return a.Key, true
		}
	}
	return "", false
}

func findSkill(def model.CharacterModelDef, name string) (string, bool) {
	for _, s := range def.Skills {
		if name == s.Key || name == strings.ToLower(s.Title) {
			return s.Key, true
		}
	}
	return "", false
}

func attributeOrder(def model.CharacterModelDef) string {
	names := make([]string, len(def.Attributes))
	for i, a := range def.Attributes {
		names[i] = a.Title
	}
	return strings.Join(names, ", ")
}

func skillOrder(def model.CharacterModelDef) string {
	names := make([]string, len(def.Skills))
	for i, s := range def.Skills {
		names[i] = s.Title
	}
	return strings.Join(names, ", ")
}
