// Package party — чистые правила групп персонажей (пункт 19, docs/game/parties.md): выбор лидера, имя новой группы,
// ближайшая группа по карте локаций. Без I/O; состояние групп меняют сценарии `app/game` и `app/round`, движок
// `service/engine` создаёт группы событиями `party_split`.
package party

import (
	"fmt"
	"strings"

	"taletogether/internal/model"
)

// Leader — лидер группы по правилу пункта 19: персонаж администратора игры среди участников, иначе первый по времени
// создания (порядок members); выбывшие (`inactive`, `dead`) берутся только когда других нет. false — участников нет.
func Leader(members []model.Character, admin model.UserID) (model.CharacterID, bool) {
	var first, firstPresent, adminPresent *model.Character
	for i := range members {
		c := &members[i]
		if first == nil {
			first = c
		}
		if c.Status.Absent() {
			continue
		}
		if firstPresent == nil {
			firstPresent = c
		}
		if c.UserID == admin && adminPresent == nil {
			adminPresent = c
		}
	}
	switch {
	case adminPresent != nil:
		return adminPresent.ID, true
	case firstPresent != nil:
		return firstPresent.ID, true
	case first != nil:
		return first.ID, true
	}
	return 0, false
}

// SplitLeader — лидер отделившихся при `party_split`: наибольшее значение `model.LeaderAttribute` (Воля), при
// равенстве первый по порядку снимка (порядок создания). false — список пуст.
func SplitLeader(members []model.SnapshotCharacter) (model.CharacterID, bool) {
	best := -1
	var id model.CharacterID
	for _, c := range members {
		if v := c.Attributes[model.LeaderAttribute]; v > best {
			best, id = v, c.ID
		}
	}
	return id, best >= 0
}

// NextName — имя новой группы: первое имя шаблона, не занятое (без учёта регистра) существующими группами,
// иначе «Группа N» с первым свободным N.
func NextName(templates, taken []string) string {
	used := func(name string) bool {
		for _, t := range taken {
			if strings.EqualFold(strings.TrimSpace(t), name) {
				return true
			}
		}
		return false
	}
	for _, name := range templates {
		name = strings.TrimSpace(name)
		if name != "" && !used(name) {
			return name
		}
	}
	for n := len(taken) + 1; ; n++ {
		if name := fmt.Sprintf("Группа %d", n); !used(name) {
			return name
		}
	}
}

// ValidateName проверяет имя группы, заданное игроком: длина и отсутствие слеша в начале.
func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if n := len([]rune(name)); n < model.PartyNameMin || n > model.PartyNameMax {
		return fmt.Errorf("имя группы от %d до %d символов", model.PartyNameMin, model.PartyNameMax)
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "#") {
		return fmt.Errorf("имя группы не может начинаться с «/» или «#»")
	}
	return nil
}

// Nearest — ближайшая к from локация из candidates по видимым выходам (поиск в ширину); при равной дальности —
// первая по порядку candidates. false — ни одна недостижима.
func Nearest(locations []model.Location, from model.LocationID, candidates []model.LocationID) (model.LocationID, bool) {
	byName := map[string]model.LocationID{}
	byID := map[model.LocationID]model.Location{}
	for _, l := range locations {
		byName[strings.ToLower(l.Name)] = l.ID
		byID[l.ID] = l
	}
	dist := map[model.LocationID]int{from: 0}
	queue := []model.LocationID{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, link := range byID[cur].Links {
			to, ok := byName[strings.ToLower(link.To)]
			if !ok {
				continue
			}
			if _, seen := dist[to]; seen {
				continue
			}
			dist[to] = dist[cur] + 1
			queue = append(queue, to)
		}
	}
	best, found := model.LocationID(0), false
	bestDist := 0
	for _, id := range candidates {
		d, ok := dist[id]
		if !ok {
			continue
		}
		if !found || d < bestDist {
			best, bestDist, found = id, d, true
		}
	}
	return best, found
}

// Active — число активных групп.
func Active(parties []model.Party) int {
	n := 0
	for _, p := range parties {
		if p.Status == model.PartyActive {
			n++
		}
	}
	return n
}

// Find — активная группа по имени (без учёта регистра) или ссылке `#id`.
func Find(parties []model.Party, ref string) (model.Party, bool) {
	ref = strings.TrimSpace(ref)
	for _, p := range parties {
		if p.Status != model.PartyActive {
			continue
		}
		if strings.EqualFold(p.Name, ref) || ref == fmt.Sprintf("#%d", p.ID) {
			return p, true
		}
	}
	return model.Party{}, false
}
