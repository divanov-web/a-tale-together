package round

import (
	"fmt"
	"strings"
	"time"

	"taletogether/internal/model"
)

// SkippedText — сообщение о пропуске раунда без кворума; на время разработки отправляется в группу
// (решение руководителя 2026-10-10), в prod раунд пропускается молча.
const SkippedText = "Недостаточное количество участников. Раунд пропущен"

// FormatTime — время в часовом поясе игры: «15:04» в тот же день, иначе «02.01 15:04».
func FormatTime(g model.Game, t, now time.Time) string {
	loc := location(g.Timezone)
	lt, ln := t.In(loc), now.In(loc)
	if lt.YearDay() == ln.YearDay() && lt.Year() == ln.Year() {
		return lt.Format("15:04")
	}
	return lt.Format("02.01 15:04")
}

// header — подпись мира и раунда в каждом игровом сообщении.
func header(g model.Game, roundNo int) string {
	return fmt.Sprintf("«%s» — раунд %d", g.Title, roundNo)
}

// openingScene подставляет название мира и имена фракций в стартовую сцену шаблона жанра.
func openingScene(g model.Game, template string, factions []model.Faction) string {
	r := strings.NewReplacer("{{world_title}}", g.Title)
	text := r.Replace(template)
	for _, f := range factions {
		text = strings.ReplaceAll(text, "{{faction_"+string(f.Code)+"}}", f.Title)
	}
	if strings.TrimSpace(text) == "" {
		text = "История начинается."
	}
	return fmt.Sprintf("«%s» — начало истории\n%s", g.Title, text)
}

func roundOpenText(g model.Game, r model.Round, carried int, now time.Time) string {
	text := fmt.Sprintf("%s открыт. Заявки до %s: /act <текст> в личке с ботом.", header(g, r.RoundNo), FormatTime(g, r.DeadlineAt, now))
	if carried > 0 {
		text += fmt.Sprintf(" Перенесено заявок из пропущенного раунда: %d.", carried)
	}
	return text
}

func roundFrozenText(g model.Game, r model.Round, submissions int) string {
	return fmt.Sprintf("%s закрыт, заявок: %d. Разрешение раундов ещё в разработке.", header(g, r.RoundNo), submissions)
}

func roundSkippedText(g model.Game, r model.Round) string {
	return fmt.Sprintf("%s. %s.", header(g, r.RoundNo), SkippedText)
}

func pausedText(g model.Game) string {
	return fmt.Sprintf("«%s» — игра на паузе. Раунды не открываются; возобновить: /pause.", g.Title)
}

func resumedText(g model.Game, r *model.Round, next *time.Time, now time.Time) string {
	if r != nil {
		return fmt.Sprintf("%s продолжается. Заявки до %s.", header(g, r.RoundNo), FormatTime(g, r.DeadlineAt, now))
	}
	if next != nil {
		return fmt.Sprintf("«%s» — игра возобновлена. Следующий раунд в %s.", g.Title, FormatTime(g, *next, now))
	}
	return fmt.Sprintf("«%s» — игра возобновлена.", g.Title)
}

func firstRoundLaterText(g model.Game, next time.Time, now time.Time) string {
	return fmt.Sprintf("«%s» — раунды открываются в игровое окно %s–%s. Первый раунд в %s.", g.Title, g.PlayFrom, g.PlayTo, FormatTime(g, next, now))
}
