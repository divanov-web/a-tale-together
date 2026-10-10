# prompts
- Назначение: версионированные промпты LLM `<роль>/vN.md` (`interpreter`, `narrator`) и оценочные наборы `<роль>/eval/`.
- Пакет `prompts` (`prompts.go`): `FS` через `embed` для `service/*` и `cmd/eval-interpreter`; промпт читается по пути `interpreter/v1.md`, версия — константа `interpreter.PromptVersion`.
- Оценочный набор интерпретатора: `interpreter/eval/cases.json` (снимок, модель персонажа, случаи с ожиданиями); прогон `make eval-interpreter`.
- Правило: новая версия файлом; версия пишется в `llm_runs`; дизайн по `/tale-prompts`, `docs/llm/`.
