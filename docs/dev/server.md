# Прод-сервер

VPS Fornex, Ubuntu 24.04, 1 vCPU, 1 ГБ ОЗУ + swap 2 ГБ, диск 10 ГБ. На той же машине VPN Amnezia (контейнер на 443), бот ему не мешает. Решение: [ADR-0040](../decisions/0040-prod-server.md). Технически прод, по ресурсам пока тестовый стенд. Доступы, состояние машины и журнал изменений ведутся в проекте «Дом» (карточка `fornex`), здесь только то, что относится к боту.

## Устройство

| Что | Где |
|---|---|
| Пользователь | `divanov` (ssh по ключу, `sudo` без пароля); алиас `ssh fornex-dev` с домашней машины. `root` только для администрирования (`ssh fornex`) |
| Код | `~/Go/a-tale-together`, клон `https://github.com/divanov-web/a-tale-together`, всегда равен `origin/master` |
| Go, goose, claude | `~/.local/go/bin/go` 1.27.2, `~/.local/bin/goose`, `~/.local/bin/claude` (Claude Code, нативная установка) |
| PostgreSQL | 16 из apt, localhost:5432, роль `tale`, база `tale`. Тестовой и имитаторской баз нет |
| Конфигурация | `~/Go/a-tale-together/.env`: `APP_ENV=prod`, `LOG_LEVEL=info`, `TELEGRAM_BOT_TOKEN` отдельного бота, `CLAUDE_CLI_PATH=/home/divanov/.local/bin/claude` |
| Сервис | `tale-bot` (systemd, `deploy/tale-bot.service`), автозапуск, перезапуск при падении |
| Лог | `logs/bot.log` (тот же путь, что дома), ротация `/etc/logrotate.d/tale-bot`; журнал чатов `logs/chats/`, 30 дней |
| Бэкап | `/etc/cron.daily/tale-backup` → `/var/backups/tale/tale-<дата>.dump`, 14 дней |

## Команды (под `divanov`, из `~/Go/a-tale-together`)

| Команда | Действие |
|---|---|
| `deploy/deploy.sh` | обновить код до `origin/master`, собрать, миграции, системные файлы, перезапустить. Основной способ выкатки после `git push` руководителя |
| `systemctl status tale-bot` | состояние сервиса |
| `sudo systemctl restart tale-bot` / `stop` / `start` | управление сервисом; `make start/stop/restart` на сервере не использовать |
| `make logs` | хвост `logs/bot.log` |
| `make migrate-status` | статус миграций на базе `tale` |
| `sudo /etc/cron.daily/tale-backup` | бэкап вручную |
| `sudo -u postgres pg_restore -d tale --clean --if-exists /var/backups/tale/tale-<дата>.dump` | восстановление (сервис остановить) |

## Первый запуск: что осталось сделать руководителю

1. Создать прод-бота в BotFather (другой, не домашний тестовый: один токен — один экземпляр) и прислать токен Claude; он запишет его в `.env` сервера и запустит сервис.
2. Войти в Claude Code на сервере под `divanov`, иначе профиль `local-claude` не сможет вызывать модель: `ssh fornex-dev`, затем `claude` → ссылка для входа в браузере → код обратно в терминал. Проверка: `claude -p --model haiku 'ответь ok'`.
3. Добавить бота в прод-группу, дальше как дома (`/ping`, `/help`).

## Ограничения и планы

- ОЗУ 1 ГБ: каждый вызов LLM через `claude-cli` поднимает процесс Node (300–500 МБ), параллелизм профиля 2. Для нагрузки реальной группы нужен тариф с 2 ГБ ОЗУ и 20 ГБ диска (после установки свободно около 1.3 ГБ).
- Подписка Claude Code допустима для теста; для прода с чужими игроками нужен провайдер `anthropic-api` (пункт плана 08, вопрос T2 в [open-questions](../game/open-questions.md)).
- Фаервол не включён: наружу слушают только ssh 22 и Amnezia 443, Postgres привязан к localhost. `ufw` рядом с Docker ломает маршрутизацию VPN, поэтому не ставится без отдельного решения.
- Дома и на сервере один и тот же пользователь и путь к проекту, поэтому `.env.example`, Makefile и скиллы работают в обоих местах без правок.
