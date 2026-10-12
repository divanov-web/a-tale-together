package templates

import "testing"

// TestRender_Status — эталон сводки игры: одно поле на строку, пустые строки между блоками (B16).
func TestRender_Status(t *testing.T) {
	t.Parallel()
	type factionRow struct {
		Title, Code string
		Count       int
	}
	type charRow struct{ Nickname, Archetype, Gender, Faction, Status string }
	got, err := Render("status", map[string]any{
		"Title": "Мир", "Status": "идёт", "Genre": "Фэнтези", "Format": "novel", "Mode": "balanced", "SeasonNo": 1, "RulesVersion": 1,
		"RoundMinutes": 15, "MinAnswers": 2, "Timezone": "UTC", "PlayWindow": "без окна", "Limits": "до /finish", "MembersCount": 2, "CharactersCount": 2,
		"Factions": []factionRow{{"Орден", "good", 1}, {"Гильдия", "evil", 0}}, "Characters": []charRow{{"Элис", "warrior", "женский", "Орден", "active"}, {"Боб", "rogue", "", "Гильдия", "waiting"}},
		"CurrentRound": "1, заявки до 12:15; заявок 0 из 2", "LastRound": "", "NextRound": "сразу после текущего, в 12:15", "JoinLink": "https://t.me/bot?start=join_X",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Мир «Мир» — идёт\n\nЖанр: Фэнтези\nФормат: novel\nРежим: balanced\nСезон: 1\nПравила: v1\nРаунд: 15 мин\nКворум: 2\nЧасовой пояс: UTC\nИгровое окно: без окна\nГраницы: до /finish\nИгроки: 2\nПерсонажей: 2\n\n" +
		"Фракции:\n• Орден (good): 1\n• Гильдия (evil): 0\n\nПерсонажи:\n• Элис — warrior, женский, Орден, active\n• Боб — rogue, Гильдия, waiting\nГруппы: пока нет\n\nТекущий раунд: 1, заявки до 12:15; заявок 0 из 2\nСледующий раунд: сразу после текущего, в 12:15\n\nПриглашение: https://t.me/bot?start=join_X\n"
	if got != want {
		t.Errorf("status =\n%q\nwant\n%q", got, want)
	}
}

// TestRender_Character — эталон краткой карточки персонажа: одно поле на строку, описания и знания отдельными командами (B28).
func TestRender_Character(t *testing.T) {
	t.Parallel()
	got, err := Render("character", map[string]any{
		"Nickname": "Элис", "Gender": "женский", "Archetype": "Воин", "Faction": "Орден", "Status": "active", "Death": "", "Game": "Мир", "Location": "Рыночная площадь",
		"HP": 9, "Conditions": "", "Attributes": "Сила 5, Ловкость 3, Разум 2, Воля 4", "Skills": "Атлетика 3, Бой 3", "Inventory": "Меч, Щит",
		"Knowledge": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Элис — Воин\nПол: женский\nФракция: «Орден»\nСтатус: active\nИгра: «Мир»\nЛокация: Рыночная площадь\nГруппа: нет (/party)\nHP: 9\nСостояния: нет\nХарактеристики: Сила 5, Ловкость 3, Разум 2, Воля 4\nНавыки: Атлетика 3, Бой 3\nИнвентарь: Меч, Щит\n" +
		"Описания: /bio\nЗнания: /knowledge (1)\n"
	if got != want {
		t.Errorf("character =\n%q\nwant\n%q", got, want)
	}
}
