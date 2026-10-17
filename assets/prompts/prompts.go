// Package prompts — версионированные промпты LLM, встроенные в бинарь (`<роль>/vN.md`), и оценочные наборы.
package prompts

import "embed"

// FS — файлы промптов и оценочных наборов: `interpreter/v1.md`, `interpreter/eval/*.json`, `narrator/v1.md`, `narrator/eval/*.json`,
// `player/v2.md` (ИИ-игрок имитатора, ADR-0034; `v2` = `v1` + адресные ответы бота в группе, B50).
//
//go:embed interpreter narrator player
var FS embed.FS
