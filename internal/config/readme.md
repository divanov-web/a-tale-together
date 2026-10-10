# config
- Назначение: конфигурация из дефолтов, `.env` и переменных окружения; валидация обязательных значений.
- Публичный вход: `Load()`, `Config.Validate()`, `Config.SlogLevel()`. Журнал чатов: `CHAT_LOG_DIR` (пусто — выключен), `CHAT_LOG_RETENTION_DAYS`.
- Зависимости: `caarlos0/env`, `godotenv`.
- Связи: [среда](../../docs/dev/environment.md), `.env.example`.
