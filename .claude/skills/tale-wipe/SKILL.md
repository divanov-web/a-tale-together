---
name: tale-wipe
description: Вайп песочницы A Tale Together — удалить все игры, персонажей, раунды, заявки, события, outbox, вызовы LLM и логи, оставив пользователей, привязку чата, настройки и профили LLM, чтобы создать мир заново. Подключать при словах «вайп», «wipe», «сбрось песочницу», «удали миры», «начать заново», «очисти базу и логи».
argument-hint: "[dry-run]"
---

# Вайп песочницы

Одна команда: `make wipe`. Делает `make stop`, выполняет `scripts/wipe-sandbox.sql` одной транзакцией на `DATABASE_URL`, очищает `logs/chats/*`, `logs/images/*`, обнуляет `logs/bot.log`, делает `make start`. Работает только при `APP_ENV=dev`. База `tale_test` не трогается.

## Что удаляется и что остаётся

| Удаляется | Остаётся |
|---|---|
| `games`, `seasons`, `factions`, `locations`, `game_members`, `characters`, `character_stats`, `parties`, `rounds`, `action_submissions`, `resolved_actions`, `world_events`, `messages_outbox`, `llm_runs`, `dialogs`, `telegram_updates`; счётчики `id` этих таблиц сброшены на 1 | `users` (с `current_game_id = NULL`), `chats`, `chat_moderators`, `app_settings`, `llm_profiles`, `genres`, `character_models`, миграции |
| `logs/chats/<chat_id>/`, `logs/images/`, содержимое `logs/bot.log` | привязка бота к группе-песочнице, `.env` |

Справочники остаются намеренно: бота не нужно заново добавлять в группу, профиль LLM и настройки не нужно настраивать через `/llm` и `/set`.

## Порядок

1. Вайп — по явной просьбе руководителя; без неё не запускать. Если в песочнице идёт игра, которую руководитель не называл, уточнить.
2. `make wipe`; при аргументе `dry-run` — только показать, что будет удалено: `psql "$DATABASE_URL" -Atc "select count(*) from games"` и список папок `logs/chats`.
3. Проверка: `make logs` — `bot starting` с текущей версией, `telegram polling started`, без `409 Conflict`; `psql ... -c "select count(*) from games"` = 0.
4. Отчёт: что удалено (счётчики `DELETE` из вывода), что оставлено, версия бота в логе.

## Сопровождение

- Новая таблица с колонкой `game_id` → добавить `DELETE` в `scripts/wipe-sandbox.sql` в порядке зависимостей (дочерние раньше родительских); проверка в конце файла падает, если таблицу забыли. Таблица с циклической ссылкой (как `parties` ↔ `characters`) → сначала `UPDATE ... SET ... = NULL`.
- `TRUNCATE ... CASCADE` не использовать: `users.current_game_id` ссылается на `games`, каскад снёс бы пользователей.
- Если бот не стартовал после вайпа — диагностика по `/tale-env`.
