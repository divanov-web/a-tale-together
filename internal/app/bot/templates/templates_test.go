package templates

import "testing"

// TestRender_Status — эталон сводки игры: одно поле на строку, пустые строки между блоками (B16).
func TestRender_Status(t *testing.T) {
	t.Parallel()
	type factionRow struct {
		Title, Code string
		Count       int
	}
	type charRow struct{ Nickname, Archetype, Faction, Status string }
	got, err := Render("status", map[string]any{
		"Title": "Мир", "Status": "идёт", "Genre": "Фэнтези", "Format": "novel", "Mode": "balanced", "SeasonNo": 1, "RulesVersion": 1,
		"RoundMinutes": 15, "MinAnswers": 2, "Timezone": "UTC", "PlayWindow": "без окна", "Limits": "до /finish", "MembersCount": 2, "CharactersCount": 1,
		"Factions": []factionRow{{"Орден", "good", 1}, {"Гильдия", "evil", 0}}, "Characters": []charRow{{"Элис", "warrior", "Орден", "active"}},
		"CurrentRound": "1, заявки до 12:15; заявок 0 из 2", "LastRound": "", "NextRound": "сразу после текущего, в 12:15", "JoinLink": "https://t.me/bot?start=join_X",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Мир «Мир» — идёт\n\nЖанр: Фэнтези\nФормат: novel\nРежим: balanced\nСезон: 1\nПравила: v1\nРаунд: 15 мин\nКворум: 2\nЧасовой пояс: UTC\nИгровое окно: без окна\nГраницы: до /finish\nИгроки: 2\nПерсонажей: 1\n\n" +
		"Фракции:\n• Орден (good): 1\n• Гильдия (evil): 0\n\nПерсонажи:\n• Элис — warrior, Орден, active\n\nТекущий раунд: 1, заявки до 12:15; заявок 0 из 2\nСледующий раунд: сразу после текущего, в 12:15\n\nПриглашение: https://t.me/bot?start=join_X\n"
	if got != want {
		t.Errorf("status =\n%q\nwant\n%q", got, want)
	}
}

// TestRender_Character — эталон карточки персонажа: одно поле на строку, знания списком.
func TestRender_Character(t *testing.T) {
	t.Parallel()
	got, err := Render("character", map[string]any{
		"Nickname": "Элис", "Archetype": "Воин", "Faction": "Орден", "Status": "active", "Death": "", "Game": "Мир", "Location": "Рыночная площадь",
		"HP": 9, "Conditions": "", "Attributes": "Сила 5, Ловкость 3, Разум 2, Воля 4", "Skills": "Атлетика 3, Бой 3", "Inventory": "Меч, Щит",
		"PublicBio": "Высокая и шумная", "SecretBio": "", "Knowledge": []string{"Гонца видели у башни."},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Элис — Воин\nФракция: «Орден»\nСтатус: active\nИгра: «Мир»\nЛокация: Рыночная площадь\nHP: 9\nСостояния: нет\nХарактеристики: Сила 5, Ловкость 3, Разум 2, Воля 4\nНавыки: Атлетика 3, Бой 3\nИнвентарь: Меч, Щит\n" +
		"Открытое описание: Высокая и шумная\nСкрытое описание: нет (/secret <текст>)\n\nЗнания:\n• Гонца видели у башни.\n\n"
	if got != want {
		t.Errorf("character =\n%q\nwant\n%q", got, want)
	}
}
