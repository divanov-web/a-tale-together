package narrator

import "encoding/json"

// PromptVersion — версия промпта рассказчика, пишется в `llm_runs.prompt_version` и `rounds.narration`.
const PromptVersion = "narrator/v14"

// Schema — JSON-схема ответа модели: общий текст раунда, блок на сцену (`scene_id` из входа: группа в локации или
// персонажи без группы) с упомянутыми событиями (docs/llm/round-calls.md) и пакет мира `world` (пункт 27: клетки
// для детализации и карточки соседей по заданиям `generate`; без заданий — пустые массивы). Локацию и подпись
// сцены подставляет код. Пункт 28: `world.quests`, `world.stages`, `world.goals` по заданиям `generate.quests`,
// `generate.stages`, `generate.goals`. Пункт 35: `hints_given` — коды фракций из `hints` входа, которым дан намёк
// (ADR-0038); без просьб — пустой массив.
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
    },
    "world": {
      "type": "object",
      "properties": {
        "cells": {"type": "array", "items": ` + cellSchema + `},
        "neighbors": {"type": "array", "items": ` + neighborSchema + `},
        "quests": {"type": "array", "items": ` + questSchema + `},
        "stages": {"type": "array", "items": ` + stagePlacementSchema + `},
        "goals": {"type": "array", "items": ` + goalSchema + `}
      },
      "required": ["cells", "neighbors", "quests", "stages", "goals"],
      "additionalProperties": false
    },
    "hints_given": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["chronicle", "scenes", "world", "hints_given"],
  "additionalProperties": false
}`)

// response — ответ модели по Schema.
type response struct {
	Chronicle string     `json:"chronicle"`
	Scenes    []rawScene `json:"scenes"`
	World     rawWorld   `json:"world"`
	Hints     []string   `json:"hints_given"`
}

type rawScene struct {
	SceneID   int     `json:"scene_id"`
	Text      string  `json:"text"`
	EventSeqs []int64 `json:"event_seqs"`
}
