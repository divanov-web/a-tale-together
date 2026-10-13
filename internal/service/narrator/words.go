package narrator

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"taletogether/internal/model"
)

// Describe — событие словами: кто действует и что произошло; для хроники без модели и для контекста рассказчика.
// Второе событие перехода (копия для локации назначения) описывается как приход. У событий группы актёр — «группа»
// без названия (названия групп игрокам и модели не показываются, решение руководителя 2026-10-13); лидер не называется (пункт 19).
func Describe(ev model.Event, state *model.RoundSnapshot) (actor, summary string) {
	switch ev.Type {
	case model.EventPartySplit:
		actor = "группа"
		names := payloadList(ev, "nicknames")
		verb := "отделяется от группы и уходит своей дорогой"
		if strings.Contains(names, ", ") {
			verb = "отделяются от группы и уходят своей дорогой"
		}
		return actor, fmt.Sprintf("%s "+verb+" из «%s» в «%s»", names, payloadStr(ev, "from_name"), payloadStr(ev, "to_name"))
	case model.EventPartyHeld:
		actor = "группа"
		wanted := heldWords(ev)
		if to := payloadStr(ev, "to_name"); to != "" {
			return actor, fmt.Sprintf("группа держится вместе и уходит из «%s» в «%s»%s", payloadStr(ev, "from_name"), to, wanted)
		}
		return actor, fmt.Sprintf("группа держится вместе и остаётся в «%s»%s", payloadStr(ev, "from_name"), wanted)
	}
	actor = nickname(ev, state, "character_id")
	switch ev.Type {
	case model.EventCharacterMoved:
		if ev.LocationID != nil && model.LocationID(payloadNum(ev, "from")) != *ev.LocationID {
			return actor, fmt.Sprintf("%s приходит из «%s» в «%s»", actor, payloadStr(ev, "from_name"), payloadStr(ev, "to_name"))
		}
		return actor, fmt.Sprintf("%s уходит из «%s» в «%s»", actor, payloadStr(ev, "from_name"), payloadStr(ev, "to_name"))
	case model.EventObjectChanged:
		if payloadStr(ev, "to") != "" {
			return actor, fmt.Sprintf("%s: «%s» теперь %s", actor, payloadStr(ev, "title"), StateWords(payloadStr(ev, "to")))
		}
		return actor, fmt.Sprintf("%s находит: %s", actor, payloadStr(ev, "title"))
	case model.EventCheckRolled:
		info := checkOf(ev)
		what := payloadStr(ev, "summary")
		if what == "" {
			what = "действует"
		}
		if info.Pushed {
			return actor, fmt.Sprintf("%s %s, форсирует — %s", actor, what, OutcomeWords(info.Outcome))
		}
		return actor, fmt.Sprintf("%s %s — %s", actor, what, OutcomeWords(info.Outcome))
	case model.EventCharacterDied:
		actor = payloadStr(ev, "nickname")
		return actor, fmt.Sprintf("%s выбывает из истории", actor)
	case model.EventCharacterReturned:
		actor = payloadStr(ev, "nickname")
		return actor, fmt.Sprintf("%s возвращается в историю", actor)
	case model.EventConditionAdded:
		return actor, fmt.Sprintf("%s: состояние «%s»", actor, ConditionWords(payloadStr(ev, "condition")))
	case model.EventHPChanged:
		return actor, fmt.Sprintf("%s: HP %s", actor, payloadStr(ev, "to"))
	case model.EventFactLearned:
		return actor, fmt.Sprintf("%s узнаёт: %s", actor, payloadStr(ev, "fact"))
	case model.EventCellDiscovered:
		return actor, fmt.Sprintf("%s первым из своих открывает «%s»", actor, payloadStr(ev, "name"))
	case model.EventCellGenerated:
		if payloadStr(ev, "stage") == string(model.StageCard) {
			return "мир", fmt.Sprintf("на горизонте «%s» (%s, %s)", payloadStr(ev, "name"), payloadStr(ev, "x"), payloadStr(ev, "y"))
		}
		return "мир", fmt.Sprintf("открывается «%s»: %s", payloadStr(ev, "name"), payloadStr(ev, "description"))
	case model.EventObjectCreated:
		var p struct {
			Object model.LocationObject `json:"object"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		return "мир", fmt.Sprintf("появляется объект «%s»", p.Object.Title)
	case model.EventQuestIssued:
		return "мир", fmt.Sprintf("у фракции новое дело: «%s»", payloadStr(ev, "quest"))
	case model.EventQuestStagePlaced:
		return "мир", fmt.Sprintf("«%s»: этап «%s» ждёт в «%s»", payloadStr(ev, "quest"), payloadStr(ev, "stage"), payloadStr(ev, "cell"))
	case model.EventQuestStageDone:
		return "мир", fmt.Sprintf("«%s»: этап «%s» выполнен", payloadStr(ev, "quest"), payloadStr(ev, "stage"))
	case model.EventQuestDone:
		return "мир", fmt.Sprintf("«%s» завершён", payloadStr(ev, "quest"))
	case model.EventGoalRevised:
		return "мир", fmt.Sprintf("цель фракции: %s", payloadStr(ev, "text"))
	}
	return actor, fmt.Sprintf("%s: %s", actor, strings.ReplaceAll(string(ev.Type), "_", " "))
}

// OutcomeWords — исход проверки словами.
func OutcomeWords(outcome string) string {
	switch outcome {
	case model.OutcomeSuccess:
		return "успех"
	case model.OutcomeCostlySuccess:
		return "успех с ценой"
	case model.OutcomeFailure:
		return "провал"
	}
	return outcome
}

// StateWords — состояние объекта словами.
func StateWords(state string) string {
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

// ConditionWords — состояние персонажа словами.
func ConditionWords(c string) string {
	switch c {
	case model.ConditionWounded:
		return "ранен"
	case model.ConditionExhausted:
		return "истощён"
	case model.ConditionHidden:
		return "скрыт"
	case model.ConditionDetained:
		return "задержан"
	}
	return c
}

// StatusWords — статус персонажа словами для подписи сцены.
func StatusWords(st model.CharacterStatus) string {
	switch st {
	case model.CharacterWaiting:
		return "выжидает"
	case model.CharacterLeaving:
		return "покидает повествование"
	case model.CharacterInactive:
		return "выбыл из повествования"
	case model.CharacterDead:
		return "выбыл до возвращения"
	}
	return ""
}

func nickname(ev model.Event, state *model.RoundSnapshot, key string) string {
	id := model.CharacterID(payloadNum(ev, key))
	if id == 0 && ev.CharacterID != nil {
		id = *ev.CharacterID
	}
	if state != nil {
		if c, ok := state.Character(id); ok {
			return c.Nickname
		}
	}
	if n := payloadStr(ev, "nickname"); n != "" {
		return n
	}
	return "кто-то"
}

func checkOf(ev model.Event) model.CheckInfo {
	var p struct {
		Check model.CheckInfo `json:"check"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	return p.Check
}

func payloadStr(ev model.Event, key string) string {
	var p map[string]any
	_ = json.Unmarshal(ev.Payload, &p)
	switch v := p[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

// heldWords — кто куда хотел уйти из удержанной группы: «; Паладин хотел в «Рынок»».
func heldWords(ev model.Event) string {
	var p struct {
		Held []struct {
			Nickname string `json:"nickname"`
			Wanted   string `json:"wanted"`
		} `json:"held"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	var parts []string
	for _, h := range p.Held {
		parts = append(parts, fmt.Sprintf("%s хотел в «%s»", h.Nickname, h.Wanted))
	}
	if len(parts) == 0 {
		return ""
	}
	return "; " + strings.Join(parts, ", ")
}

// payloadList — строковый массив полезной нагрузки через запятую.
func payloadList(ev model.Event, key string) string {
	var p map[string]any
	_ = json.Unmarshal(ev.Payload, &p)
	list, _ := p[key].([]any)
	var out []string
	for _, v := range list {
		out = append(out, fmt.Sprint(v))
	}
	return strings.Join(out, ", ")
}

func payloadNum(ev model.Event, key string) float64 {
	var p map[string]any
	_ = json.Unmarshal(ev.Payload, &p)
	v, _ := p[key].(float64)
	return v
}
