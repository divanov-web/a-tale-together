# deploy

Файлы прод-сервера (ADR-0040, [docs/dev/server.md](../docs/dev/server.md)).

| Файл | Куда ставится | Назначение |
|---|---|---|
| `deploy.sh` | запускается из клона на сервере | обновить код до `origin/master`, собрать, миграции, установить файлы ниже, перезапустить сервис |
| `tale-bot.service` | `/etc/systemd/system/` | сервис бота от пользователя `divanov`, автозапуск, лог в `logs/bot.log` |
| `tale-bot.logrotate` | `/etc/logrotate.d/tale-bot` | ротация `logs/bot.log` (неделя или 50 МБ, 8 копий) |
| `tale-backup.sh` | `/etc/cron.daily/tale-backup` | ежедневный `pg_dump` базы `tale` в `/var/backups/tale`, 14 дней |

Дома эти файлы не используются: локальный бот запускается `make start`.
