package narrator

import "encoding/json"

// PromptVersion — версия промпта рассказчика, пишется в `llm_runs.prompt_version` и `rounds.narration`.
const PromptVersion = "narrator/v3"

// Schema — JSON-схема ответа модели: общий текст раунда и блок на сцену (`scene_id` из входа: группа в локации или
// персонажи без группы) с упомянутыми событиями (docs/llm/round-calls.md). Локацию и подпись сцены подставляет код.
var Schema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "chronicle": {"type": "string"},
    "scenes": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "scene_id": {"type": "integer"},
          "text": {"type": "string"},
          "event_seqs": {"type": "array", "items": {"type": "integer"}}
        },
        "required": ["scene_id", "text", "event_seqs"],
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
	SceneID   int     `json:"scene_id"`
	Text      string  `json:"text"`
	EventSeqs []int64 `json:"event_seqs"`
}
