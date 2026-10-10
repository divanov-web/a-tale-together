# prompts
- Назначение: версионированные промпты LLM `<роль>/vN.md` (`interpreter`, `narrator`) и оценочные наборы `<роль>/eval/`.
- Пакет `prompts` (`prompts.go`): `FS` через `embed` для `service/*` и `cmd/eval-*`; промпт читается по пути из версии (`interpreter/v1.md`, `narrator/v2.md`), версия — константы `interpreter.PromptVersion`, `narrator.PromptVersion`; `narrator/v2` добавил правило входа персонажа (`entry`, B1).
- Оценочные наборы: интерпретатор `interpreter/eval/cases.json` (снимок, модель персонажа, случаи с ожиданиями полей намерений; `make eval-interpreter`); рассказчик `narrator/eval/cases.json` (мир, фракции, модель персонажа, случаи с состоянием, событиями, описаниями и ожиданиями: нужные и запрещённые подстроки, без цифр; `make eval-narrator`).
- Правило: новая версия файлом; версия пишется в `llm_runs`; дизайн по `/tale-prompts`, `docs/llm/`.
