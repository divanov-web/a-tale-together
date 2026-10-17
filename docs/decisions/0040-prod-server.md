# ADR-0040: Прод-сервер на VPS: systemd, деплой из origin/master, бэкап

- Статус: принято
- Дата: 2026-10-17
- Область: `deploy/`, `docs/dev/server.md`, `.env` сервера

## Контекст
Руководитель выделил VPS (Fornex, Ubuntu 24.04, 1 vCPU, 1 ГБ ОЗУ, 10 ГБ диска; там же VPN Amnezia в Docker на 443). Технически сервер считается продом, по ресурсам пока тестовый, до релиза не дойдёт. Нужны автозапуск бота, обновление кода без ручных шагов, бэкап базы, тот же лог `logs/bot.log`, что дома, чтобы `/tale-debug` и `make logs` работали без изменений. Ресурсов мало: Docker-сборка бота и второй Postgres в контейнере не помещаются.

## Варианты
| Вариант | За | Против |
|---|---|---|
| `make start` с pid-файлом, как дома | ничего нового | нет автозапуска после перезагрузки и перезапуска при падении |
| systemd-юнит от непривилегированного пользователя, бинарь из `make build` | автозапуск, перезапуск, ограничение прав, лог в тот же файл | системные файлы ставятся с sudo |
| Docker Compose (бот + Postgres) | воспроизводимость | лишние 300–500 МБ ОЗУ и ~1 ГБ диска на образы, Postgres уже стоит из apt |
| CI/CD из GitHub Actions по push | без входа на сервер | секреты и ssh-ключ в GitHub, лишнее для одного разработчика |

## Решение
- Сервер: пользователь `divanov` (sudo без пароля, вход только по ключу), Go в `~/.local/go`, PostgreSQL 16 из apt (роль `tale`, одна база `tale`; тестовой и имитаторской баз на проде нет), клон репозитория в `~/Go/a-tale-together` — пути совпадают с домашними.
- Сервис `tale-bot` (`deploy/tale-bot.service`): `User=divanov`, `EnvironmentFile=.env`, `Restart=on-failure`, stdout и stderr дописываются в `logs/bot.log`; ротация `deploy/tale-bot.logrotate` (неделя или 50 МБ, 8 копий, `copytruncate`).
- Деплой `deploy/deploy.sh`: `git fetch` + `git reset --hard origin/master` (локальных правок на сервере не бывает), `make build`, `make migrate`, установка системных файлов из `deploy/`, `systemctl restart`; при пустом `TELEGRAM_BOT_TOKEN` сервис не запускается. Push в GitHub делает руководитель, деплой запускает Claude по его команде через ssh.
- Бэкап: `deploy/tale-backup.sh` в `/etc/cron.daily`, `pg_dump -Fc` в `/var/backups/tale`, 14 дней.
- `.env` сервера: `APP_ENV=prod`, `LOG_LEVEL=info`, отдельный бот BotFather (один токен — один экземпляр, иначе `409 Conflict` с домашним), пароль роли `tale` сгенерирован и хранится вне git (проект «Дом», `secrets/fornex.env`).
- LLM: до релиза профиль `local-claude` (`claude-cli`, подписка Claude Code, вход `claude` под `divanov`); для прода с чужими игроками нужен `anthropic-api` (вопрос T2, пункт 08), он ещё заглушка.

## Последствия
- Новая папка `deploy/` с readme; описание сервера и порядка действий в `docs/dev/server.md`; ссылки в `docs/README.md`, `docs/dev/environment.md`, `docs/go_commands.md`, скилле `/tale-env`.
- `make start/stop/restart` на сервере не использовать: экземпляр из systemd и экземпляр с pid-файлом будут мешать друг другу.
- Перед релизом: тариф с 2 ГБ ОЗУ и 20 ГБ диска (после установки свободно 1.3 ГБ), реализация `anthropic-api`, ревизия `.env`.
