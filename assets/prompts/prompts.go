// Package prompts — версионированные промпты LLM, встроенные в бинарь (`<роль>/vN.md`), и оценочные наборы.
package prompts

import "embed"

// FS — файлы промптов: `interpreter/v1.md`, `interpreter/eval/*.json`.
//
//go:embed interpreter
var FS embed.FS
