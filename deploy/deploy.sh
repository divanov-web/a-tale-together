#!/usr/bin/env bash
# Деплой на прод-сервере (docs/dev/server.md): код из origin/master, сборка, миграции, системные файлы, перезапуск.
# Запуск под пользователем divanov: ~/Go/a-tale-together/deploy/deploy.sh. Локальных правок на сервере не бывает,
# поэтому рабочая копия приводится к origin/master без слияния (git reset --hard).
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$HOME/.local/go/bin:$HOME/.local/bin:$PATH"

old=$(git rev-parse --short HEAD)
git fetch -q origin
git reset -q --hard origin/master
new=$(git rev-parse --short HEAD)
echo "код: $old -> $new"

make build
make migrate 2>&1 | tail -1

sudo install -m 644 deploy/tale-bot.service /etc/systemd/system/tale-bot.service
sudo install -m 644 deploy/tale-bot.logrotate /etc/logrotate.d/tale-bot
sudo install -m 755 deploy/tale-backup.sh /etc/cron.daily/tale-backup
sudo systemctl daemon-reload
sudo systemctl enable -q tale-bot

if [ -z "$(grep '^TELEGRAM_BOT_TOKEN=' .env | cut -d= -f2-)" ]; then
  echo "TELEGRAM_BOT_TOKEN пуст в .env: сервис не запускаю"; exit 0
fi
sudo systemctl restart tale-bot
sleep 3
systemctl is-active tale-bot
tail -n 5 logs/bot.log
