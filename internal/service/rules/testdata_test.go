package rules

import "taletogether/internal/model"

// basicFantasy — копия модели basic_fantasy версии 1 (миграция 00015) для тестов.
func basicFantasy() model.CharacterModelDef {
	return model.CharacterModelDef{
		SchemaVersion: 1,
		Attributes: []model.AttributeDef{
			{Key: "strength", Title: "Сила", Min: 2, Max: 5},
			{Key: "agility", Title: "Ловкость", Min: 2, Max: 5},
			{Key: "wits", Title: "Разум", Min: 2, Max: 5},
			{Key: "will", Title: "Воля", Min: 2, Max: 5},
		},
		AttributePoints: 14,
		Skills: []model.SkillDef{
			{Key: "athletics", Title: "Атлетика", Attribute: "strength"},
			{Key: "melee", Title: "Бой", Attribute: "strength"},
			{Key: "thievery", Title: "Воровство", Attribute: "agility"},
			{Key: "stealth", Title: "Скрытность", Attribute: "agility"},
			{Key: "awareness", Title: "Внимательность", Attribute: "wits"},
			{Key: "survival", Title: "Выживание", Attribute: "wits"},
			{Key: "persuasion", Title: "Убеждение", Attribute: "will"},
		},
		SkillPoints:    6,
		SkillMax:       3,
		HPFormula:      "4 + strength",
		InventoryLimit: 6,
		Archetypes: []model.ArchetypeDef{
			{Key: "warrior", Title: "Воин", Attributes: map[string]int{"strength": 5, "agility": 3, "wits": 2, "will": 4}, Skills: map[string]int{"athletics": 3, "melee": 3}, Items: []string{"sword", "shield"}},
			{Key: "rogue", Title: "Плут", Attributes: map[string]int{"strength": 2, "agility": 5, "wits": 4, "will": 3}, Skills: map[string]int{"thievery": 3, "stealth": 3}, Items: []string{"dagger", "lockpicks"}},
			{Key: "mage", Title: "Маг", Attributes: map[string]int{"strength": 2, "agility": 3, "wits": 5, "will": 4}, Skills: map[string]int{"awareness": 3, "persuasion": 2, "survival": 1}, Items: []string{"staff", "tome"}},
			{Key: "bard", Title: "Бард", Attributes: map[string]int{"strength": 3, "agility": 2, "wits": 4, "will": 5}, Skills: map[string]int{"persuasion": 3, "awareness": 2, "survival": 1}, Items: []string{"lute", "coin_purse"}},
			{Key: "ranger", Title: "Следопыт", Attributes: map[string]int{"strength": 4, "agility": 3, "wits": 5, "will": 2}, Skills: map[string]int{"survival": 3, "stealth": 2, "melee": 1}, Items: []string{"bow", "rope"}},
		},
	}
}
