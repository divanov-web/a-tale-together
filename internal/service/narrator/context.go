package narrator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"taletogether/internal/model"
	"taletogether/internal/service/engine"
	"taletogether/internal/service/rules"
)

// Лимиты контекста: выжимка прошлых хроник — последние раунды, каждая обрезана.
const (
	previousLast    = 3
	previousMaxRune = 400
)

// Bio — описания персонажа: открытое видно всем, скрытое — данные для подачи его линии, в тексте не раскрывается.
type Bio struct {
	Public string `json:"public"`
	Secret string `json:"secret"`
}

// Faction — сторона конфликта для контекста рассказчика.
type Faction struct {
	Code        string            `json:"code"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Tone        string            `json:"tone,omitempty"`
	Relations   map[string]string `json:"relations,omitempty"`
}

// Params — данные раунда для сборки контекста: мир, состояние после свёртки, описания персонажей, события по видимости.
type Params struct {
	Title    string                    `json:"title"`
	Tone     string                    `json:"tone"`
	Mode     model.GameMode            `json:"mode"`
	RoundNo  int                       `json:"round_no"`
	Factions []Faction                 `json:"factions"`
	Opening  string                    `json:"opening"`  // завязка истории (ADR-0018); первой строкой в выжимке прошлого
	Previous []model.Narration         `json:"previous"` // хроники прошлых раундов, от новых к старым
	State    *model.RoundSnapshot      `json:"state"`    // состояние после применения событий раунда
	Bios     map[model.CharacterID]Bio `json:"bios"`
	Events   []model.Event             `json:"events"` // только public и location (visibility.Public)
	Def      model.CharacterModelDef   `json:"character_model"`
}

// Input — переменная часть вызова: мир, сцены, события, подходы следующего раунда.
type Input struct {
	World      world        `json:"world"`
	Scenes     []scene      `json:"scenes"`
	Events     []event      `json:"events"`
	Approaches []approachIn `json:"approaches"`
}

type world struct {
	Title    string    `json:"title"`
	Tone     string    `json:"tone"`
	Mode     string    `json:"mode"`
	RoundNo  int       `json:"round_no"`
	Factions []Faction `json:"factions"`
	Previous []string  `json:"previous"`
}

type scene struct {
	SceneID     int              `json:"scene_id"`
	LocationID  model.LocationID `json:"location_id"`
	Party       string           `json:"party,omitempty"` // имя группы; пусто — персонажи без группы в этой локации (пункт 19)
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Exits       []string         `json:"exits"`
	Objects     []sceneObject    `json:"objects"`
	Characters  []sceneCharacter `json:"characters"`
}

// Label — подпись сцены в хронике: имя группы с локацией в скобках, у одиночек — название локации.
func (sc scene) Label() string {
	if sc.Party == "" {
		return sc.Name
	}
	return sc.Party + " (" + sc.Name + ")"
}

type sceneObject struct {
	Title string `json:"title"`
	State string `json:"state,omitempty"`
}

type sceneCharacter struct {
	Nickname   string   `json:"nickname"`
	Gender     string   `json:"gender,omitempty"` // `male`, `female`; пусто — не указан (B18)
	Status     string   `json:"status"`
	Entry      string   `json:"entry,omitempty"` // `new` — впервые в истории, `returned` — вернулся после отсутствия (B1)
	PublicBio  string   `json:"public_bio"`
	SecretBio  string   `json:"secret_bio"`
	Conditions []string `json:"conditions"`
}

type event struct {
	Seq        int64             `json:"seq"`
	Type       string            `json:"type"`
	LocationID *model.LocationID `json:"location_id,omitempty"`
	Actor      string            `json:"actor"`
	Summary    string            `json:"summary"`
	Outcome    string            `json:"outcome,omitempty"`
	Pushed     bool              `json:"pushed,omitempty"`
	Numbers    *checkNumbers     `json:"numbers,omitempty"` // только режим «механика»
}

type checkNumbers struct {
	Param      int `json:"param"`
	Difficulty int `json:"difficulty"`
	ChancePct  int `json:"chance_pct"`
	Roll       int `json:"roll"`
	PushRoll   int `json:"push_roll,omitempty"`
}

type approachIn struct {
	LocationID model.LocationID `json:"location_id"`
	Object     string           `json:"object"`
	Title      string           `json:"title"`
	Attribute  string           `json:"attribute,omitempty"`
	Skill      string           `json:"skill,omitempty"`
	Difficulty int              `json:"difficulty,omitempty"` // в режиме «повествование» не передаётся
}

// sceneKey — сцена: локация и группа (0 — персонажи без группы).
type sceneKey struct {
	location model.LocationID
	party    model.PartyID
}

// BuildInput собирает контекст вызова 2 (docs/llm/round-calls.md): сцены по группам внутри локаций (пункт 19: блок на
// группу, персонажи без группы одной локации — отдельная сцена без имени группы) с присутствующими персонажами
// (без выбывших: `inactive` и `dead` в сцены не попадают, уход описывается в раунде статуса `leaving`), их описания как
// данные, видимые объекты и выходы; события только public и location, числа броска — в режиме «механика»; подходы
// следующего раунда без шансов (их код припишет сам). Сцена без персонажей не нужна; лидер группы не передаётся.
func BuildInput(p Params) Input {
	in := Input{Scenes: []scene{}, Events: []event{}, Approaches: []approachIn{}}
	in.World = world{Title: p.Title, Tone: p.Tone, Mode: string(p.Mode), RoundNo: p.RoundNo, Factions: nonNilFactions(p.Factions), Previous: digest(p.Opening, p.Previous)}
	if p.State == nil {
		return in
	}
	present := map[sceneKey][]sceneCharacter{}
	var keys []sceneKey
	for _, c := range p.State.Characters {
		if c.LocationID == nil || c.Status.Absent() {
			continue
		}
		conds := []string{}
		for _, cond := range c.Conditions {
			conds = append(conds, ConditionWords(cond))
		}
		bio := p.Bios[c.ID]
		key := sceneKey{location: *c.LocationID}
		if c.PartyID != nil {
			if _, ok := p.State.Party(*c.PartyID); ok {
				key.party = *c.PartyID
			}
		}
		if _, seen := present[key]; !seen {
			keys = append(keys, key)
		}
		present[key] = append(present[key], sceneCharacter{Nickname: c.Nickname, Gender: string(c.Gender), Status: string(c.Status), Entry: c.Entry, PublicBio: bio.Public, SecretBio: bio.Secret, Conditions: conds})
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].location != keys[j].location {
			return keys[i].location < keys[j].location
		}
		// Группы раньше одиночек, группы между собой по id.
		if (keys[i].party == 0) != (keys[j].party == 0) {
			return keys[i].party != 0
		}
		return keys[i].party < keys[j].party
	})
	for _, key := range keys {
		l, ok := p.State.Location(key.location)
		if !ok {
			continue
		}
		sc := scene{SceneID: len(in.Scenes) + 1, LocationID: l.ID, Name: l.Name, Description: l.Description, Exits: []string{}, Objects: []sceneObject{}, Characters: present[key]}
		if pt, ok := p.State.Party(key.party); ok && key.party != 0 {
			sc.Party = pt.Name
		}
		for _, link := range l.Links {
			if !link.Hidden {
				sc.Exits = append(sc.Exits, link.Title)
			}
		}
		for _, o := range l.Objects {
			if o.HiddenUntil && !o.Revealed {
				continue
			}
			sc.Objects = append(sc.Objects, sceneObject{Title: o.Title, State: StateWords(o.State)})
		}
		in.Scenes = append(in.Scenes, sc)
	}
	mechanics := p.Mode == model.ModeMechanics
	for _, ev := range p.Events {
		if ev.Visibility == model.VisibilityPrivate {
			continue
		}
		actor, summary := Describe(ev, p.State)
		e := event{Seq: ev.Seq, Type: string(ev.Type), LocationID: ev.LocationID, Actor: actor, Summary: summary}
		if ev.Type == model.EventCheckRolled {
			info := checkOf(ev)
			e.Outcome, e.Pushed = OutcomeWords(info.Outcome), info.Pushed
			if mechanics {
				e.Numbers = &checkNumbers{Param: info.Param, Difficulty: info.Difficulty, ChancePct: info.ChancePct, Roll: info.Roll, PushRoll: info.PushRoll}
			}
		}
		in.Events = append(in.Events, e)
	}
	for _, opt := range Options(p.State, p.Def, p.Mode) {
		a := approachIn{LocationID: opt.LocationID, Object: opt.ObjectTitle, Title: opt.Title, Attribute: opt.Attribute, Skill: opt.Skill}
		if p.Mode != model.ModeNarrative {
			a.Difficulty = opt.Difficulty
		}
		in.Approaches = append(in.Approaches, a)
	}
	return in
}

// Encode — JSON контекста для поля Input запроса.
func (in Input) Encode() string {
	b, err := json.Marshal(in)
	if err != nil {
		panic(fmt.Sprintf("narrator.Input: %v", err))
	}
	return string(b)
}

// digest — выжимка прошлого: завязка истории (обрезанная до openingDigestRune), затем общий текст последних
// раундов, обрезанный; порядок от старых к новым.
func digest(opening string, prev []model.Narration) []string {
	out := []string{}
	if d := openingDigest(opening); d != "" {
		out = append(out, d)
	}
	n := min(len(prev), previousLast)
	for i := n - 1; i >= 0; i-- {
		text := strings.TrimSpace(prev[i].Chronicle)
		if text == "" {
			continue
		}
		if r := []rune(text); len(r) > previousMaxRune {
			text = string(r[:previousMaxRune]) + "…"
		}
		out = append(out, text)
	}
	return out
}

func nonNilFactions(f []Faction) []Faction {
	if f == nil {
		return []Faction{}
	}
	return f
}

// Option — подход объекта для следующего раунда с посчитанной кодом сложностью и шансом каждого присутствующего персонажа.
type Option struct {
	LocationID  model.LocationID
	Object      string // ключ объекта
	ObjectTitle string
	Idx         int
	Title       string
	Attribute   string // название характеристики; пусто — без броска
	Skill       string // название навыка
	Difficulty  int    // итоговая: подход + локация; 0 — без броска
	Chances     []Chance
}

// Chance — параметр и шанс персонажа на подход, в процентах (в группу не выводится, для личных подсказок; B15).
type Chance struct {
	Nickname string
	Param    int
	Pct      int
}

// Options считает подходы видимых объектов каждой сцены для следующего раунда (шаг 8 в docs/llm/round-calls.md):
// сложность по пресету режима плюс сложность локации, шанс по пулу каждого присутствующего персонажа. Исчерпанные
// подходы (`engine.ApproachDone`: объект уже в целевом состоянии, факт известен всем присутствующим; B8) не предлагаются.
func Options(state *model.RoundSnapshot, def model.CharacterModelDef, mode model.GameMode) []Option {
	var out []Option
	if state == nil {
		return out
	}
	preset := rules.Preset(mode)
	for _, l := range state.Locations {
		var chars []model.SnapshotCharacter
		for _, c := range state.Characters {
			if c.LocationID != nil && *c.LocationID == l.ID && c.Status != model.CharacterDead {
				chars = append(chars, c)
			}
		}
		if len(chars) == 0 {
			continue
		}
		for objIdx, o := range l.Objects {
			if o.HiddenUntil && !o.Revealed {
				continue
			}
			for i, ap := range o.Approaches {
				if engine.ApproachDone(l, objIdx, ap, chars) {
					continue
				}
				opt := Option{LocationID: l.ID, Object: o.Key, ObjectTitle: o.Title, Idx: i, Title: ap.Title}
				if opt.Title == "" {
					opt.Title = string(ap.Action) + " " + o.Title
				}
				if ap.Attribute != "" {
					if a, ok := def.Attribute(ap.Attribute); ok {
						opt.Attribute = a.Title
					} else {
						opt.Attribute = ap.Attribute
					}
					if ap.Skill != "" {
						if s, ok := def.Skill(ap.Skill); ok {
							opt.Skill = s.Title
						} else {
							opt.Skill = ap.Skill
						}
					}
					opt.Difficulty = rules.Difficulty(preset, ap.Difficulty, l.Difficulty)
					for _, c := range chars {
						param := rules.Param(c, ap.Attribute, ap.Skill, 0)
						opt.Chances = append(opt.Chances, Chance{Nickname: c.Nickname, Param: param, Pct: rules.ChancePct(param, opt.Difficulty)})
					}
				}
				out = append(out, opt)
			}
		}
	}
	return out
}

// OptionsText — блок подходов сцены для сообщения в группу: заголовок и пункт на подход с новой строки,
// характеристика и сложность; шанс персонажей не показывается (решение руководителя 2026-10-10, B6), в режиме
// «повествование» сложность словами. Пусто, если подходов нет.
func OptionsText(opts []Option, mode model.GameMode) string {
	parts := make([]string, 0, len(opts))
	for _, o := range opts {
		if o.Attribute == "" {
			parts = append(parts, "• "+o.Title+" (без броска)")
			continue
		}
		stat := o.Attribute
		if o.Skill != "" {
			stat += "/" + strings.ToLower(o.Skill)
		}
		if mode == model.ModeNarrative {
			parts = append(parts, fmt.Sprintf("• %s (%s, %s)", o.Title, stat, rules.DifficultyWords(o.Difficulty)))
		} else {
			parts = append(parts, fmt.Sprintf("• %s (%s, сложность %d)", o.Title, stat, o.Difficulty))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Варианты:\n" + strings.Join(parts, "\n")
}
