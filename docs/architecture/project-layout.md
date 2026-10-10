# Структура репозитория

Слои и их зависимости: [layers.md](layers.md). Правила кода: [coding-rules.md](../dev/coding-rules.md).

```text
a-tale-together/
├── CLAUDE.md, .claude/          система промптов Claude Code
├── cmd/bot/main.go              сборка зависимостей и запуск
├── internal/
│   ├── config/                  дефолты, .env/ENV, Validate
│   ├── model/                   сущности, события, намерения, статусы
│   ├── app/                     сценарии: bot, chat, game, round, scheduler, delivery
│   ├── service/                 логика: rules, engine, visibility, interpreter, narrator
│   └── adapter/                 внешнее: postgres, telegram, llm
├── migrations/                  SQL-миграции goose (embed)
├── assets/prompts/<роль>/vN.md  промпты LLM (embed) и eval-наборы
├── docs/                        документация
├── docker/db-init/              инициализация тестовой БД для docker-compose
├── Makefile, docker-compose.yml, .env.example
└── go.mod                       модуль taletogether
```

Каждый пакет содержит `readme.md`. Новый пакет внутри слоя — когда есть второй потребитель или существующий пакет перерос ~500 строк; новый слой — через ADR.
