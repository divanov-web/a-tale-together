package narrator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"taletogether/internal/model"
)

// MaxOutputRunes — верхняя граница текста хроники с блоками; больше — замечание и дозапрос (сообщение всё равно делится).
const MaxOutputRunes = 6000

// Порог повтора зачина (B45): первое предложение хроники из не меньше чем repeatMinWords значимых слов, из которых
// доля repeatShare и больше есть в первом предложении одной из прошлых хроник.
const (
	repeatMinWords = 4
	repeatShare    = 0.6
)

var (
	digits      = regexp.MustCompile(`\d`)
	sentenceEnd = regexp.MustCompile(`[.!?…]`)
)

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
	world     WorldPackage        // пакет мира по заданиям; замечания к качеству пакета дозапроса не вызывают
	undone    []string            // задания `generate`, которые модель не выполнила: замечания для дозапроса (B45, ADR-0035)
	hints     []model.FactionCode // фракции из `hints` входа, отмеченные в `hints_given` (ADR-0038)
}

func (c checked) needsRetry() bool { return len(c.issues)+len(c.undone) > 0 }

// remarks — все замечания для дозапроса: к хронике и к невыполненным заданиям.
func (c checked) remarks() []string { return append(append([]string{}, c.issues...), c.undone...) }

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
	if RepeatsOpening(out.narration.Chronicle, v.in.World.Previous) {
		out.issues = append(out.issues, "зачин хроники повторяет прошлые раунды (world.previous): начни с того, что произошло в этом раунде, не с того же времени суток, той же тишины и того же дыма")
	}
	out.suspect = v.suspectOutcome(out.narration)
	out.world = validateWorld(resp.World, v.tasks, v.def)
	out.undone = undoneTasks(out.world, v.tasks)
	out.hints, out.undone = checkHints(v.in.Hints, resp.Hints, out.undone)
	return out, nil
}

// checkHints сверяет `hints_given` ответа с просьбами входа (ADR-0038): выполненные — по кодам фракций, порядок входа;
// фракция без подсказки — замечание для дозапроса; коды, которых не просили, отбрасываются.
func checkHints(asked []hintIn, given []string, undone []string) ([]model.FactionCode, []string) {
	marked := map[string]bool{}
	for _, code := range given {
		marked[strings.TrimSpace(code)] = true
	}
	var done []model.FactionCode
	for _, h := range asked {
		if marked[h.Faction] {
			done = append(done, model.FactionCode(h.Faction))
			continue
		}
		undone = append(undone, fmt.Sprintf("подсказка фракции «%s» не дана: в сцене её персонажей (%s) намекни, куда двигаться, только по данным входа, и добавь «%s» в hints_given", h.Faction, strings.Join(h.Characters, ", "), h.Faction))
	}
	return done, undone
}

// RepeatsOpening — зачин хроники повторяет начало одной из прошлых (`world.previous`): первое предложение состоит
// в основном из тех же значимых слов (B45). Слова — буквы и дефис, от трёх символов, без учёта регистра.
func RepeatsOpening(chronicle string, previous []string) bool {
	cur := sentenceWords(chronicle)
	if len(cur) < repeatMinWords {
		return false
	}
	for _, prev := range previous {
		old := sentenceWords(prev)
		common := 0
		for w := range cur {
			if old[w] {
				common++
			}
		}
		if float64(common) >= repeatShare*float64(len(cur)) {
			return true
		}
	}
	return false
}

// sentenceWords — значимые слова первого предложения текста.
func sentenceWords(text string) map[string]bool {
	text = strings.TrimSpace(text)
	if i := sentenceEnd.FindStringIndex(text); i != nil {
		text = text[:i[0]]
	}
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && r != '-' }) {
		if utf8.RuneCountInString(w) >= 3 {
			out[w] = true
		}
	}
	return out
}

// undoneTasks — задания `generate`, которые модель пропустила (закрыты шаблоном или перенесены): замечания для
// дозапроса наравне с пропущенными событиями (B45, ADR-0035). Клетки сверх лимита (`Tasks.Overflow`) заданием
// модели не были и не считаются.
func undoneTasks(w WorldPackage, tasks Tasks) []string {
	var out []string
	asked := map[model.LocationID]CellTask{}
	for _, task := range tasks.Cells {
		asked[task.LocationID] = task
	}
	for _, c := range w.Cells {
		if task, ok := asked[c.LocationID]; ok && c.Template {
			out = append(out, fmt.Sprintf("клетка location_id=%d («%s») не описана: верни её в world.cells с описанием и объектами", task.LocationID, task.Name))
		}
	}
	for i, n := range w.Neighbors {
		if n.Template && i < len(tasks.Neighbors) {
			task := tasks.Neighbors[i]
			out = append(out, fmt.Sprintf("сосед (%d, %d) %s от «%s» без карточки: верни его в world.neighbors", task.X, task.Y, task.Direction.Toward(), task.OfName))
		}
	}
	proposed := map[model.FactionID]bool{}
	for _, q := range w.Quests {
		if !q.Template {
			proposed[q.FactionID] = true
		}
	}
	for _, task := range tasks.Quests {
		if !proposed[task.FactionID] {
			out = append(out, fmt.Sprintf("квест фракции «%s» не предложен: верни его в world.quests с этапами", task.Faction))
		}
	}
	for _, st := range w.Stages {
		if st.Template {
			out = append(out, fmt.Sprintf("этап «%s» квеста %d не размещён: верни его в world.stages с условием вида %s и объектами клетки", st.Stage.Title, st.QuestID, st.Stage.Condition.Kind))
		}
	}
	for _, g := range w.Goals {
		if g.Template {
			faction := ""
			for _, task := range tasks.Goals {
				if task.FactionID == g.FactionID {
					faction = task.Faction
				}
			}
			out = append(out, fmt.Sprintf("цель фракции «%s» не сформулирована: верни её в world.goals", faction))
		}
	}
	return out
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
