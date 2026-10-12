package rules

import (
	"taletogether/internal/model"
)

// Version — версия правил, закрепляется за игрой (`games.rules_version`, G10). 1 — «Прямой шанс» (ADR-0014).
const Version = 1

// Границы шанса (docs/game/checks.md): гарантированного исхода нет.
const (
	ChanceMin  = 5
	ChanceMax  = 95
	ChanceBase = 50 // шанс при равных параметре и сложности
	ChanceStep = 10 // процентных пунктов за очко разницы
)

// Сложность свободного действия по оценке риска (docs/game/checks.md).
var riskDifficulty = map[model.Risk]int{model.RiskNormal: 5, model.RiskHard: 7, model.RiskExtreme: 9}

// RiskDifficulty — базовая сложность по риску; неизвестный риск считается обычным.
func RiskDifficulty(r model.Risk) int {
	if d, ok := riskDifficulty[r]; ok {
		return d
	}
	return riskDifficulty[model.RiskNormal]
}

// Risks — уровни риска свободного испытания в порядке роста сложности.
var Risks = []model.Risk{model.RiskNormal, model.RiskHard, model.RiskExtreme}

// Conditions — состояния персонажа, которые может назначить эффект on_fail (docs/game/character.md).
var Conditions = []string{model.ConditionWounded, model.ConditionExhausted, model.ConditionHidden, model.ConditionDetained}

// FreeDamageMax — потолок урона в эффекте on_fail свободного испытания (допущение пункта 08).
const FreeDamageMax = 2

// ValidRisk — риск из таблицы.
func ValidRisk(r model.Risk) bool {
	_, ok := riskDifficulty[r]
	return ok
}

// ValidCondition — состояние из списка.
func ValidCondition(c string) bool {
	for _, k := range Conditions {
		if k == c {
			return true
		}
	}
	return false
}

// Состояния, снимающие по очку с параметра (docs/game/character.md).
var penaltyConditions = []string{model.ConditionWounded, model.ConditionExhausted}

// Param — параметр персонажа в проверке: характеристика + навык + бонус предметов (не более 2) − штрафы состояний
// + помощь (+1 за помощника, не более +2). Может быть любым: границы задаёт шанс.
func Param(c model.SnapshotCharacter, attribute, skill string, helpers int) int {
	param := c.Attributes[attribute]
	if skill != "" {
		param += c.Skills[skill]
	}
	bonus := 0
	for _, it := range c.Inventory {
		if it.Bonus != nil && skill != "" && it.Bonus.Skill == skill {
			bonus += it.Bonus.Points
		}
	}
	param += min(bonus, 2)
	for _, cond := range penaltyConditions {
		if c.HasCondition(cond) {
			param--
		}
	}
	return param + min(helpers, 2)
}

// ChancePct — шанс успеха в процентах: 50 + 10 × (параметр − сложность) в границах 5–95 («Прямой шанс»).
func ChancePct(param, difficulty int) int {
	return min(max(ChanceBase+ChanceStep*(param-difficulty), ChanceMin), ChanceMax)
}

// Success — исход одного броска d100 (1–100) против шанса.
func Success(roll, chancePct int) bool {
	return roll <= chancePct
}

// ChanceWords — шанс словами для режима «повествование».
func ChanceWords(pct int) string {
	switch {
	case pct >= 60:
		return "легко"
	case pct >= 30:
		return "рискованно"
	case pct >= 10:
		return "очень рискованно"
	}
	return "почти невозможно"
}

// DifficultyWords — сложность словами для режима «повествование» (docs/game/checks.md: 5 обычно, 6–7 трудно,
// 8–9 очень трудно, 10 и выше героически).
func DifficultyWords(difficulty int) string {
	switch {
	case difficulty <= 5:
		return "обычно"
	case difficulty <= 7:
		return "трудно"
	case difficulty <= 9:
		return "очень трудно"
	}
	return "героически"
}

// Penalties — флаг правил: включены ли состояния-штрафы («истощён» за форсирование, состояние из on_fail).
// Выключен до механики отдыха, которая их снимает (B27, docs/game/open-questions.md); урон и факты провала остаются.
const Penalties = false

// ModePreset — что из механик включено в режиме игры (docs/game/game-modes.md).
type ModePreset struct {
	MaxDifficulty      int  // потолок базовой сложности (без локации); 0 — без потолка
	PushCost           bool // форсирование даёт состояние «истощён»
	AutoPush           bool // провал автоматически форсируется (повествование: без цены)
	FailAsCostly       bool // провал заменяется «успехом с ценой», кроме deadly
	ShowNumbers        bool // числа проверки в приватном итоге
	TrackHP            bool // столкновения со счётом HP
	InventoryLimit     bool
	RequireSkillChoice bool
}

// Preset возвращает пресет режима; неизвестный режим считается балансом.
func Preset(mode model.GameMode) ModePreset {
	switch mode {
	case model.ModeMechanics:
		return ModePreset{PushCost: true, ShowNumbers: true, TrackHP: true, InventoryLimit: true, RequireSkillChoice: true}
	case model.ModeNarrative:
		return ModePreset{MaxDifficulty: RiskDifficulty(model.RiskNormal), AutoPush: true, FailAsCostly: true}
	}
	return ModePreset{PushCost: true, TrackHP: true, InventoryLimit: true}
}

// Difficulty — итоговая сложность: базовая (подход или риск) с потолком режима плюс сложность локации.
func Difficulty(preset ModePreset, base, location int) int {
	if preset.MaxDifficulty > 0 && base > preset.MaxDifficulty {
		base = preset.MaxDifficulty
	}
	return max(base+location, 1)
}

// RoundsPerDay — раундов в сутки по расписанию игры: окно (или сутки) делённое на длительность раунда, не меньше 1.
func RoundsPerDay(roundMinutes, windowMinutes int) int {
	if windowMinutes <= 0 || windowMinutes > 24*60 {
		windowMinutes = 24 * 60
	}
	if roundMinutes <= 0 {
		roundMinutes = 15
	}
	return max(windowMinutes/roundMinutes, 1)
}

// DeathRounds — счётчик возврата после ненастоящей смерти: дни × раундов в сутки (docs/game/death.md).
func DeathRounds(days, roundsPerDay int) int {
	if days <= 0 {
		days = model.DeathDaysDefault
	}
	return max(days*max(roundsPerDay, 1), 1)
}
