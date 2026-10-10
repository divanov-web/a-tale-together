package narrator

import "encoding/json"

// PromptVersion — версия промпта рассказчика, пишется в `llm_runs.prompt_version` и `rounds.narration`.
const PromptVersion = "narrator/v2"

// Schema — JSON-схема ответа модели: общий текст раунда и блок на сцену с упомянутыми событиями (docs/llm/round-calls.md).
var Schema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "chronicle": {"type": "string"},
    "scenes": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "location_id": {"type": "integer"},
          "party": {"type": "string"},
          "text": {"type": "string"},
          "event_seqs": {"type": "array", "items": {"type": "integer"}}
        },
        "required": ["location_id", "party", "text", "event_seqs"],
        "additionalProperties": false
      }
    }
  },
  "required": ["chronicle", "scenes"],
  "additionalProperties": false
}`)

// response — ответ модели по Schema.
type response struct {
	Chronicle string     `json:"chronicle"`
	Scenes    []rawScene `json:"scenes"`
}

type rawScene struct {
	LocationID int64   `json:"location_id"`
	Party      string  `json:"party"`
	Text       string  `json:"text"`
	EventSeqs  []int64 `json:"event_seqs"`
}
