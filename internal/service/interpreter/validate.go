package interpreter

import (
	"encoding/json"
	"fmt"
	"strings"

	"taletogether/internal/model"
	"taletogether/internal/service/rules"
)

// issue — ошибка валидации одного намерения; текст уходит модели в повторе и в журнал.
type issue struct {
	SubmissionID model.SubmissionID
	Msg          string
}

func (i issue) String() string { return fmt.Sprintf("заявка %d: %s", i.SubmissionID, i.Msg) }

// validator проверяет ответ модели по снимку и модели персонажа (docs/llm/interpreter.md, «Валидация»).
type validator struct {
	snap      model.RoundSnapshot
	def       model.CharacterModelDef
	subs      map[model.SubmissionID]model.SnapshotSubmission
	fallbacks map[model.SubmissionID]*rawFallback // запасные действия модели по заявкам из всех ответов
}

func newValidator(snap model.RoundSnapshot, def model.CharacterModelDef) *validator {
	v := &validator{snap: snap, def: def, subs: map[model.SubmissionID]model.SnapshotSubmission{}, fallbacks: map[model.SubmissionID]*rawFallback{}}
	for _, s := range snap.Submissions {
		v.subs[s.SubmissionID] = s
	}
	return v
}

// validate разбирает ответ: валидные намерения по заявкам, ошибки по заявкам. Лишние и повторные ID игнорируются.
func (v *validator) validate(raw json.RawMessage) (map[model.SubmissionID]model.Intent, []issue, error) {
	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, fmt.Errorf("ответ не по схеме: %w", err)
	}
	valid := map[model.SubmissionID]model.Intent{}
	var issues []issue
	seen := map[model.SubmissionID]bool{}
	for _, ri := range resp.Intents {
		id := model.SubmissionID(ri.SubmissionID)
		sub, ok := v.subs[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		if ri.Fallback != nil {
			v.fallbacks[id] = ri.Fallback
		}
		in, err := v.intent(sub, ri)
		if err != nil {
			issues = append(issues, issue{SubmissionID: id, Msg: err.Error()})
			continue
		}
		valid[id] = in
	}
	for _, s := range v.snap.Submissions {
		if !seen[s.SubmissionID] {
			issues = append(issues, issue{SubmissionID: s.SubmissionID, Msg: "намерение не возвращено"})
		}
	}
	return valid, issues, nil
}

// intent превращает сырое намерение в model.Intent, проверяя тип, цель, подход и свободное испытание.
func (v *validator) intent(sub model.SnapshotSubmission, ri rawIntent) (model.Intent, error) {
	in := model.Intent{SubmissionID: sub.SubmissionID, CharacterID: sub.CharacterID, ActionType: model.ActionType(ri.ActionType),
		Target: strings.TrimSpace(ri.Target), Push: ri.Push, Ambiguities: ri.Ambiguities, RawText: sub.Text}
	if in.Ambiguities == nil {
		in.Ambiguities = []string{}
	}
	if _, known := rules.Entry(in.ActionType); !known {
		return in, fmt.Errorf("неизвестный action_type %q", ri.ActionType)
	}
	c, ok := v.snap.Character(sub.CharacterID)
	if !ok || c.LocationID == nil || c.Status == model.CharacterDead {
		return in, nil // движок отклонит с понятной причиной; цель не проверяем
	}
	loc, ok := v.snap.Location(*c.LocationID)
	if !ok {
		return in, nil
	}
	switch in.ActionType {
	case model.ActionMove:
		e, ok := findExit(v.snap, loc, in.Target)
		if !ok {
			return in, fmt.Errorf("нет выхода %q; выходы: %s", in.Target, exitsList(v.snap, loc))
		}
		in.Target = string(e.Direction)
	case model.ActionExplore:
		if in.Target != "" && !strings.EqualFold(in.Target, loc.Name) {
			o, ok := findObject(loc, in.Target)
			if !ok {
				return in, fmt.Errorf("нет видимого объекта %q; объекты: %s", in.Target, objectsList(loc))
			}
			in.Target = o.Key
		}
	case model.ActionInteract:
		if in.Target == "" {
			if ri.Attribute == nil || *ri.Attribute == "" {
				return in, fmt.Errorf("interact без цели требует свободного испытания (attribute)")
			}
		} else {
			o, ok := findObject(loc, in.Target)
			if !ok {
				return in, fmt.Errorf("нет видимого объекта %q; объекты: %s", in.Target, objectsList(loc))
			}
			in.Target = o.Key
			if ri.ApproachIdx == nil && (ri.Attribute == nil || *ri.Attribute == "") && !hasApproach(o, model.ActionInteract) {
				return in, fmt.Errorf("у объекта %q нет подхода interact: укажите approach_idx или свободное испытание", o.Key)
			}
		}
	case model.ActionHelp:
		t, ok := findCharacter(v.snap, loc.ID, in.Target)
		if !ok {
			return in, fmt.Errorf("персонажа %q нет в локации", in.Target)
		}
		in.Target = t.Nickname
	}
	if ri.ApproachIdx != nil {
		o, ok := findObject(loc, in.Target)
		if !ok || *ri.ApproachIdx < 0 || *ri.ApproachIdx >= len(o.Approaches) {
			return in, fmt.Errorf("approach_idx %d вне подходов объекта %q", *ri.ApproachIdx, in.Target)
		}
		idx := *ri.ApproachIdx
		in.ApproachIdx = &idx
		return in, nil // подход из данных: свободное испытание не нужно
	}
	if ri.Attribute == nil || *ri.Attribute == "" {
		return in, nil
	}
	if in.ActionType != model.ActionMove && in.ActionType != model.ActionExplore && in.ActionType != model.ActionInteract {
		return in, nil // испытание имеет смысл только у этих действий; у остальных игнорируется
	}
	if _, ok := v.def.Attribute(*ri.Attribute); !ok {
		return in, fmt.Errorf("характеристики %q нет в модели персонажа", *ri.Attribute)
	}
	in.Attribute = *ri.Attribute
	if ri.Skill != nil && *ri.Skill != "" {
		if _, ok := v.def.Skill(*ri.Skill); !ok {
			return in, fmt.Errorf("навыка %q нет в модели персонажа", *ri.Skill)
		}
		in.Skill = *ri.Skill
	}
	in.Risk = model.RiskNormal
	if ri.Risk != nil && *ri.Risk != "" {
		if !rules.ValidRisk(model.Risk(*ri.Risk)) {
			return in, fmt.Errorf("риск %q не из таблицы normal/hard/extreme", *ri.Risk)
		}
		in.Risk = model.Risk(*ri.Risk)
	}
	success, err := v.successEffect(loc, ri.OnSuccess)
	if err != nil {
		return in, err
	}
	fail, err := failEffect(ri.OnFail)
	if err != nil {
		return in, err
	}
	if in.ActionType != model.ActionMove && success == nil {
		return in, fmt.Errorf("свободное испытание без on_success: нужен fact или move")
	}
	in.OnSuccess, in.OnFail = success, fail
	return in, nil
}

// successEffect — эффекты успеха из списка: fact, move (видимый выход), reveal (существующий скрытый объект).
func (v *validator) successEffect(loc *model.SnapshotLocation, e *successEff) (json.RawMessage, error) {
	if e == nil {
		return nil, nil
	}
	out := map[string]string{}
	if e.Fact != nil && strings.TrimSpace(*e.Fact) != "" {
		out["fact"] = strings.TrimSpace(*e.Fact)
	}
	if e.Move != nil && *e.Move != "" {
		x, ok := findExit(v.snap, loc, *e.Move)
		if !ok {
			return nil, fmt.Errorf("on_success.move: нет выхода %q", *e.Move)
		}
		out["move"] = string(x.Direction)
	}
	if e.Reveal != nil && *e.Reveal != "" {
		o, _, ok := loc.Object(*e.Reveal)
		if !ok || !o.HiddenUntil || o.Revealed {
			return nil, fmt.Errorf("on_success.reveal: нет скрытого объекта %q", *e.Reveal)
		}
		out["reveal"] = o.Key
	}
	if len(out) == 0 {
		return nil, nil
	}
	b, _ := json.Marshal(out)
	return b, nil
}

// failEffect — эффекты провала из списка: condition из таблицы, damage в лимите, fact.
func failEffect(e *failEff) (json.RawMessage, error) {
	if e == nil {
		return nil, nil
	}
	out := map[string]any{}
	if e.Condition != nil && *e.Condition != "" {
		if !rules.ValidCondition(*e.Condition) {
			return nil, fmt.Errorf("on_fail.condition %q не из списка %s", *e.Condition, strings.Join(rules.Conditions, "/"))
		}
		out["condition"] = *e.Condition
	}
	if e.Damage != nil && *e.Damage != 0 {
		if *e.Damage < 1 || *e.Damage > rules.FreeDamageMax {
			return nil, fmt.Errorf("on_fail.damage %d вне 1–%d", *e.Damage, rules.FreeDamageMax)
		}
		out["damage"] = *e.Damage
	}
	if e.Fact != nil && strings.TrimSpace(*e.Fact) != "" {
		out["fact"] = strings.TrimSpace(*e.Fact)
	}
	if len(out) == 0 {
		return nil, nil
	}
	b, _ := json.Marshal(out)
	return b, nil
}

// safe — безопасное толкование заявки (G3): запасное действие модели, если оно проходит проверку, иначе `wait`.
func (v *validator) safe(sub model.SnapshotSubmission, fb *rawFallback, reason string) model.Intent {
	if fb != nil {
		in, err := v.intent(sub, rawIntent{SubmissionID: int64(sub.SubmissionID), ActionType: fb.ActionType, Target: fb.Target, Ambiguities: []string{reason}})
		if err == nil && (in.ActionType == model.ActionExplore || in.ActionType == model.ActionWait || in.ActionType == model.ActionMove) {
			return in
		}
	}
	return model.Intent{SubmissionID: sub.SubmissionID, CharacterID: sub.CharacterID, ActionType: model.ActionWait, Ambiguities: []string{reason}, RawText: sub.Text}
}

// findExit — выход клетки по цели модели: направление (`north`, «на север») или имя соседа; цель нормализуется в
// ключ направления, который понимает движок.
func findExit(snap model.RoundSnapshot, loc *model.SnapshotLocation, target string) (model.Exit, bool) {
	return snap.ExitTo(loc, target)
}

func findObject(loc *model.SnapshotLocation, target string) (model.LocationObject, bool) {
	t := strings.TrimSpace(target)
	for _, o := range loc.Objects {
		if o.HiddenUntil && !o.Revealed {
			continue
		}
		if strings.EqualFold(o.Key, t) || strings.EqualFold(o.Title, t) {
			return o, true
		}
	}
	return model.LocationObject{}, false
}

func findCharacter(snap model.RoundSnapshot, loc model.LocationID, nickname string) (model.SnapshotCharacter, bool) {
	t := strings.TrimPrefix(strings.TrimSpace(nickname), "@")
	for _, c := range snap.Characters {
		if c.LocationID != nil && *c.LocationID == loc && c.Status != model.CharacterDead && strings.EqualFold(c.Nickname, t) {
			return c, true
		}
	}
	return model.SnapshotCharacter{}, false
}

func hasApproach(o model.LocationObject, t model.ActionType) bool {
	for _, a := range o.Approaches {
		if a.Action == t {
			return true
		}
	}
	return false
}

func exitsList(snap model.RoundSnapshot, loc *model.SnapshotLocation) string {
	var out []string
	for _, e := range snap.Exits(loc) {
		out = append(out, string(e.Direction)+" ("+e.To.Name+")")
	}
	return strings.Join(out, ", ")
}

func objectsList(loc *model.SnapshotLocation) string {
	var out []string
	for _, o := range loc.Objects {
		if !o.HiddenUntil || o.Revealed {
			out = append(out, o.Key)
		}
	}
	return strings.Join(out, ", ")
}
