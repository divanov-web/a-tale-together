package rules

import (
	"strings"

	"taletogether/internal/model"
)

// CatalogEntry — описание типа действия каталога (docs/game/actions.md).
type CatalogEntry struct {
	Type      model.ActionType
	Title     string
	Supported bool   // разрешается движком в текущей версии
	Later     string // пункт плана, где появится
}

// Catalog — каталог действий в порядке фаз разрешения.
var Catalog = []CatalogEntry{
	{Type: model.ActionHelp, Title: "помочь", Supported: true},
	{Type: model.ActionWait, Title: "ждать", Supported: true},
	{Type: model.ActionSneak, Title: "красться", Later: "12"},
	{Type: model.ActionMove, Title: "идти", Supported: true},
	{Type: model.ActionExplore, Title: "осмотреть", Supported: true},
	{Type: model.ActionInteract, Title: "взаимодействовать", Supported: true},
	{Type: model.ActionPersuade, Title: "убедить", Later: "13"},
	{Type: model.ActionTrade, Title: "обменяться", Later: "12"},
	{Type: model.ActionAttack, Title: "атаковать", Later: "12"},
}

// Entry возвращает запись каталога по типу.
func Entry(t model.ActionType) (CatalogEntry, bool) {
	for _, e := range Catalog {
		if e.Type == t {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

// typedVerbs — слова типовых команд, которые разбираются кодом без LLM (`/act идти north`, `/act wait`).
var typedVerbs = map[string]model.ActionType{
	"move": model.ActionMove, "go": model.ActionMove, "идти": model.ActionMove, "иду": model.ActionMove, "пойти": model.ActionMove, "перейти": model.ActionMove,
	"explore": model.ActionExplore, "look": model.ActionExplore, "осмотреть": model.ActionExplore, "осматриваю": model.ActionExplore, "изучить": model.ActionExplore, "обыскать": model.ActionExplore,
	"interact": model.ActionInteract, "use": model.ActionInteract, "открыть": model.ActionInteract, "открываю": model.ActionInteract, "взломать": model.ActionInteract, "выбить": model.ActionInteract, "нажать": model.ActionInteract, "дёрнуть": model.ActionInteract,
	"wait": model.ActionWait, "ждать": model.ActionWait, "жду": model.ActionWait, "отдыхать": model.ActionWait,
	"help": model.ActionHelp, "помочь": model.ActionHelp, "помогаю": model.ActionHelp,
	"attack": model.ActionAttack, "атаковать": model.ActionAttack, "атакую": model.ActionAttack, "ударить": model.ActionAttack,
	"persuade": model.ActionPersuade, "убедить": model.ActionPersuade, "уговорить": model.ActionPersuade,
	"sneak": model.ActionSneak, "красться": model.ActionSneak, "крадусь": model.ActionSneak,
	"trade": model.ActionTrade, "обменять": model.ActionTrade,
}

// ParseTyped разбирает типовую команду «глагол [цель] [force]» в намерение; свободный текст
// возвращает ambiguous с подсказкой — до интерпретатора LLM (пункт 08). Текст игрока остаётся данными в RawText.
func ParseTyped(sub model.Submission) model.Intent {
	in := model.Intent{SubmissionID: sub.ID, CharacterID: sub.CharacterID, RawText: sub.RawText}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(sub.RawText)))
	if len(fields) == 0 {
		in.Ambiguities = []string{"пустая заявка"}
		return in
	}
	verb := strings.Trim(fields[0], ".,!:;")
	t, ok := typedVerbs[verb]
	if !ok {
		in.Ambiguities = []string{"свободный текст пока не разбирается: используйте типовую команду, например «идти <выход>», «осмотреть <объект>», «открыть <объект>», «помочь <персонаж>», «ждать»"}
		return in
	}
	in.ActionType = t
	rest := fields[1:]
	if n := len(rest); n > 0 && (rest[n-1] == "force" || rest[n-1] == "форс" || rest[n-1] == "форсировать") {
		in.Push = true
		rest = rest[:n-1]
	}
	in.Target = strings.Trim(strings.Join(rest, " "), "«»\"'.,!")
	return in
}
