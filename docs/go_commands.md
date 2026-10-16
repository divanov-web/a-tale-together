# Команды запуска бота

Команды выполняются из WSL в корне проекта. После правок кода и миграций Claude сам делает `make restart` и проверяет `make logs`; руководитель пользуется теми же командами при необходимости (правило 2026-10-10). Базу (`make migrate`, правка строк запущенных игр) тоже обновляет Claude.

| Команда | Действие |
|---|---|
| `make start` | собрать и запустить бота в фоне: pid `bin/bot.pid`, лог `logs/bot.log`; снимает экземпляр без pid-файла |
| `make restart` | `stop` + `start`; после каждой правки кода и после миграций |
| `make stop` | остановить фонового бота (и экземпляр без pid-файла) |
| `make sim` | прогон партии ИИ-игроками на БД `DATABASE_URL_SIM` без Telegram (`SIM_ARGS="-run sim/runs/basic.json -rounds 3 -model-narrator sonnet"`); отчёт `logs/sim/<метка>/` — [dev/simulation.md](dev/simulation.md) |
| `make migrate-sim` | миграции на БД прогонов `DATABASE_URL_SIM` |
| `make wipe` | вайп песочницы (только `APP_ENV=dev`): `stop`, удаление всех игр и связанных данных по `scripts/wipe-sandbox.sql` (пользователи, чат, настройки и профили LLM остаются), очистка `logs/chats`, `logs/images`, `logs/bot.log`, `start`; выполняет Claude по просьбе руководителя (`/tale-wipe`) |

Проверка после запуска: `make logs` — строка `bot starting` с новой версией, без `409 Conflict`; `/ping` в песочнице отвечает новой версией.

## Запуск в терминале с отладкой

Для отладки бот запускается в этом же терминале, весь лог идёт на экран. Уровень лога задаёт `LOG_LEVEL` в `.env` (`debug|info|warn|error`, по умолчанию `info`).

| Команда | Действие |
|---|---|
| `make run LOG_LEVEL=debug` | разовый запуск с уровнем `debug` без правки `.env` (переменная make перекрывает значение из `.env`) |
| `tail -f logs/bot.log` | лог фонового бота в реальном времени; `Ctrl+C` выходит из просмотра, бот продолжает работать |
| `logs/chats/<chat_id>/<день>.jsonl` | журнал чата: входящие, ответы, вызовы LLM, события раунда ([dev/chat-logs.md](dev/chat-logs.md)) |

Остальные команды (`check`, `build`, `test`, `lint`, `migrate`, `logs`) — в [dev/environment.md](dev/environment.md).
