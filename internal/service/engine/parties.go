package engine

import (
	"fmt"
	"sort"
	"strings"

	"taletogether/internal/model"
	"taletogether/internal/service/party"
)

// proposal — заявленный переход персонажа до сверки с его группой.
type proposal struct {
	submission model.SubmissionID
	character  model.CharacterID
	from, to   model.LocationID
}

// settleMoves сверяет заявленные переходы с группами (сценарий S14, docs/game/parties.md). Персонаж без группы
// переходит сразу. Участники группы, оказавшиеся по итогам заявок в разных локациях, расходятся: большинство
// остаётся исходной группой, остальные по локациям назначения становятся новыми группами (`party_split`), пока
// свободны лимиты групп и локаций. Первая блокировка лимитом удерживает группу (`party_held`): все, кто ещё в ней,
// идут туда, куда заявил лидер (без лидера — большинство); заявки на другое направление получают исход «удержан»,
// участники без заявки на переход уходят вместе с группой.
func (r *resolver) settleMoves() {
	props := r.proposals
	r.proposals = nil
	if len(props) == 0 {
		return
	}
	proposed := map[model.CharacterID]model.LocationID{}
	for _, p := range props {
		proposed[p.character] = p.to
	}
	resultOf := func(sub model.SubmissionID) *model.ActionResult {
		for i := range r.results {
			if r.results[i].SubmissionID == sub {
				return &r.results[i]
			}
		}
		return nil
	}
	// Решение по каждому персонажу: куда он в итоге попадает.
	final := map[model.CharacterID]model.LocationID{}
	for i := range r.snap.Characters {
		c := &r.snap.Characters[i]
		if c.LocationID == nil {
			continue
		}
		if to, ok := proposed[c.ID]; ok {
			final[c.ID] = to
		} else {
			final[c.ID] = *c.LocationID
		}
	}
	held := map[model.CharacterID]model.LocationID{} // персонаж → куда его уводит группа
	for pi := range r.snap.Parties {
		pt := r.snap.Parties[pi]
		var members []*model.SnapshotCharacter
		for i := range r.snap.Characters {
			c := &r.snap.Characters[i]
			if c.PartyID != nil && *c.PartyID == pt.ID && !c.Status.Absent() && c.LocationID != nil {
				members = append(members, c)
			}
		}
		if len(members) == 0 {
			continue
		}
		groups := map[model.LocationID][]*model.SnapshotCharacter{}
		var order []model.LocationID
		for _, m := range members {
			to := final[m.ID]
			if _, ok := groups[to]; !ok {
				order = append(order, to)
			}
			groups[to] = append(groups[to], m)
		}
		if len(groups) < 2 {
			continue
		}
		origin := *members[0].LocationID
		main := mainGroup(pt, groups, order, origin)
		others := make([]model.LocationID, 0, len(order))
		for _, id := range order {
			if id != main {
				others = append(others, id)
			}
		}
		sort.SliceStable(others, func(i, j int) bool {
			if len(groups[others[i]]) != len(groups[others[j]]) {
				return len(groups[others[i]]) > len(groups[others[j]])
			}
			return others[i] < others[j]
		})
		// Отделение по локациям назначения, пока лимиты свободны; первая блокировка удерживает всех оставшихся вместе.
		reason := ""
		for _, loc := range others {
			if reason = r.splitBlocked(final); reason != "" {
				break
			}
			r.split(&r.snap.Parties[pi], groups[loc], origin, loc)
		}
		if reason == "" {
			continue
		}
		direction := main
		if lead := r.snap.Parties[pi].LeaderID; lead != nil {
			if to, ok := final[*lead]; ok {
				direction = to // лидер остался в группе: группа идёт за ним
			}
		}
		var heldList []map[string]any
		for _, m := range members {
			if *m.PartyID != pt.ID || final[m.ID] == direction {
				continue
			}
			heldList = append(heldList, map[string]any{"character_id": m.ID, "nickname": m.Nickname, "wanted": r.locName(final[m.ID])})
			held[m.ID] = direction
			final[m.ID] = direction
		}
		if len(heldList) == 0 {
			continue
		}
		payload := map[string]any{"party_id": pt.ID, "party": pt.Name, "reason": reason, "held": heldList, "from": origin, "from_name": r.locName(origin)}
		if direction != origin {
			payload["to"], payload["to_name"] = direction, r.locName(direction)
		}
		r.emitAt(origin, model.EventPartyHeld, payload)
	}
	// Переходы: принятые заявки, затем участники, которых группа уводит с собой.
	for _, p := range props {
		res := resultOf(p.submission)
		c, _ := r.snap.Character(p.character)
		if c == nil || c.LocationID == nil {
			continue
		}
		target, wasHeld := held[p.character]
		if !wasHeld {
			target = p.to
		}
		from, _ := r.snap.Location(*c.LocationID)
		dest, _ := r.snap.Location(target)
		if from == nil || dest == nil {
			continue
		}
		if wasHeld && res != nil {
			pt := r.partyName(c)
			res.Outcome = model.OutcomeHeld
			if dest.ID == from.ID {
				res.Summary = fmt.Sprintf("остаётся с группой «%s» в «%s»: в «%s» не ушёл", pt, from.Name, r.locName(p.to))
			} else {
				res.Summary = fmt.Sprintf("остаётся с группой «%s»: группа идёт в «%s», а не в «%s»", pt, dest.Name, r.locName(p.to))
			}
		}
		if dest.ID != from.ID {
			r.moveTo(res, c, from, dest)
		}
		delete(held, p.character)
	}
	for i := range r.snap.Characters {
		c := &r.snap.Characters[i]
		target, ok := held[c.ID]
		if !ok || c.LocationID == nil || *c.LocationID == target {
			continue
		}
		from, _ := r.snap.Location(*c.LocationID)
		dest, _ := r.snap.Location(target)
		if from != nil && dest != nil {
			r.moveTo(nil, c, from, dest)
		}
	}
}

// mainGroup — кто остаётся исходной группой при расхождении: большинство; при равенстве — те, кто остаётся в
// текущей локации, затем группа лидера, затем меньший id локации. Направление лидера решает только удержание.
func mainGroup(pt model.SnapshotParty, groups map[model.LocationID][]*model.SnapshotCharacter, order []model.LocationID, origin model.LocationID) model.LocationID {
	leaderIn := func(id model.LocationID) bool {
		if pt.LeaderID == nil {
			return false
		}
		for _, m := range groups[id] {
			if m.ID == *pt.LeaderID {
				return true
			}
		}
		return false
	}
	best := order[0]
	for _, id := range order[1:] {
		switch {
		case len(groups[id]) > len(groups[best]):
			best = id
		case len(groups[id]) < len(groups[best]):
		case id == origin:
			best = id
		case best == origin:
		case leaderIn(id) && !leaderIn(best):
			best = id
		case !leaderIn(best) && id < best:
			best = id
		}
	}
	return best
}

// splitBlocked — причина, по которой новую группу создать нельзя: лимит групп (или нет зарезервированных
// идентификаторов) либо лимит занятых локаций по итоговым позициям всех присутствующих; пусто — разделение возможно.
func (r *resolver) splitBlocked(final map[model.CharacterID]model.LocationID) string {
	if len(r.snap.NewPartyIDs) == 0 || (r.snap.MaxParties > 0 && len(r.snap.Parties) >= r.snap.MaxParties) {
		return model.HeldPartyLimit
	}
	if r.snap.MaxActiveLocations > 0 {
		occupied := map[model.LocationID]bool{}
		for i := range r.snap.Characters {
			c := &r.snap.Characters[i]
			if c.Status.Absent() {
				continue
			}
			if loc, ok := final[c.ID]; ok {
				occupied[loc] = true
			}
		}
		if len(occupied) > r.snap.MaxActiveLocations {
			return model.HeldLocationLimit
		}
	}
	return ""
}

// split выделяет участников в новую группу с зарезервированным идентификатором, именем из шаблона и лидером по
// наибольшей Воле; событие `party_split` видно в исходной локации, состояние меняется здесь и в Apply одинаково.
func (r *resolver) split(from *model.SnapshotParty, members []*model.SnapshotCharacter, origin, to model.LocationID) {
	id := r.snap.NewPartyIDs[0]
	r.snap.NewPartyIDs = r.snap.NewPartyIDs[1:]
	taken := make([]string, 0, len(r.snap.Parties))
	for _, p := range r.snap.Parties {
		taken = append(taken, p.Name)
	}
	name := party.NextName(r.snap.PartyNames, taken)
	values := make([]model.SnapshotCharacter, 0, len(members))
	ids := make([]model.CharacterID, 0, len(members))
	nicks := make([]string, 0, len(members))
	for _, m := range members {
		values, ids, nicks = append(values, *m), append(ids, m.ID), append(nicks, m.Nickname)
	}
	leader, _ := party.SplitLeader(values)
	payload := map[string]any{"party_id": from.ID, "party": from.Name, "new_party_id": id, "new_party": name, "leader_id": leader, "members": ids, "nicknames": nicks,
		"from": origin, "from_name": r.locName(origin), "to": to, "to_name": r.locName(to)}
	r.emitAt(origin, model.EventPartySplit, payload)
	applySplit(&r.snap, id, name, leader, ids)
	if from.LeaderID != nil {
		for _, m := range members {
			if m.ID == *from.LeaderID {
				from.LeaderID = nil // лидер ушёл с отделившимися; преемника назначит сценарий раунда
			}
		}
	}
}

// applySplit — общая для движка и свёртки смена состояния при `party_split`.
func applySplit(snap *model.RoundSnapshot, id model.PartyID, name string, leader model.CharacterID, members []model.CharacterID) {
	pid := id
	for _, mid := range members {
		if c, ok := snap.Character(mid); ok {
			c.PartyID = &pid
		}
	}
	lid := leader
	snap.Parties = append(snap.Parties, model.SnapshotParty{ID: id, Name: name, LeaderID: &lid})
}

// emitAt добавляет событие группы с видимостью в локации; персонажа у события нет.
func (r *resolver) emitAt(loc model.LocationID, t model.EventType, payload map[string]any) {
	r.emit(nil, t, model.VisibilityLocation, nil, nil, payload)
	id := loc
	r.events[len(r.events)-1].LocationID = &id
}

func (r *resolver) locName(id model.LocationID) string {
	if l, ok := r.snap.Location(id); ok {
		return l.Name
	}
	return fmt.Sprint(id)
}

func (r *resolver) partyName(c *model.SnapshotCharacter) string {
	if c.PartyID != nil {
		if p, ok := r.snap.Party(*c.PartyID); ok {
			return p.Name
		}
	}
	return ""
}

// PartyNames — имена групп снимка через запятую (для журнала и тестов).
func PartyNames(snap model.RoundSnapshot) string {
	names := make([]string, 0, len(snap.Parties))
	for _, p := range snap.Parties {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}
