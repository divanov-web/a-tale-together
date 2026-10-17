#!/usr/bin/env bash
# Ежедневный дамп базы tale на прод-сервере: /var/backups/tale/tale-<дата>.dump (pg_dump -Fc), хранится 14 дней.
# Ставится deploy/deploy.sh в /etc/cron.daily/tale-backup, выполняется от root.
# Восстановление: sudo -u postgres pg_restore -d tale --clean --if-exists /var/backups/tale/tale-<дата>.dump
set -euo pipefail
DIR=/var/backups/tale
install -d -m 700 "$DIR"
sudo -u postgres pg_dump -Fc tale > "$DIR/tale-$(date +%F).dump.tmp"
mv "$DIR/tale-$(date +%F).dump.tmp" "$DIR/tale-$(date +%F).dump"
chmod 600 "$DIR"/*.dump
find "$DIR" -name 'tale-*.dump' -mtime +14 -delete
