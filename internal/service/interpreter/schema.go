package interpreter

import "encoding/json"

// PromptVersion — версия промпта интерпретатора, пишется в `llm_runs.prompt_version`.
const PromptVersion = "interpreter/v5"

// Schema — JSON-схема ответа модели: массив намерений по одному на заявку (docs/llm/interpreter.md).
// Все поля обязательны, необязательные значения — null: так схему принимают провайдеры со строгим режимом.
// `flavor` — окраска действия (пункт 38, ADR-0039): безмеханическая часть заявки для рассказчика, пустая строка без неё.
var Schema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "intents": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "submission_id": {"type": "integer"},
          "character_id": {"type": "integer"},
          "action_type": {"type": "string", "enum": ["move", "explore", "interact", "help", "wait", "attack", "sneak", "persuade", "trade"]},
          "target": {"type": "string"},
          "approach_idx": {"type": ["integer", "null"]},
          "attribute": {"type": ["string", "null"]},
          "skill": {"type": ["string", "null"]},
          "risk": {"type": ["string", "null"], "enum": ["normal", "hard", "extreme", null]},
          "on_success": {
            "type": ["object", "null"],
            "properties": {
              "fact": {"type": ["string", "null"]},
              "move": {"type": ["string", "null"]},
              "reveal": {"type": ["string", "null"]}
            },
            "required": ["fact", "move", "reveal"],
            "additionalProperties": false
          },
          "on_fail": {
            "type": ["object", "null"],
            "properties": {
              "condition": {"type": ["string", "null"]},
              "damage": {"type": ["integer", "null"]},
              "fact": {"type": ["string", "null"]}
            },
            "required": ["condition", "damage", "fact"],
            "additionalProperties": false
          },
          "push": {"type": "boolean"},
          "flavor": {"type": "string"},
          "ambiguities": {"type": "array", "items": {"type": "string"}},
          "fallback": {
            "type": ["object", "null"],
            "properties": {
              "action_type": {"type": "string", "enum": ["move", "explore", "wait"]},
              "target": {"type": "string"}
            },
            "required": ["action_type", "target"],
            "additionalProperties": false
          }
        },
        "required": ["submission_id", "character_id", "action_type", "target", "approach_idx", "attribute", "skill", "risk", "on_success", "on_fail", "push", "flavor", "ambiguities", "fallback"],
        "additionalProperties": false
      }
    }
  },
  "required": ["intents"],
  "additionalProperties": false
}`)

// response — ответ модели по Schema.
type response struct {
	Intents []rawIntent `json:"intents"`
}

// rawIntent — намерение в ответе модели до валидации.
type rawIntent struct {
	SubmissionID int64        `json:"submission_id"`
	CharacterID  int64        `json:"character_id"`
	ActionType   string       `json:"action_type"`
	Target       string       `json:"target"`
	ApproachIdx  *int         `json:"approach_idx"`
	Attribute    *string      `json:"attribute"`
	Skill        *string      `json:"skill"`
	Risk         *string      `json:"risk"`
	OnSuccess    *successEff  `json:"on_success"`
	OnFail       *failEff     `json:"on_fail"`
	Push         bool         `json:"push"`
	Flavor       string       `json:"flavor"`
	Ambiguities  []string     `json:"ambiguities"`
	Fallback     *rawFallback `json:"fallback"`
}

type successEff struct {
	Fact   *string `json:"fact"`
	Move   *string `json:"move"`
	Reveal *string `json:"reveal"`
}

type failEff struct {
	Condition *string `json:"condition"`
	Damage    *int    `json:"damage"`
	Fact      *string `json:"fact"`
}

type rawFallback struct {
	ActionType string `json:"action_type"`
	Target     string `json:"target"`
}
