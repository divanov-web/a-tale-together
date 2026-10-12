package narrator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"taletogether/internal/model"
)

// MaxOutputRunes — верхняя граница текста хроники с блоками; больше — замечание и дозапрос (сообщение всё равно делится).
const MaxOutputRunes = 6000

var digits = regexp.MustCompile(`\d`)

// validator — проверка ответа по входу: сцены, события (пара событий перехода считается одним), скрытые описания.
type validator struct {
	in       Input
	mode     model.GameMode
	tasks    Tasks
	def      model.CharacterModelDef
	scenes   map[int]scene   // по scene_id
	groups   map[int64]int64 // seq события → ключ группы (у перехода два seq с одной сутью)
	required map[int64]bool  // ключи групп, которые должны быть упомянуты
	events   map[int64]event
	secrets  map[string]string // никнейм → скрытое описание
	failed   map[int64]string  // seq проверки с провалом → актёр
}

// checked — разобранный ответ с замечаниями.
type checked struct {
	narration model.Narration
	issues    []string
	missing   []int64 // ключи групп событий без упоминания
	leak      bool
	suspect   string
	world     WorldPackage // пакет мира по заданиям; замечания к нему дозапроса не вызывают
}

func (c checked) needsRetry() bool { return len(c.issues) > 0 }

func newValidator(in Input, p Params) *validator {
	v := &validator{in: in, mode: p.Mode, tasks: p.Tasks, def: p.Def, scenes: map[int]scene{}, groups: map[int64]int64{}, required: map[int64]bool{}, events: map[int64]event{}, secrets: map[string]string{}, failed: map[int64]string{}}
	for _, sc := range in.Scenes {
		v.scenes[sc.SceneID] = sc
		for _, c := range sc.Characters {
			if len([]rune(strings.TrimSpace(c.SecretBio))) >= 8 {
				v.secrets[c.Nickname] = strings.ToLower(strings.TrimSpace(c.SecretBio))
			}
		}
	}
	moves := map[string]int64{}
	for _, e := range in.Events {
		v.events[e.Seq] = e
		key := e.Seq
		if e.Type == string(model.EventCharacterMoved) {
			k := e.Actor + "|" + e.Summary[strings.LastIndex(e.Summary, "«"):]
			if first, ok := moves[k]; ok {
				key = first
			} else {
				moves[k] = e.Seq
			}
		}
		v.groups[e.Seq] = key
		v.required[key] = true
		if e.Type == string(model.EventCheckRolled) && e.Outcome == OutcomeWords(model.OutcomeFailure) {
			v.failed[e.Seq] = e.Actor
		}
	}
	return v
}

// validate разбирает JSON ответа; ошибка только при ответе не по схеме.
func (v *validator) validate(raw json.RawMessage) (checked, error) {
	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return checked{}, fmt.Errorf("ответ не по схеме: %w", err)
	}
	out := checked{narration: model.Narration{Chronicle: strings.TrimSpace(resp.Chronicle), Scenes: []model.NarrationScene{}}}
	mentioned := map[int64]bool{}
	seen := map[int]bool{}
	var all strings.Builder
	all.WriteString(out.narration.Chronicle)
	for _, rs := range resp.Scenes {
		sc, ok := v.scenes[rs.SceneID]
		if !ok {
			out.issues = append(out.issues, fmt.Sprintf("сцены scene_id=%d нет во входе: убери её", rs.SceneID))
			continue
		}
		if seen[rs.SceneID] {
			out.issues = append(out.issues, fmt.Sprintf("сцена scene_id=%d повторяется: оставь один блок", rs.SceneID))
			continue
		}
		seen[rs.SceneID] = true
		ns := model.NarrationScene{LocationID: sc.LocationID, Party: sc.Label(), Text: strings.TrimSpace(rs.Text), EventSeqs: []int64{}}
		for _, seq := range rs.EventSeqs {
			key, ok := v.groups[seq]
			if !ok {
				out.issues = append(out.issues, fmt.Sprintf("события seq=%d нет во входе: не указывай его", seq))
				continue
			}
			mentioned[key] = true
			ns.EventSeqs = append(ns.EventSeqs, seq)
		}
		all.WriteString("\n" + ns.Text)
		out.narration.Scenes = append(out.narration.Scenes, ns)
	}
	for _, sc := range v.in.Scenes {
		if !seen[sc.SceneID] {
			out.issues = append(out.issues, fmt.Sprintf("нет блока для сцены scene_id=%d («%s»)", sc.SceneID, sc.Label()))
		}
	}
	for key := range v.required {
		if !mentioned[key] {
			out.missing = append(out.missing, key)
		}
	}
	sort.Slice(out.missing, func(i, j int) bool { return out.missing[i] < out.missing[j] })
	for _, key := range out.missing {
		e := v.events[key]
		out.issues = append(out.issues, fmt.Sprintf("событие seq=%d (%s) не упомянуто: опиши его в сцене %s и добавь seq в event_seqs", key, e.Summary, v.sceneHint(e.LocationID)))
	}
	text := strings.ToLower(all.String())
	for nick, secret := range v.secrets {
		if strings.Contains(text, secret) {
			out.leak = true
			out.issues = append(out.issues, fmt.Sprintf("раскрыто скрытое описание персонажа %s: убери его из текста", nick))
		}
	}
	if v.mode == model.ModeNarrative && digits.MatchString(all.String()) {
		out.issues = append(out.issues, "в режиме «повествование» в тексте нет чисел: замени числа словами")
	}
	if n := len([]rune(all.String())); n > MaxOutputRunes {
		out.issues = append(out.issues, fmt.Sprintf("текст слишком длинный (%d символов): сократи до %d", n, MaxOutputRunes))
	}
	out.suspect = v.suspectOutcome(out.narration)
	out.world = validateWorld(resp.World, v.tasks, v.def)
	return out, nil
}

// suspectOutcome — выборочная проверка исхода по ключевым словам: сцена с провалами без слов неудачи. Только для лога.
func (v *validator) suspectOutcome(n model.Narration) string {
	for _, sc := range n.Scenes {
		hasFail := false
		for _, seq := range sc.EventSeqs {
			if _, ok := v.failed[seq]; ok {
				hasFail = true
			}
		}
		if !hasFail {
			continue
		}
		text := strings.ToLower(sc.Text)
		if !strings.Contains(text, "не ") && !strings.Contains(text, "провал") && !strings.Contains(text, "тщетн") && !strings.Contains(text, "безуспеш") && !strings.Contains(text, "сорвал") && !strings.Contains(text, "напрасн") {
			return fmt.Sprintf("location_id=%d", sc.LocationID)
		}
	}
	return ""
}

// sceneHint — сцены локации события для замечания модели: «scene_id=2» или перечень, «любой» без локации.
func (v *validator) sceneHint(loc *model.LocationID) string {
	if loc == nil {
		return "любой"
	}
	var ids []string
	for _, sc := range v.in.Scenes {
		if sc.LocationID == *loc {
			ids = append(ids, fmt.Sprintf("scene_id=%d", sc.SceneID))
		}
	}
	if len(ids) == 0 {
		return "любой"
	}
	return strings.Join(ids, " или ")
}

// patch дописывает в сцены строки кода для неупомянутых событий (в первую сцену локации события) и добавляет
// блоки для сцен без текста; порядок сцен — как во входе.
func (v *validator) patch(n *model.Narration, missing []int64) {
	find := func(loc model.LocationID, label string) int {
		for i, sc := range n.Scenes {
			if sc.LocationID == loc && (label == "" || sc.Party == label) {
				return i
			}
		}
		return -1
	}
	for _, key := range missing {
		e := v.events[key]
		target := model.LocationID(0)
		if e.LocationID != nil {
			target = *e.LocationID
		}
		i := find(target, "")
		if i < 0 {
			added := false
			for _, sc := range v.in.Scenes {
				if sc.LocationID == target {
					n.Scenes = append(n.Scenes, model.NarrationScene{LocationID: target, Party: sc.Label(), EventSeqs: []int64{}})
					added = true
					break
				}
			}
			if !added && len(n.Scenes) == 0 {
				n.Scenes = append(n.Scenes, model.NarrationScene{LocationID: target, EventSeqs: []int64{}})
				added = true
			}
			if added {
				i = len(n.Scenes) - 1
			} else {
				i = 0
			}
		}
		line := e.Summary + "."
		if n.Scenes[i].Text == "" {
			n.Scenes[i].Text = line
		} else {
			n.Scenes[i].Text += "\n" + line
		}
		n.Scenes[i].EventSeqs = append(n.Scenes[i].EventSeqs, key)
		n.Patched = append(n.Patched, key)
	}
	for _, sc := range v.in.Scenes {
		if find(sc.LocationID, sc.Label()) < 0 {
			n.Scenes = append(n.Scenes, model.NarrationScene{LocationID: sc.LocationID, Party: sc.Label(), EventSeqs: []int64{}})
		}
	}
	order := map[string]int{}
	for _, sc := range v.in.Scenes {
		order[fmt.Sprintf("%d|%s", sc.LocationID, sc.Label())] = sc.SceneID
	}
	sort.SliceStable(n.Scenes, func(i, j int) bool {
		return order[fmt.Sprintf("%d|%s", n.Scenes[i].LocationID, n.Scenes[i].Party)] < order[fmt.Sprintf("%d|%s", n.Scenes[j].LocationID, n.Scenes[j].Party)]
	})
}
