# deploy

Файлы прод-сервера (ADR-0040, ADR-0041, [docs/dev/server.md](../docs/dev/server.md)).

| Файл | Где используется | Назначение |
|---|---|---|
| `release.sh` | дома, по команде руководителя через `/tale-release` (`make release RELEASE_ARGS=...`) | проверка, что HEAD запушен; статический бинарник; заливка по ssh в `releases/`; остановка сервиса; миграции бинарником (`bot -migrate`); ссылка `bin/bot` на новую версию; установка файлов ниже; запуск и проверка лога; `rollback`, `status`, `clean`. Шаги описаны в шапке скрипта |
| `tale-bot.service` | `/etc/systemd/system/` | сервис бота от пользователя `divanov`, автозапуск, лог в `logs/bot.log`; `ExecStart` — ссылка `bin/bot` |
| `tale-bot.logrotate` | `/etc/logrotate.d/tale-bot` | ротация `logs/bot.log` (неделя или 50 МБ, 8 копий) |
| `tale-backup.sh` | `/etc/cron.daily/tale-backup` | ежедневный `pg_dump` базы `tale` в `/var/backups/tale`, 14 дней |

Дома эти файлы не используются: локальный бот запускается `make start`. На сервере нет Go, git и goose: только бинарники в `releases/`, `.env`, `logs/` и копии файлов этой папки.
