package fake

import "taletogether/internal/model"

// BasicFantasy — модель персонажа basic_fantasy, как в миграции 00003.
func BasicFantasy() model.CharacterModelDef {
	return model.CharacterModelDef{
		SchemaVersion: 1,
		Attributes: []model.AttributeDef{
			{Key: "strength", Title: "Сила", Min: 2, Max: 4},
			{Key: "agility", Title: "Ловкость", Min: 2, Max: 4},
			{Key: "wits", Title: "Разум", Min: 2, Max: 4},
			{Key: "will", Title: "Воля", Min: 2, Max: 4},
		},
		AttributePoints: 12,
		Skills: []model.SkillDef{
			{Key: "thievery", Title: "Воровство", Attribute: "agility"},
			{Key: "awareness", Title: "Внимательность", Attribute: "wits"},
			{Key: "persuasion", Title: "Убеждение", Attribute: "will"},
			{Key: "survival", Title: "Выживание", Attribute: "wits"},
			{Key: "stealth", Title: "Скрытность", Attribute: "agility"},
			{Key: "melee", Title: "Бой", Attribute: "strength"},
		},
		SkillPoints:    6,
		SkillMax:       3,
		HPFormula:      "4 + strength",
		InventoryLimit: 6,
		Archetypes: []model.ArchetypeDef{
			{Key: "warrior", Title: "Воин", Attributes: map[string]int{"strength": 4, "agility": 3, "wits": 2, "will": 3}, Skills: map[string]int{"melee": 3, "survival": 2, "awareness": 1}, Items: []string{"sword", "shield"}},
			{Key: "rogue", Title: "Плут", Attributes: map[string]int{"strength": 2, "agility": 4, "wits": 3, "will": 3}, Skills: map[string]int{"thievery": 3, "stealth": 2, "awareness": 1}, Items: []string{"dagger", "lockpicks"}},
			{Key: "mage", Title: "Маг", Attributes: map[string]int{"strength": 2, "agility": 2, "wits": 4, "will": 4}, Skills: map[string]int{"awareness": 3, "persuasion": 2, "survival": 1}, Items: []string{"staff", "tome"}},
			{Key: "diplomat", Title: "Дипломат", Attributes: map[string]int{"strength": 2, "agility": 3, "wits": 3, "will": 4}, Skills: map[string]int{"persuasion": 3, "awareness": 2, "thievery": 1}, Items: []string{"seal", "coin_purse"}},
			{Key: "ranger", Title: "Следопыт", Attributes: map[string]int{"strength": 3, "agility": 4, "wits": 3, "will": 2}, Skills: map[string]int{"survival": 3, "stealth": 2, "melee": 1}, Items: []string{"bow", "rope"}},
		},
	}
}

// FantasyTemplate — шаблон жанра fantasy, как в миграции 00003 (сокращённый каталог предметов).
func FantasyTemplate() model.GenreTemplate {
	return model.GenreTemplate{
		SchemaVersion:  1,
		CharacterModel: "basic_fantasy",
		Tone:           "Маленький город на краю леса. Приключение, лёгкая ирония, без мрачности.",
		Factions: []model.FactionTemplate{
			{Code: model.FactionGood, Title: "Орден Рассвета", Description: "Хранители города, верят в закон и свет.", Tone: "строгие, не безгрешные", Relations: map[string]string{"evil": "hostile", "neutral": "neutral"}},
			{Code: model.FactionEvil, Title: "Гильдия теней", Description: "Контрабандисты и шпионы; хотят свободы от Ордена.", Tone: "сторона конфликта, не злодеи", Relations: map[string]string{"good": "hostile", "neutral": "neutral"}},
			{Code: model.FactionNeutral, Title: "Вольные", Description: "Торговцы и наёмники, выгода превыше флага.", Tone: "циничные посредники", Relations: map[string]string{"good": "neutral", "evil": "neutral"}},
		},
		OpeningScene: "В {{world_title}} наступает осень.",
		Items: []model.ItemTemplate{
			{Key: "sword", Title: "Меч", Tags: []string{"weapon", "melee"}},
			{Key: "shield", Title: "Щит", Tags: []string{"armor"}},
			{Key: "dagger", Title: "Кинжал", Tags: []string{"weapon", "melee"}},
			{Key: "lockpicks", Title: "Отмычки", Tags: []string{"tool"}},
			{Key: "staff", Title: "Посох", Tags: []string{"weapon", "focus"}},
			{Key: "tome", Title: "Фолиант", Tags: []string{"book"}},
			{Key: "seal", Title: "Печать гильдии", Tags: []string{"token"}},
			{Key: "coin_purse", Title: "Кошель", Tags: []string{"valuable"}},
			{Key: "bow", Title: "Лук", Tags: []string{"weapon", "ranged"}},
			{Key: "rope", Title: "Верёвка", Tags: []string{"tool"}},
		},
	}
}
