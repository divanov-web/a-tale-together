#!/usr/bin/env bash
# Релиз бота на прод с домашней машины (ADR-0041, скилл /tale-release, docs/dev/server.md).
# Бинарник собирается дома (статически, linux/amd64), на сервер уходит по ssh; Go, git и goose на сервере не нужны.
# Запуск: deploy/release.sh <шаг> [шаг ...]. Обычный релиз без ручных правок базы: deploy/release.sh all.
# Релиз с ручными правками таблиц: check build upload stop migrate, затем SQL руками, затем switch start verify.
#
# Шаги:
#   check    дерево чистое, HEAD входит в origin/master (push сделан); версии дома и на сервере, список коммитов и миграций между ними
#   build    статический бинарник bin/release/bot-<версия>
#   upload   бинарник в releases/ на сервере, файлы deploy/ (юнит, logrotate, бэкап) рядом
#   stop     остановить сервис tale-bot
#   migrate  применить миграции новым бинарником (bot -migrate, читает .env сервера)
#   status   статус миграций на сервере новым бинарником
#   switch   bin/bot -> releases/bot-<версия>; прежняя цель запоминается в releases/.previous
#   start    установить файлы deploy/ (sudo), daemon-reload, запустить сервис
#   verify   сервис активен, в логе bot starting с новой версией и polling started, нет 409
#   all      check build upload stop migrate switch start verify
#   rollback stop, bin/bot -> releases/.previous, start, verify (миграции не откатываются)
#   clean    удалить старые бинарники на сервере, оставить 3 последних
#
# Переменные: RELEASE_HOST (ssh-алиас, по умолчанию fornex-dev), RELEASE_DIR (папка бота на сервере), GO (компилятор дома).
set -euo pipefail
cd "$(dirname "$0")/.."

HOST=${RELEASE_HOST:-fornex-dev}
DIR=${RELEASE_DIR:-/home/divanov/Projects/a-tale-together}
GO=${GO:-$HOME/.local/go/bin/go}
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
OUT=bin/release/bot-$VERSION
REMOTE_BIN=releases/bot-$VERSION

remote() { ssh "$HOST" "cd '$DIR' && $1"; }
fail() { echo "релиз: $*" >&2; exit 1; }

server_version() {
  # Версия на сервере: цель ссылки bin/bot, иначе последняя строка bot starting в логе.
  remote 'v=$(readlink bin/bot 2>/dev/null | sed "s|.*/bot-||"); [ -n "$v" ] && echo "$v" || grep "bot starting" logs/bot.log 2>/dev/null | tail -1 | grep -o "version=[^ ]*" | cut -d= -f2' || true
}

cmd_check() {
  [ -z "$(git status --porcelain)" ] || fail "рабочее дерево не чистое: закоммитить или убрать правки"
  git fetch -q origin
  git merge-base --is-ancestor HEAD origin/master || fail "HEAD $VERSION не входит в origin/master: нужен git push руководителя"
  local server; server=$(server_version)
  echo "дома: $VERSION; на сервере: ${server:-неизвестно}"
  if [ -n "$server" ] && git rev-parse -q --verify "${server}^{commit}" >/dev/null 2>&1; then
    [ "$server" = "$VERSION" ] && echo "на сервере уже эта версия"
    echo "коммиты к выкату:"; git log --oneline "$server..HEAD" | cat
    echo "миграции к выкату:"; git diff --name-only "$server..HEAD" -- migrations | grep -v embed.go | cat
  else
    echo "версия сервера не найдена в истории дома: список коммитов и миграций не показан"
  fi
}

cmd_build() {
  mkdir -p bin/release
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$GO" build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT" ./cmd/bot
  ls -la "$OUT"
}

cmd_upload() {
  [ -f "$OUT" ] || fail "нет $OUT: сначала build"
  remote 'mkdir -p releases deploy logs bin'
  scp -q "$OUT" "$HOST:$DIR/releases/"
  scp -q deploy/tale-bot.service deploy/tale-bot.logrotate deploy/tale-backup.sh "$HOST:$DIR/deploy/"
  remote "chmod 755 $REMOTE_BIN && $REMOTE_BIN -h >/dev/null 2>&1 || true; ls -la releases/"
}

cmd_stop() { remote 'sudo systemctl stop tale-bot && echo "tale-bot остановлен"'; }

cmd_migrate() { remote "test -x $REMOTE_BIN || { echo 'нет $REMOTE_BIN: сначала upload'; exit 1; }; $REMOTE_BIN -migrate"; }
cmd_status()  { remote "$REMOTE_BIN -migrate-status"; }

cmd_switch() {
  remote "test -x $REMOTE_BIN || { echo 'нет $REMOTE_BIN: сначала upload'; exit 1; }
    if [ -L bin/bot ]; then readlink bin/bot > releases/.previous
    elif [ -f bin/bot ]; then mv bin/bot releases/bot-old-\$(date +%Y%m%d-%H%M%S); ls -t releases/bot-old-* | head -1 | sed 's|^|../|' > releases/.previous
    fi
    ln -sfn ../$REMOTE_BIN bin/bot; echo \"bin/bot -> \$(readlink bin/bot); прежний: \$(cat releases/.previous 2>/dev/null || echo нет)\""
}

cmd_start() {
  remote 'sudo install -m 644 deploy/tale-bot.service /etc/systemd/system/tale-bot.service
    sudo install -m 644 deploy/tale-bot.logrotate /etc/logrotate.d/tale-bot
    sudo install -m 755 deploy/tale-backup.sh /etc/cron.daily/tale-backup
    sudo systemctl daemon-reload && sudo systemctl enable -q tale-bot && sudo systemctl start tale-bot && echo "tale-bot запущен"'
}

cmd_verify() {
  sleep 4
  local expect=${1:-$VERSION}
  remote "systemctl is-active tale-bot || { tail -n 20 logs/bot.log; exit 1; }
    tail -n 30 logs/bot.log | grep -E 'bot starting|polling started|409|ошибка' | tail -3
    tail -n 30 logs/bot.log | grep 'bot starting' | tail -1 | grep -q 'version=$expect' || { echo 'в логе нет bot starting с версией $expect'; exit 1; }
    tail -n 30 logs/bot.log | grep -q '409' && { echo '409 Conflict: второй экземпляр с тем же токеном'; exit 1; }
    free -m | awk 'NR==2{print \"память: занято \" \$3 \" МБ из \" \$2}'"
}

cmd_rollback() {
  local prev; prev=$(remote 'cat releases/.previous 2>/dev/null' || true)
  [ -n "$prev" ] || fail "releases/.previous пуст: откатывать нечего"
  cmd_stop
  remote "ln -sfn '$prev' bin/bot && echo \"bin/bot -> \$(readlink bin/bot)\""
  cmd_start
  cmd_verify "${prev##*/bot-}"
  echo "откат бинарника выполнен; миграции базы не откатывались"
}

cmd_clean() { remote 'ls -t releases/bot-* | tail -n +4 | xargs -r rm -v; ls -la releases/'; }

cmd_all() { cmd_check; cmd_build; cmd_upload; cmd_stop; cmd_migrate; cmd_switch; cmd_start; cmd_verify; }

[ $# -gt 0 ] || { sed -n '2,25p' "$0"; exit 1; }
for step in "$@"; do
  case "$step" in
    check|build|upload|stop|migrate|status|switch|start|verify|all|rollback|clean) echo "== $step"; "cmd_$step" ;;
    *) fail "неизвестный шаг: $step" ;;
  esac
done
