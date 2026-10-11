# Документация A Tale Together

Проект: асинхронная многопользовательская текстовая RPG с ИИ-мастером в Telegram. Рабочее название в концепте: Massive D&D. Правила ведения документов: `.claude/rules/docs.md`.

## Разделы

| Раздел | Содержание |
|---|---|
| [Концепт](concept/massive_dnd_concept.md), [риски реализации](concept/massive_dnd_implementation_risks.md) | Не источник правды: исходные документы руководителя, в них смотрят только за нереализованными механиками; реализованное адаптируется под проект, переносится в профильный документ и удаляется из концепта |
| [План реализации](plan/README.md) | Крупные пункты по порядку (техника, фичи, devops) и файл-скелет на каждый; [доработки вне плана](plan/backlog.md) — очередь замечаний ручного тестирования, [выполненные](plan/backlog-done.md) отдельно |
| [Roadmap](roadmap.md) | Этапы концепта и их связь с пунктами плана |
| [Глоссарий проекта](glossary.md), [глоссарий игры](game/glossary.md) | Единая терминология: технические и игровые термины; неудачные термины заменяются с пометкой «Было» |
| [Архитектура](architecture/overview.md) | [Компоненты](architecture/overview.md), [слои](architecture/layers.md), [структура репозитория](architecture/project-layout.md), [модель данных](architecture/data-model.md), [конвейер раунда](architecture/round-pipeline.md), [команды Telegram](architecture/telegram-commands.md) |
| [Руководство игрока](../guide/README.md) | Материалы со стороны пользователя, вне `docs/`: [команды игры](../guide/commands.md) по пути владельца чата и игрока, [персонаж](../guide/character/README.md) и [испытания](../guide/checks/checks.md) человеческим языком; правила ведения `.claude/rules/guide.md` |
| [Решения (ADR)](decisions/README.md) | Принятые и ожидающие архитектурные решения |
| [Игра](game/README.md) | [Глоссарий игры](game/glossary.md), [форматы игр](game/game-formats.md), [режимы игр](game/game-modes.md), [сюжетные цели](game/story-goals.md), [правила](game/rules.md), [персонаж](game/character.md), [смерть персонажа](game/death.md), [испытания](game/checks.md), [фракции](game/factions.md), [группы](game/parties.md), [модели](game/models/README.md), [каталог действий](game/actions.md), [раунд](game/round.md), [эталонные сценарии](game/scenarios.md), [вопросы руководителю](game/open-questions.md) |
| [LLM](llm/README.md) | [Конвейер и бюджет](llm/README.md), [два вызова раунда: шаги, входы, выходы, пример](llm/round-calls.md), [провайдеры и профили](llm/providers.md), [интерпретатор](llm/interpreter.md), [рассказчик](llm/narrator.md) |
| [Разработка](dev/environment.md) | [Правила программирования](dev/coding-rules.md), [среда](dev/environment.md), [команды запуска бота](go_commands.md), [рабочий процесс](dev/workflow.md), [журнал чатов](dev/chat-logs.md) |

## Система промптов Claude Code

| Файл | Роль |
|---|---|
| `CLAUDE.md` | Точка входа; импортирует главный скилл |
| `.claude/skills/tale-team/` | Главный скилл: роли, принципы, карта, цикл задачи |
| `.claude/skills/tale-architect/`, `tale-gamedesign/`, `tale-prompts/`, `tale-env/`, `tale-debug/` | Скиллы-процессы, по запросу |
| `.claude/rules/*.md` | Правила областей, подключаются при касании файлов по `paths` |
