// Package engine — детерминированное разрешение раунда (пункт 07): снимок + намерения + seed → события и итоги заявок.
// Чистые функции без I/O; каждая заявка получает ровно один статус; состояние меняется только применением событий (apply.go).
package engine

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"

	"taletogether/internal/model"
	"taletogether/internal/service/rules"
)

// Result — выход движка: события в порядке возникновения и итог каждой заявки.
type Result struct {
	Events  []model.Event
	Results []model.ActionResult
}

// Resolve разрешает раунд. Фазы (docs/game/round.md): подготовка (help, wait) → перемещение → взаимодействие
// (explore, interact) → итоги (возврат после ненастоящей смерти). Перемещения участников группы сверяются после
// фазы перемещения (`settleMoves`, S14): расхождение локаций даёт `party_split` в пределах лимитов или `party_held`
// с приоритетом направления лидера. Броски d100 из rand.New(rand.NewPCG(seed, seed>>1)).
func Resolve(snap model.RoundSnapshot, intents []model.Intent, seed int64) Result {
	r := &resolver{snap: Clone(snap), rng: rand.New(rand.NewPCG(uint64(seed), uint64(seed)>>1)), preset: rules.Preset(snap.Mode), helpers: map[model.CharacterID]int{}}
	ordered := append([]model.Intent(nil), intents...)
	sort.SliceStable(ordered, func(i, j int) bool { return phase(ordered[i].ActionType) < phase(ordered[j].ActionType) })
	settled := false
	for _, in := range ordered {
		if !settled && phase(in.ActionType) > phase(model.ActionMove) {
			r.settleMoves()
			settled = true
		}
		r.resolveOne(in)
	}
	r.settleMoves() // перемещения фазы 2, если дальше намерений не было, и переходы по эффектам испытаний фазы 3
	r.returns()
	return Result{Events: r.events, Results: r.results}
}

// Clone — глубокая копия снимка: движок и свёртка не трогают исходные данные.
func Clone(snap model.RoundSnapshot) model.RoundSnapshot {
	b, err := json.Marshal(snap)
	if err != nil {
		panic("engine.Clone: " + err.Error())
	}
	var out model.RoundSnapshot
	if err := json.Unmarshal(b, &out); err != nil {
		panic("engine.Clone: " + err.Error())
	}
	return out
}

// phase — порядок разрешения по типу действия.
func phase(t model.ActionType) int {
	switch t {
	case model.ActionHelp, model.ActionWait, model.ActionSneak:
		return 1
	case model.ActionMove:
		return 2
	case model.ActionExplore, model.ActionInteract, model.ActionPersuade, model.ActionTrade:
		return 3
	case model.ActionAttack:
		return 4
	}
	return 5
}

type resolver struct {
	snap      model.RoundSnapshot
	rng       *rand.Rand
	preset    rules.ModePreset
	events    []model.Event
	results   []model.ActionResult
	helpers   map[model.CharacterID]int
	proposals []proposal // заявленные переходы до сверки с группой (parties.go)
}

func (r *resolver) resolveOne(in model.Intent) {
	res := model.ActionResult{SubmissionID: in.SubmissionID, CharacterID: in.CharacterID, Intent: in, EventSeqs: []int64{}}
	c, ok := r.snap.Character(in.CharacterID)
	switch {
	case !ok:
		res.Status, res.Reason = model.ResultRejected, "персонажа нет в снимке раунда"
	case c.Status == model.CharacterDead:
		res.Status, res.Reason = model.ResultRejected, "персонаж выбыл из игры до возвращения"
	case in.ActionType == "":
		// Намерение без типа: разбор не удался и безопасного толкования нет.
		res.Status, res.Reason = model.ResultAmbiguous, strings.Join(append([]string{"заявка не разобрана"}, in.Ambiguities...), "; ")
	default:
		entry, known := rules.Entry(in.ActionType)
		switch {
		case !known:
			res.Status, res.Reason = model.ResultRejected, fmt.Sprintf("неизвестное действие %q", in.ActionType)
		case !entry.Supported:
			// Защита: интерпретатор такие намерения заменяет безопасными до движка (ADR-0036), сюда попадает только обход.
			res.Status, res.Reason = model.ResultRejected, fmt.Sprintf("действие «%s» появится в пункте плана %s", entry.Title, entry.Later)
		default:
			r.resolveSupported(c, in, &res)
		}
	}
	if res.Status != model.ResultResolved && res.Summary == "" {
		res.Summary = res.Reason
		r.emit(&res, model.EventActionRejected, model.VisibilityPrivate, []model.CharacterID{in.CharacterID}, c, map[string]any{"status": res.Status, "reason": res.Reason, "action_type": in.ActionType})
	}
	if len(in.Ambiguities) > 0 && res.Status == model.ResultResolved {
		// Неоднозначности интерпретатора (в том числе безопасное толкование, G3): действие выполнено, игроку видно, что было непонятно.
		res.Summary += " (примечание: " + joinNotes(in.Ambiguities) + ")"
	}
	r.results = append(r.results, res)
}

// joinNotes склеивает примечания интерпретатора через «; »: у каждой части срезаются пробелы и завершающая точка
// или точка с запятой, чтобы в итоге не было «дымок.; extra: …» (B55); пустые части пропускаются.
func joinNotes(notes []string) string {
	parts := make([]string, 0, len(notes))
	for _, n := range notes {
		n = strings.TrimSpace(n)
		n = strings.TrimRight(n, ".; ")
		if n != "" {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, "; ")
}

func (r *resolver) resolveSupported(c *model.SnapshotCharacter, in model.Intent, res *model.ActionResult) {
	loc, _ := r.locationOf(c)
	switch in.ActionType {
	case model.ActionWait:
		res.Status, res.Summary = model.ResultResolved, "ждёт и держится настороже"
		r.emit(res, model.EventCharacterWaited, model.VisibilityPrivate, []model.CharacterID{c.ID}, c, map[string]any{"character_id": c.ID})
	case model.ActionHelp:
		target := r.characterByNickname(in.Target)
		switch {
		case target == nil:
			res.Status, res.Reason = model.ResultRejected, fmt.Sprintf("персонаж «%s» не найден", in.Target)
		case target.ID == c.ID:
			res.Status, res.Reason = model.ResultRejected, "помочь самому себе нельзя"
		case !sameLocation(c, target):
			res.Status, res.Reason = model.ResultRejected, fmt.Sprintf("%s не рядом", target.Nickname)
		case !sameParty(c, target):
			// Помощь только внутри группы (docs/game/checks.md, пункт 19).
			res.Status, res.Reason = model.ResultRejected, fmt.Sprintf("помогать можно только участнику своей группы, а %s не в вашей группе (/party)", target.Nickname)
		default:
			r.helpers[target.ID]++
			res.Status, res.Summary = model.ResultResolved, "помогает: "+target.Nickname
			r.emit(res, model.EventHelpGiven, model.VisibilityPrivate, []model.CharacterID{c.ID, target.ID}, c, map[string]any{"helper_id": c.ID, "target_id": target.ID})
		}
	case model.ActionMove:
		r.move(c, loc, in, res)
	case model.ActionExplore:
		r.explore(c, loc, in, res)
	case model.ActionInteract:
		r.interact(c, loc, in, res)
	}
}

func (r *resolver) move(c *model.SnapshotCharacter, loc *model.SnapshotLocation, in model.Intent, res *model.ActionResult) {
	if loc == nil {
		res.Status, res.Reason = model.ResultRejected, "персонаж вне локаций"
		return
	}
	exit, ok := r.exitTo(loc, in.Target)
	if !ok {
		res.Status, res.Reason = model.ResultRejected, fmt.Sprintf("из «%s» нет выхода «%s»; выходы: %s", loc.Name, in.Target, exits(&r.snap, loc))
		return
	}
	dest := exit.To
	if in.FreeCheck() {
		// Переход с испытанием: провал оставляет персонажа на месте и применяет on_fail.
		ap := freeApproach(in)
		ap.OnSuccess = nil
		if !r.check(c, loc, in, res, ap, model.LocationObject{}, -1, fmt.Sprintf("пробует перейти: %s → %s", loc.Name, dest.Name)) {
			return
		}
	}
	r.propose(res, c, loc, dest)
}

// propose заявляет переход: итог считается выполненным, сам переход и события — после сверки с группой (`settleMoves`).
func (r *resolver) propose(res *model.ActionResult, c *model.SnapshotCharacter, loc, dest *model.SnapshotLocation) {
	res.Status, res.Summary = model.ResultResolved, fmt.Sprintf("переходит: %s → %s", loc.Name, dest.Name)
	r.proposals = append(r.proposals, proposal{submission: res.SubmissionID, character: c.ID, from: loc.ID, to: dest.ID})
}

// moveTo переводит персонажа в локацию назначения; событие видно в обеих локациях. Итог res может быть nil
// (участник группы, которого группа увела с собой без его заявки). Первый вход персонажа фракции в клетку (кроме
// стартовой) — открытие: приватное событие `cell_discovered`, фракция добавляется к `discovered_by` клетки (пункт 27).
func (r *resolver) moveTo(res *model.ActionResult, c *model.SnapshotCharacter, loc, dest *model.SnapshotLocation) {
	from := loc.ID
	r.emit(res, model.EventCharacterMoved, model.VisibilityLocation, nil, c, map[string]any{"character_id": c.ID, "from": from, "to": dest.ID, "from_name": loc.Name, "to_name": dest.Name, "x": dest.X, "y": dest.Y})
	// Событие видно и в локации назначения: второе событие с той же сутью, адресованное новой локации.
	ev := r.events[len(r.events)-1]
	ev2 := ev
	ev2.LocationID = &dest.ID
	ev2.Seq = int64(len(r.events) + 1)
	r.events = append(r.events, ev2)
	if res != nil {
		res.EventSeqs = append(res.EventSeqs, ev2.Seq)
	}
	c.LocationID = &dest.ID
	dest.Visited = true
	if !dest.Start && c.FactionID != 0 && !dest.Discovered(c.FactionID) {
		dest.DiscoveredBy = append(dest.DiscoveredBy, c.FactionID)
		r.emit(res, model.EventCellDiscovered, model.VisibilityPrivate, []model.CharacterID{c.ID}, c, map[string]any{"character_id": c.ID, "faction_id": c.FactionID, "location_id": dest.ID, "x": dest.X, "y": dest.Y, "name": dest.Name})
	}
}

// freeApproach — подход свободного испытания из намерения: сложность по таблице риска, эффекты от интерпретатора.
func freeApproach(in model.Intent) model.Approach {
	return model.Approach{Action: in.ActionType, Attribute: in.Attribute, Skill: in.Skill, Difficulty: rules.RiskDifficulty(in.Risk), OnSuccess: in.OnSuccess, OnFail: in.OnFail}
}

func (r *resolver) explore(c *model.SnapshotCharacter, loc *model.SnapshotLocation, in model.Intent, res *model.ActionResult) {
	if loc == nil {
		res.Status, res.Reason = model.ResultRejected, "персонаж вне локаций"
		return
	}
	if in.Target == "" || strings.EqualFold(in.Target, loc.Name) || in.Target == "вокруг" || in.Target == "around" {
		if in.FreeCheck() {
			r.check(c, loc, in, res, freeApproach(in), model.LocationObject{}, -1, "действует в локации «"+loc.Name+"»")
			return
		}
		fact := fmt.Sprintf("%s: %s Выходы: %s. Объекты: %s.", loc.Name, loc.Description, exits(&r.snap, loc), objects(loc))
		res.Status, res.Summary = model.ResultResolved, "осматривается в локации «"+loc.Name+"»"
		r.learn(res, c, loc, fact)
		return
	}
	obj, idx, ok := r.findObject(loc, in.Target)
	if !ok {
		res.Status, res.Reason = model.ResultRejected, fmt.Sprintf("в «%s» нет объекта «%s»; объекты: %s", loc.Name, in.Target, objects(loc))
		return
	}
	if in.FreeCheck() {
		r.check(c, loc, in, res, freeApproach(in), obj, idx, "осматривает: "+obj.Title)
		return
	}
	ap, apIdx := pickApproach(obj, model.ActionExplore, in.ApproachIdx)
	if apIdx < 0 {
		fact := fmt.Sprintf("%s: %s", obj.Title, describeObject(obj))
		res.Status, res.Summary = model.ResultResolved, "осматривает: "+obj.Title
		r.learn(res, c, loc, fact)
		return
	}
	r.check(c, loc, in, res, ap, obj, idx, "осматривает: "+obj.Title)
}

func (r *resolver) interact(c *model.SnapshotCharacter, loc *model.SnapshotLocation, in model.Intent, res *model.ActionResult) {
	if loc == nil {
		res.Status, res.Reason = model.ResultRejected, "персонаж вне локаций"
		return
	}
	if in.Target == "" && in.FreeCheck() {
		r.check(c, loc, in, res, freeApproach(in), model.LocationObject{}, -1, "действует в локации «"+loc.Name+"»")
		return
	}
	obj, idx, ok := r.findObject(loc, in.Target)
	if !ok {
		res.Status, res.Reason = model.ResultRejected, fmt.Sprintf("в «%s» нет объекта «%s»; объекты: %s", loc.Name, in.Target, objects(loc))
		return
	}
	if in.FreeCheck() {
		r.check(c, loc, in, res, freeApproach(in), obj, idx, "взаимодействует: "+obj.Title)
		return
	}
	ap, apIdx := pickApproach(obj, model.ActionInteract, in.ApproachIdx)
	if apIdx < 0 {
		res.Status, res.Reason = model.ResultRejected, fmt.Sprintf("с объектом «%s» нечего делать", obj.Title)
		return
	}
	r.check(c, loc, in, res, ap, obj, idx, "взаимодействует: "+obj.Title)
}

// check выполняет проверку по подходу (объекта или свободного испытания, objIdx < 0 — без объекта) и применяет
// on_success / on_fail через события. Возвращает true, если ветвь успеха.
func (r *resolver) check(c *model.SnapshotCharacter, loc *model.SnapshotLocation, in model.Intent, res *model.ActionResult, ap model.Approach, obj model.LocationObject, objIdx int, summary string) bool {
	if ap.Difficulty == 0 && ap.Attribute == "" {
		res.Status, res.Summary = model.ResultResolved, summary
		r.applyApproach(res, c, loc, obj, objIdx, ap.OnSuccess)
		return true
	}
	param := rules.Param(*c, ap.Attribute, ap.Skill, r.helpers[c.ID])
	difficulty := rules.Difficulty(r.preset, ap.Difficulty, loc.Difficulty)
	info := model.CheckInfo{Attribute: ap.Attribute, Skill: ap.Skill, Param: param, Difficulty: difficulty, ChancePct: rules.ChancePct(param, difficulty)}
	info.Roll = r.roll()
	success := rules.Success(info.Roll, info.ChancePct)
	if !success && (in.Push || r.preset.AutoPush) {
		// Форсирование — повторный бросок с тем же шансом (docs/game/checks.md).
		info.Pushed = true
		info.PushRoll = r.roll()
		success = rules.Success(info.PushRoll, info.ChancePct)
		if r.preset.PushCost && r.snap.Penalties && !c.HasCondition(model.ConditionExhausted) {
			c.Conditions = append(c.Conditions, model.ConditionExhausted)
			r.emit(res, model.EventConditionAdded, model.VisibilityPrivate, []model.CharacterID{c.ID}, c, map[string]any{"character_id": c.ID, "condition": model.ConditionExhausted})
		}
	}
	switch {
	case success && info.Pushed:
		info.Outcome = model.OutcomeCostlySuccess
	case success:
		info.Outcome = model.OutcomeSuccess
	case r.preset.FailAsCostly && !ap.Deadly:
		info.Outcome = model.OutcomeCostlySuccess
	default:
		info.Outcome = model.OutcomeFailure
	}
	res.Status, res.Check, res.Outcome = model.ResultResolved, &info, info.Outcome
	res.Summary = summary + " — " + outcomeWords(info.Outcome)
	// Попытка и её исход видны присутствующим в локации (рассказчик описывает их в хронике, docs/llm/round-calls.md);
	// числа броска из полезной нагрузки в контекст рассказчика попадают только в режиме «механика».
	r.emit(res, model.EventCheckRolled, model.VisibilityLocation, nil, c, map[string]any{"character_id": c.ID, "object": obj.Key, "title": obj.Title, "free": objIdx < 0 || in.FreeCheck(), "summary": summary, "check": info})
	if info.Outcome == model.OutcomeFailure {
		r.applyFail(res, c, loc, ap.OnFail)
		return false
	}
	r.applyApproach(res, c, loc, obj, objIdx, ap.OnSuccess)
	return true
}

// applyApproach выполняет on_success: смена состояния объекта (только у подхода из данных локации), раскрытие
// скрытого объекта, факт, переход по видимому выходу (свободное испытание). Без объекта (objIdx < 0) состояние не меняется.
func (r *resolver) applyApproach(res *model.ActionResult, c *model.SnapshotCharacter, loc *model.SnapshotLocation, obj model.LocationObject, objIdx int, effect json.RawMessage) {
	var e struct {
		State  string `json:"state"`
		Object string `json:"object"`
		Reveal string `json:"reveal"`
		Fact   string `json:"fact"`
		Move   string `json:"move"`
	}
	if len(effect) > 0 {
		_ = json.Unmarshal(effect, &e)
	}
	if e.State != "" {
		targetKey, targetIdx := obj.Key, objIdx
		if e.Object != "" {
			if _, i, ok := loc.Object(e.Object); ok {
				targetKey, targetIdx = e.Object, i
			}
		}
		if targetIdx >= 0 {
			prev := loc.Objects[targetIdx].State
			loc.Objects[targetIdx].State = e.State
			r.emit(res, model.EventObjectChanged, model.VisibilityLocation, nil, c, map[string]any{"character_id": c.ID, "location_id": loc.ID, "object": targetKey, "title": loc.Objects[targetIdx].Title, "from": prev, "to": e.State})
		}
	}
	if e.Reveal != "" {
		if _, i, ok := loc.Object(e.Reveal); ok && !loc.Objects[i].Revealed {
			loc.Objects[i].Revealed = true
			r.emit(res, model.EventObjectChanged, model.VisibilityLocation, nil, c, map[string]any{"character_id": c.ID, "location_id": loc.ID, "object": e.Reveal, "title": loc.Objects[i].Title, "revealed": true})
		}
	}
	fact := e.Fact
	if fact == "" && objIdx >= 0 {
		fact = fmt.Sprintf("%s: %s", obj.Title, describeObject(loc.Objects[objIdx]))
	}
	if fact != "" {
		r.learn(res, c, loc, fact)
	}
	if e.Move != "" {
		if exit, ok := r.exitTo(loc, e.Move); ok {
			summary := res.Summary
			r.propose(res, c, loc, exit.To)
			res.Summary = summary + "; " + res.Summary
		}
	}
}

// ApproachDone сообщает, исчерпан ли подход объекта для присутствующих персонажей (B8, docs/game/checks.md):
// подход со сменой состояния или раскрытием — когда объект уже в целевом состоянии или раскрыт; подход только с
// фактом (явным из on_success или описанием объекта, как в applyApproach) — когда факт уже известен каждому
// присутствующему; подход с переходом (`move`) не исчерпывается. Без персонажей — false.
func ApproachDone(loc model.SnapshotLocation, objIdx int, ap model.Approach, chars []model.SnapshotCharacter) bool {
	if objIdx < 0 || objIdx >= len(loc.Objects) || len(chars) == 0 {
		return false
	}
	var e struct {
		State  string `json:"state"`
		Object string `json:"object"`
		Reveal string `json:"reveal"`
		Fact   string `json:"fact"`
		Move   string `json:"move"`
	}
	if len(ap.OnSuccess) > 0 {
		_ = json.Unmarshal(ap.OnSuccess, &e)
	}
	if e.Move != "" {
		return false
	}
	if e.State != "" || e.Reveal != "" {
		if e.State != "" {
			target := loc.Objects[objIdx]
			if e.Object != "" {
				o, _, ok := loc.Object(e.Object)
				if !ok {
					return false
				}
				target = o
			}
			if target.State != e.State {
				return false
			}
		}
		if e.Reveal != "" {
			o, _, ok := loc.Object(e.Reveal)
			if !ok || !o.Revealed {
				return false
			}
		}
		return true
	}
	fact := e.Fact
	if fact == "" {
		obj := loc.Objects[objIdx]
		fact = fmt.Sprintf("%s: %s", obj.Title, describeObject(obj))
	}
	for _, c := range chars {
		if !slices.Contains(c.Knowledge, fact) {
			return false
		}
	}
	return true
}

// applyFail выполняет on_fail: состояние (только при включённых штрафах снимка, B27), урон в пределах режима, факт.
func (r *resolver) applyFail(res *model.ActionResult, c *model.SnapshotCharacter, loc *model.SnapshotLocation, effect json.RawMessage) {
	var e struct {
		Condition string `json:"condition"`
		Damage    int    `json:"damage"`
		Fact      string `json:"fact"`
	}
	if len(effect) > 0 {
		_ = json.Unmarshal(effect, &e)
	}
	if e.Condition != "" && r.snap.Penalties && !c.HasCondition(e.Condition) {
		c.Conditions = append(c.Conditions, e.Condition)
		r.emit(res, model.EventConditionAdded, model.VisibilityPrivate, []model.CharacterID{c.ID}, c, map[string]any{"character_id": c.ID, "condition": e.Condition})
	}
	if e.Damage > 0 && r.preset.TrackHP {
		r.damage(res, c, e.Damage)
	}
	if e.Fact != "" {
		r.learn(res, c, loc, e.Fact)
	}
}

// damage снимает HP; при 0 — ненастоящая смерть по режиму (docs/game/death.md).
func (r *resolver) damage(res *model.ActionResult, c *model.SnapshotCharacter, dmg int) {
	from := c.HP
	c.HP = max(c.HP-dmg, 0)
	r.emit(res, model.EventHPChanged, model.VisibilityPrivate, []model.CharacterID{c.ID}, c, map[string]any{"character_id": c.ID, "from": from, "to": c.HP})
	if c.HP == 0 && c.Status != model.CharacterDead {
		c.Status = model.CharacterDead
		r.emit(res, model.EventCharacterDied, model.VisibilityPublic, nil, c, map[string]any{"character_id": c.ID, "nickname": c.Nickname})
	}
}

// returns возвращает персонажей, у которых истёк счётчик ненастоящей смерти.
func (r *resolver) returns() {
	for i := range r.snap.Characters {
		c := &r.snap.Characters[i]
		if c.Status == model.CharacterDead && c.DeathRoundsLeft <= 0 {
			c.Status, c.HP, c.Conditions = model.CharacterActive, c.MaxHP, []string{}
			r.emit(nil, model.EventCharacterReturned, model.VisibilityPublic, nil, c, map[string]any{"character_id": c.ID, "nickname": c.Nickname, "hp": c.HP})
		}
	}
}

func (r *resolver) learn(res *model.ActionResult, c *model.SnapshotCharacter, loc *model.SnapshotLocation, fact string) {
	c.Knowledge = appendUnique(c.Knowledge, fact)
	r.emit(res, model.EventFactLearned, model.VisibilityPrivate, []model.CharacterID{c.ID}, c, map[string]any{"character_id": c.ID, "location_id": loc.ID, "fact": fact})
}

// emit добавляет событие с номером внутри раунда и привязывает к итогу заявки.
func (r *resolver) emit(res *model.ActionResult, t model.EventType, vis model.Visibility, to []model.CharacterID, c *model.SnapshotCharacter, payload map[string]any) {
	b, err := json.Marshal(payload)
	if err != nil {
		panic("engine: marshal payload: " + err.Error())
	}
	ev := model.Event{Seq: int64(len(r.events) + 1), Type: t, Payload: b, Visibility: vis, VisibleTo: to}
	if c != nil {
		id := c.ID
		ev.CharacterID = &id
		ev.LocationID = c.LocationID
	}
	if vis != model.VisibilityPrivate {
		ev.VisibleTo = nil
	}
	r.events = append(r.events, ev)
	if res != nil {
		res.EventSeqs = append(res.EventSeqs, ev.Seq)
	}
}

// roll — бросок d100: целое от 1 до 100.
func (r *resolver) roll() int {
	return r.rng.IntN(100) + 1
}

func (r *resolver) locationOf(c *model.SnapshotCharacter) (*model.SnapshotLocation, bool) {
	if c.LocationID == nil {
		return nil, false
	}
	return r.snap.Location(*c.LocationID)
}

func (r *resolver) characterByNickname(name string) *model.SnapshotCharacter {
	for i := range r.snap.Characters {
		if strings.EqualFold(r.snap.Characters[i].Nickname, strings.TrimPrefix(name, "@")) {
			return &r.snap.Characters[i]
		}
	}
	return nil
}

// findObject ищет видимый объект по ключу или названию (подстрока без учёта регистра).
func (r *resolver) findObject(loc *model.SnapshotLocation, target string) (model.LocationObject, int, bool) {
	t := strings.ToLower(strings.TrimSpace(target))
	for i, o := range loc.Objects {
		if o.HiddenUntil && !o.Revealed {
			continue
		}
		if strings.EqualFold(o.Key, t) || strings.EqualFold(o.Title, t) || matches(t, o.Title) {
			return o, i, true
		}
	}
	return model.LocationObject{}, -1, false
}

// pickApproach — подход по индексу или первый подход с нужным типом действия.
func pickApproach(obj model.LocationObject, t model.ActionType, idx *int) (model.Approach, int) {
	if idx != nil && *idx >= 0 && *idx < len(obj.Approaches) {
		return obj.Approaches[*idx], *idx
	}
	for i, a := range obj.Approaches {
		if a.Action == t {
			return a, i
		}
	}
	return model.Approach{}, -1
}

// matches — грубое сравнение цели игрока с названием: каждое слово цели (от 3 букв) по основе
// (первые 4 буквы или слово без последней) должно начинать какое-то слово названия: «доску» ≈ «доска объявлений».
// Временная замена интерпретатора (пункт 08) для типовых команд.
func matches(target, title string) bool {
	words := strings.Fields(strings.ToLower(target))
	titleWords := strings.Fields(strings.ToLower(title))
	checked := 0
	for _, w := range words {
		w = strings.Trim(w, "«»\"'.,!?()")
		rw := []rune(w)
		if len(rw) < 3 {
			continue
		}
		checked++
		stem := string(rw[:min(len(rw), max(4, len(rw)-1))])
		found := false
		for _, tw := range titleWords {
			if strings.HasPrefix(strings.Trim(tw, "«»\"'.,!?()"), stem) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return checked > 0
}

func sameLocation(a, b *model.SnapshotCharacter) bool {
	return a.LocationID != nil && b.LocationID != nil && *a.LocationID == *b.LocationID
}

func sameParty(a, b *model.SnapshotCharacter) bool {
	return a.PartyID != nil && b.PartyID != nil && *a.PartyID == *b.PartyID
}

// exitTo — выход клетки по цели: направление словом или имя соседа (`RoundSnapshot.ExitTo`), иначе имя видимого
// соседа по основе слов (`matches`: «к башне» ≈ «Зал Серой башни») — для запасного разбора кодом. Имя скрытого
// соседа не сопоставляется (ADR-0025).
func (r *resolver) exitTo(loc *model.SnapshotLocation, target string) (model.Exit, bool) {
	if e, ok := r.snap.ExitTo(loc, target); ok {
		return e, true
	}
	for _, e := range r.snap.Exits(loc) {
		if !e.To.Hidden && matches(target, e.To.Name) {
			return e, true
		}
	}
	return model.Exit{}, false
}

// exits — выходы клетки словами: «на север: Лес, на восток: Дорога».
func exits(snap *model.RoundSnapshot, loc *model.SnapshotLocation) string {
	var out []string
	for _, e := range snap.Exits(loc) {
		out = append(out, e.Title())
	}
	if len(out) == 0 {
		return "нет"
	}
	return strings.Join(out, ", ")
}

func objects(loc *model.SnapshotLocation) string {
	var out []string
	for _, o := range loc.Objects {
		if o.HiddenUntil && !o.Revealed {
			continue
		}
		out = append(out, o.Title)
	}
	if len(out) == 0 {
		return "нет"
	}
	return strings.Join(out, ", ")
}

func describeObject(o model.LocationObject) string {
	parts := []string{}
	if o.Description != "" {
		parts = append(parts, o.Description)
	}
	if o.State != "" {
		parts = append(parts, "состояние: "+stateWords(o.State))
	}
	if len(parts) == 0 {
		return "ничего примечательного"
	}
	return strings.Join(parts, "; ")
}

func stateWords(state string) string {
	switch state {
	case "locked":
		return "заперто"
	case "open":
		return "открыто"
	case "broken":
		return "сломано"
	case "closed":
		return "закрыто"
	}
	return state
}

func outcomeWords(outcome string) string {
	switch outcome {
	case model.OutcomeSuccess:
		return "успех"
	case model.OutcomeCostlySuccess:
		return "успех с ценой"
	}
	return "провал"
}

func appendUnique(list []string, v string) []string {
	for _, it := range list {
		if it == v {
			return list
		}
	}
	return append(list, v)
}
