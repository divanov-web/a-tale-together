package rules

import (
	"math"

	"taletogether/internal/model"
)

// Version — версия правил, закрепляется за игрой (`games.rules_version`, G10).
const Version = 0

// Сложность свободного действия по оценке риска (docs/game/checks.md): требуемое число успехов.
var riskDifficulty = map[model.Risk]int{model.RiskNormal: 1, model.RiskHard: 2, model.RiskExtreme: 3}

// RiskDifficulty — требуемое число успехов по риску; неизвестный риск считается обычным.
func RiskDifficulty(r model.Risk) int {
	if d, ok := riskDifficulty[r]; ok {
		return d
	}
	return 1
}

// Состояния, снимающие по кубику с пула (docs/game/character.md).
var penaltyConditions = []string{model.ConditionWounded, model.ConditionExhausted}

// Pool — пул кубиков персонажа: характеристика + навык + бонус предметов (не более 2) − штрафы состояний − помощь (+1 за помощника, не более +2). Не меньше 1.
func Pool(c model.SnapshotCharacter, attribute, skill string, helpers int) int {
	pool := c.Attributes[attribute]
	if skill != "" {
		pool += c.Skills[skill]
	}
	bonus := 0
	for _, it := range c.Inventory {
		if it.Bonus != nil && skill != "" && it.Bonus.Skill == skill {
			bonus += it.Bonus.Dice
		}
	}
	pool += min(bonus, 2)
	for _, cond := range penaltyConditions {
		if c.HasCondition(cond) {
			pool--
		}
	}
	pool += min(helpers, 2)
	return max(pool, 1)
}

// ChancePct — вероятность хотя бы need успехов («6») на пуле из pool d6, в процентах.
func ChancePct(pool, need int) int {
	if need <= 0 {
		return 100
	}
	if pool < need {
		return 0
	}
	p := 0.0
	for k := need; k <= pool; k++ {
		p += binomial(pool, k) * math.Pow(1.0/6, float64(k)) * math.Pow(5.0/6, float64(pool-k))
	}
	return int(math.Round(p * 100))
}

func binomial(n, k int) float64 {
	r := 1.0
	for i := 1; i <= k; i++ {
		r = r * float64(n-k+i) / float64(i)
	}
	return r
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
		return ModePreset{MaxDifficulty: 1, AutoPush: true, FailAsCostly: true}
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
