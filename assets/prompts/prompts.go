// Package prompts — версионированные промпты LLM, встроенные в бинарь (`<роль>/vN.md`), и оценочные наборы.
package prompts

import "embed"

// FS — файлы промптов и оценочных наборов: `interpreter/v1.md`, `interpreter/eval/*.json`, `narrator/v1.md`, `narrator/eval/*.json`.
//
//go:embed interpreter narrator
var FS embed.FS
