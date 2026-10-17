# Прод-сервер

VPS Fornex, Ubuntu 24.04, 1 vCPU, 1 ГБ ОЗУ + swap 2 ГБ, диск 10 ГБ. На той же машине VPN Amnezia (контейнер на 443), бот ему не мешает. Решения: [ADR-0040](../decisions/0040-prod-server.md) (сервер, сервис, бэкап), [ADR-0041](../decisions/0041-binary-release.md) (релиз бинарником). Технически прод, по ресурсам пока тестовый стенд. Доступы, состояние машины и журнал изменений ведутся в проекте «Дом» (карточка `fornex`, скилл `/home-fornex`), здесь только то, что относится к боту.

## Устройство

| Что | Где |
|---|---|
| Пользователь | `divanov` (ssh по ключу, `sudo` без пароля); алиас `ssh fornex-dev` с домашней машины. `root` только для администрирования (`ssh fornex`) |
| Папка бота | `~/Projects/a-tale-together` (не `~/Go`: имя сталкивалось с `~/go`): `.env`, `bin/bot` → `../releases/bot-<версия>`, `releases/` (бинарники, `.previous` — прежняя версия), `deploy/` (копии юнита, logrotate, бэкапа), `logs/`. Исходников, Go, git и goose на сервере нет |
| claude | `~/.local/bin/claude` (Claude Code, нативная установка), вход под `divanov` по подписке; нужен провайдеру `claude-cli` |
| PostgreSQL | 16 из apt, localhost:5432, роль `tale`, база `tale`. Тестовой и имитаторской баз нет |
| Конфигурация | `.env`: `APP_ENV=prod`, `LOG_LEVEL=info`, `MIGRATE_ON_START=false`, `TELEGRAM_BOT_TOKEN` отдельного бота `@a_tale_together_bot`, `CLAUDE_CLI_PATH=/home/divanov/.local/bin/claude` |
| Сервис | `tale-bot` (systemd, `deploy/tale-bot.service`), автозапуск, перезапуск при падении |
| Лог | `logs/bot.log` (тот же путь, что дома), ротация `/etc/logrotate.d/tale-bot`; журнал чатов `logs/chats/`, 30 дней |
| Бэкап | `/etc/cron.daily/tale-backup` → `/var/backups/tale/tale-<дата>.dump`, 14 дней |

## Релиз

Делается с домашней машины скриптом `deploy/release.sh` только по команде руководителя (скилл `/tale-release`, ADR-0041). Коротко: `make release RELEASE_ARGS=check` показывает, что накопилось; `make release RELEASE_ARGS=all` собирает статический бинарник, заливает его, останавливает сервис, применяет миграции (`bot -migrate`), переключает `bin/bot`, запускает и проверяет лог. Релиз только из `origin/master`: сначала `git push` руководителя. Журнал: [releases.md](releases.md).

## Команды на сервере (под `divanov`, из `~/Projects/a-tale-together`)

| Команда | Действие |
|---|---|
| `systemctl status tale-bot` | состояние сервиса |
| `sudo systemctl restart tale-bot` / `stop` / `start` | управление сервисом; `make` на сервере нет |
| `tail -n 50 logs/bot.log` | хвост лога |
| `bin/bot -migrate-status` | статус миграций на базе `tale` (встроенные в бинарник) |
| `bin/bot -migrate` | применить миграции; обычно делает `release.sh`, при остановленном сервисе |
| `readlink bin/bot`, `ls releases/` | какая версия работает, какие лежат рядом |
| `sudo /etc/cron.daily/tale-backup` | бэкап вручную |
| `sudo -u postgres pg_restore -d tale --clean --if-exists /var/backups/tale/tale-<дата>.dump` | восстановление (сервис остановить) |

## Ограничения и планы

- ОЗУ 1 ГБ: каждый вызов LLM через `claude-cli` поднимает процесс Node (300–500 МБ), параллелизм профиля 2. Для нагрузки реальной группы нужен тариф с 2 ГБ ОЗУ и 20 ГБ диска.
- Подписка Claude Code допустима для теста; для прода с чужими игроками нужен провайдер `anthropic-api` (пункт плана 08, вопрос T2 в [open-questions](../game/open-questions.md)).
- Фаервол не включён: наружу слушают только ssh 22 и Amnezia 443, Postgres привязан к localhost. `ufw` рядом с Docker ломает маршрутизацию VPN, поэтому не ставится без отдельного решения.
- Полный путь `/home/divanov/Projects/a-tale-together` прописан в `deploy/tale-bot.service`, `deploy/tale-bot.logrotate` и как умолчание `RELEASE_DIR` в `deploy/release.sh`.
