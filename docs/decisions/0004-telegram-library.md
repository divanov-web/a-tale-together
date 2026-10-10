# ADR-0004: Библиотека Telegram

- Статус: принято
- Дата: 2026-10-10
- Область: `internal/adapter/telegram`, `internal/app/bot`

## Варианты
| Вариант | За | Против |
|---|---|---|
| `github.com/go-telegram/bot` | контекст везде, middleware, long polling и webhook, активно развивается | |
| `github.com/go-telegram-bot-api/telegram-bot-api/v5` | популярна | без контекста, развитие замедлилось |
| `gopkg.in/telebot.v3` | удобный роутер | магия обработчиков, сложнее тестировать |

## Решение
`github.com/go-telegram/bot` v1.27+. Long polling в разработке; webhook позже отдельным ADR при выходе за пределы локального стенда.

## Последствия
Правило `.claude/rules/telegram.md`; таблица `telegram_updates` для дедупликации; outbox для исходящих.
