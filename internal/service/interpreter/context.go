package interpreter

import (
	"encoding/json"
	"fmt"
	"sort"

	"taletogether/internal/model"
)

// Лимиты контекста: знания персонажа — последние факты, каждый обрезан.
const (
	knowledgeLast    = 3
	knowledgeMaxRune = 160
)

// Input — переменная часть вызова: модель персонажа, сцены по локациям, заявки (данные игроков).
type Input struct {
	CharacterModel modelInfo    `json:"character_model"`
	Scenes         []scene      `json:"scenes"`
	Submissions    []submission `json:"submissions"`
}

type modelInfo struct {
	Attributes []keyTitle `json:"attributes"`
	Skills     []skillRef `json:"skills"`
}

type keyTitle struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type skillRef struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Attribute string `json:"attribute"`
}

type scene struct {
	LocationID  model.LocationID `json:"location_id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Difficulty  int              `json:"difficulty"`
	Exits       []exit           `json:"exits"`
	Objects     []object         `json:"objects"`
	Characters  []character      `json:"characters"`
}

type exit struct {
	To    string `json:"to"`
	Title string `json:"title"`
}

type object struct {
	Key         string     `json:"key"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	State       string     `json:"state,omitempty"`
	Approaches  []approach `json:"approaches"`
}

type approach struct {
	Idx       int              `json:"idx"`
	Action    model.ActionType `json:"action"`
	Title     string           `json:"title"`
	Attribute string           `json:"attribute,omitempty"`
	Skill     string           `json:"skill,omitempty"`
}

type character struct {
	CharacterID model.CharacterID     `json:"character_id"`
	Nickname    string                `json:"nickname"`
	Status      model.CharacterStatus `json:"status"`
	Attributes  map[string]int        `json:"attributes"`
	Skills      map[string]int        `json:"skills"`
	Conditions  []string              `json:"conditions"`
	Knowledge   []string              `json:"knowledge"`
}

type submission struct {
	SubmissionID model.SubmissionID `json:"submission_id"`
	CharacterID  model.CharacterID  `json:"character_id"`
	Nickname     string             `json:"nickname"`
	Text         string             `json:"text"`
}

// BuildInput собирает контекст вызова из снимка: только видимые объекты и выходы, персонажи без секретов,
// знания — последние факты; заявки всех персонажей в одном блоке данных. Персонажи вне локаций и выбывшие
// в сцены не попадают, их заявки передаются как есть (движок отклонит).
func BuildInput(snap model.RoundSnapshot, def model.CharacterModelDef) Input {
	in := Input{Scenes: []scene{}, Submissions: []submission{}}
	for _, a := range def.Attributes {
		in.CharacterModel.Attributes = append(in.CharacterModel.Attributes, keyTitle{Key: a.Key, Title: a.Title})
	}
	for _, s := range def.Skills {
		in.CharacterModel.Skills = append(in.CharacterModel.Skills, skillRef{Key: s.Key, Title: s.Title, Attribute: s.Attribute})
	}
	byLocation := map[model.LocationID][]character{}
	nick := map[model.CharacterID]string{}
	for _, c := range snap.Characters {
		nick[c.ID] = c.Nickname
		if c.LocationID == nil || c.Status == model.CharacterDead {
			continue
		}
		byLocation[*c.LocationID] = append(byLocation[*c.LocationID], character{
			CharacterID: c.ID, Nickname: c.Nickname, Status: c.Status,
			Attributes: nonNilMap(c.Attributes), Skills: nonZero(c.Skills), Conditions: nonNil(c.Conditions), Knowledge: lastFacts(c.Knowledge),
		})
	}
	for _, l := range snap.Locations {
		chars := byLocation[l.ID]
		if len(chars) == 0 {
			continue // сцена без персонажей модели не нужна
		}
		sc := scene{LocationID: l.ID, Name: l.Name, Description: l.Description, Difficulty: l.Difficulty, Exits: []exit{}, Objects: []object{}, Characters: chars}
		for _, link := range l.Links {
			if !link.Hidden {
				sc.Exits = append(sc.Exits, exit{To: link.To, Title: link.Title})
			}
		}
		for _, o := range l.Objects {
			if o.HiddenUntil && !o.Revealed {
				continue
			}
			obj := object{Key: o.Key, Title: o.Title, Description: o.Description, State: o.State, Approaches: []approach{}}
			for i, ap := range o.Approaches {
				obj.Approaches = append(obj.Approaches, approach{Idx: i, Action: ap.Action, Title: ap.Title, Attribute: ap.Attribute, Skill: ap.Skill})
			}
			sc.Objects = append(sc.Objects, obj)
		}
		in.Scenes = append(in.Scenes, sc)
	}
	sort.Slice(in.Scenes, func(i, j int) bool { return in.Scenes[i].LocationID < in.Scenes[j].LocationID })
	for _, s := range snap.Submissions {
		in.Submissions = append(in.Submissions, submission{SubmissionID: s.SubmissionID, CharacterID: s.CharacterID, Nickname: nick[s.CharacterID], Text: s.Text})
	}
	return in
}

// Encode — JSON контекста для поля Input запроса.
func (in Input) Encode() string {
	b, err := json.Marshal(in)
	if err != nil {
		panic(fmt.Sprintf("interpreter.Input: %v", err))
	}
	return string(b)
}

func lastFacts(facts []string) []string {
	out := []string{}
	start := max(len(facts)-knowledgeLast, 0)
	for _, f := range facts[start:] {
		r := []rune(f)
		if len(r) > knowledgeMaxRune {
			f = string(r[:knowledgeMaxRune]) + "…"
		}
		out = append(out, f)
	}
	return out
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func nonNilMap(m map[string]int) map[string]int {
	if m == nil {
		return map[string]int{}
	}
	return m
}

func nonZero(m map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range m {
		if v != 0 {
			out[k] = v
		}
	}
	return out
}
