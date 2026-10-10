# Документация A Tale Together

Проект: асинхронная многопользовательская текстовая RPG с ИИ-мастером в Telegram. Рабочее название в концепте: Massive D&D. Правила ведения документов: `.claude/rules/docs.md`.

## Разделы

| Раздел | Содержание |
|---|---|
| [Концепт](concept/massive_dnd_concept.md), [риски реализации](concept/massive_dnd_implementation_risks.md) | Документы руководителя: продукт, принципы, план. Claude не редактирует |
| [План реализации](plan/README.md) | Крупные пункты по порядку (техника, фичи, devops) и файл-скелет на каждый |
| [Roadmap](roadmap.md) | Этапы концепта и их связь с пунктами плана |
| [Глоссарий проекта](glossary.md), [глоссарий игры](game/glossary.md) | Единая терминология: технические и игровые термины; неудачные термины заменяются с пометкой «Было» |
| [Архитектура](architecture/overview.md) | [Компоненты](architecture/overview.md), [слои](architecture/layers.md), [структура репозитория](architecture/project-layout.md), [модель данных](architecture/data-model.md), [конвейер раунда](architecture/round-pipeline.md), [команды Telegram](architecture/telegram-commands.md) |
| [Решения (ADR)](decisions/README.md) | Принятые и ожидающие архитектурные решения |
| [Игра](game/README.md) | [Глоссарий игры](game/glossary.md), [форматы игр](game/game-formats.md), [режимы игр](game/game-modes.md), [сюжетные цели](game/story-goals.md), [правила](game/rules.md), [персонаж](game/character.md), [испытания](game/checks.md), [каталог действий](game/actions.md), [раунд](game/round.md), [эталонные сценарии](game/scenarios.md), [вопросы руководителю](game/open-questions.md) |
| [LLM](llm/README.md) | [Конвейер и бюджет](llm/README.md), [провайдеры и профили](llm/providers.md), [интерпретатор](llm/interpreter.md), [рассказчик](llm/narrator.md) |
| [Разработка](dev/environment.md) | [Правила программирования](dev/coding-rules.md), [среда](dev/environment.md), [рабочий процесс](dev/workflow.md) |

## Система промптов Claude Code

| Файл | Роль |
|---|---|
| `CLAUDE.md` | Точка входа; импортирует главный скилл |
| `.claude/skills/tale-team/` | Главный скилл: роли, принципы, карта, цикл задачи |
| `.claude/skills/tale-architect/`, `tale-gamedesign/`, `tale-prompts/`, `tale-env/` | Скиллы-процессы, по запросу |
| `.claude/rules/*.md` | Правила областей, подключаются при касании файлов по `paths` |
