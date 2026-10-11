package fake

import (
	"encoding/json"

	"taletogether/internal/model"
)

// BasicFantasy — модель персонажа basic_fantasy версии правил 1, как в миграции 00015 (docs/game/models/character-basic.md).
func BasicFantasy() model.CharacterModelDef {
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
			{Key: "diplomat", Title: "Дипломат", Attributes: map[string]int{"strength": 3, "agility": 2, "wits": 4, "will": 5}, Skills: map[string]int{"persuasion": 3, "awareness": 2, "survival": 1}, Items: []string{"seal", "coin_purse"}},
			{Key: "ranger", Title: "Следопыт", Attributes: map[string]int{"strength": 4, "agility": 3, "wits": 5, "will": 2}, Skills: map[string]int{"survival": 3, "stealth": 2, "melee": 1}, Items: []string{"bow", "rope"}},
		},
	}
}

// FantasyTemplate — шаблон жанра fantasy, как в миграциях 00003 и 00015 (сокращённый набор локаций).
func FantasyTemplate() model.GenreTemplate {
	return model.GenreTemplate{
		SchemaVersion:  1,
		CharacterModel: "basic_fantasy",
		Tone:           "Маленький город на краю леса. Приключение, лёгкая ирония, без мрачности.",
		Factions: []model.FactionTemplate{
			{Code: model.FactionGood, Title: "Орден Рассвета", Description: "Хранители города, верят в закон и свет.", Tone: "строгие, не безгрешные", Relations: map[string]string{"evil": "hostile"}},
			{Code: model.FactionEvil, Title: "Гильдия теней", Description: "Контрабандисты и шпионы; хотят свободы от Ордена.", Tone: "сторона конфликта, не злодеи", Relations: map[string]string{"good": "hostile"}},
		},
		Locations: []model.LocationTemplate{
			{Name: "Рыночная площадь", Description: "Сердце города.", Start: true, Links: []model.LocationLink{{To: "Зал Серой башни", Title: "к башне"}},
				Objects: []model.LocationObject{{Key: "notice_board", Title: "доска объявлений", Description: "Объявление о награде.", Approaches: []model.Approach{{Action: model.ActionExplore, Title: "разобрать приписку", Attribute: "wits", Skill: "awareness", Difficulty: 5, OnSuccess: json.RawMessage(`{"fact":"Гонца видели у башни."}`)}}}}},
			{Name: "Зал Серой башни", Description: "Пыльный зал.", Links: []model.LocationLink{{To: "Рыночная площадь", Title: "на площадь"}, {To: "Подвал башни", Title: "вниз в подвал"}}},
			{Name: "Подвал башни", Description: "Сырой подвал.", Difficulty: 1, Links: []model.LocationLink{{To: "Зал Серой башни", Title: "наверх в зал"}},
				Objects: []model.LocationObject{{Key: "cellar_door", Title: "запертая дверь", State: "locked", Approaches: []model.Approach{
					{Action: model.ActionInteract, Title: "выбить", Attribute: "strength", Skill: "athletics", Difficulty: 4, OnSuccess: json.RawMessage(`{"state":"broken"}`), OnFail: json.RawMessage(`{"condition":"exhausted"}`)},
					{Action: model.ActionInteract, Title: "отпереть", Attribute: "agility", Skill: "thievery", Difficulty: 4, OnSuccess: json.RawMessage(`{"state":"open"}`)},
				}}}},
		},
		OpeningScene: "В {{world_title}} наступает осень.",
		PartyNames:   []string{"Ночной дозор", "Вольные клинки", "Странники", "Искатели", "Тени башни"},
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
