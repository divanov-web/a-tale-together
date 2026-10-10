// Package config загружает конфигурацию приложения: дефолты, затем .env и переменные окружения.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config — настройки приложения. Секреты дефолтов не имеют и проверяются в Validate.
type Config struct {
	TelegramBotToken string        `env:"TELEGRAM_BOT_TOKEN"` // Токен бота Telegram
	DatabaseURL      string        `env:"DATABASE_URL"`       // DSN PostgreSQL
	DatabaseURLTest  string        `env:"DATABASE_URL_TEST"`  // DSN тестовой БД для интеграционных тестов
	AppEnv           string        `env:"APP_ENV"`            // dev|prod: в prod отключены dev-команды
	LogLevel         string        `env:"LOG_LEVEL"`          // debug|info|warn|error
	MigrateOnStart   bool          `env:"MIGRATE_ON_START"`   // Применять миграции при старте
	SchedulerTick    time.Duration `env:"SCHEDULER_TICK"`     // Период планировщика раундов
	OutboxTick       time.Duration `env:"OUTBOX_TICK"`        // Период воркера доставки outbox
	BotOwnerID       int64         `env:"BOT_OWNER_ID"`       // Telegram ID владельца бота: команды /llm
	LLM              LLM
}

// LLM — секреты и пути провайдеров языковой модели; профили (провайдер, модели) хранятся в БД (ADR-0003).
type LLM struct {
	AnthropicAPIKey  string `env:"ANTHROPIC_API_KEY"`  // Ключ Anthropic для провайдера anthropic-api
	OpenAIAPIKey     string `env:"OPENAI_API_KEY"`     // Ключ OpenAI для провайдера openai-api
	ClaudeCLIPath    string `env:"CLAUDE_CLI_PATH"`    // Путь к бинарю claude для провайдера claude-cli
	ClaudeCLIWorkdir string `env:"CLAUDE_CLI_WORKDIR"` // Пустая рабочая папка для запусков claude -p
}

// Defaults возвращает конфигурацию по умолчанию; приложение стартует без .env.
func Defaults() Config {
	return Config{
		DatabaseURL:     "postgres://tale:tale@localhost:5432/tale?sslmode=disable",
		DatabaseURLTest: "postgres://tale:tale@localhost:5432/tale_test?sslmode=disable",
		AppEnv:          "dev",
		LogLevel:        "info",
		SchedulerTick:   10 * time.Second,
		OutboxTick:      3 * time.Second,
		LLM:             LLM{ClaudeCLIPath: "claude"},
	}
}

// Load читает .env (если есть) и переменные окружения поверх дефолтов.
func Load() (Config, error) {
	_ = godotenv.Load()
	cfg := Defaults()
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("config.Load: %w", err)
	}
	return cfg, nil
}

// Validate проверяет обязательные для запуска бота значения.
func (c Config) Validate() error {
	var errs []error
	if strings.TrimSpace(c.TelegramBotToken) == "" {
		errs = append(errs, errors.New("TELEGRAM_BOT_TOKEN не задан"))
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		errs = append(errs, errors.New("DATABASE_URL не задан"))
	}
	if _, err := c.SlogLevel(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// SlogLevel преобразует LogLevel в уровень slog.
func (c Config) SlogLevel() (slog.Level, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(c.LogLevel)); err != nil {
		return 0, fmt.Errorf("LOG_LEVEL %q: %w", c.LogLevel, err)
	}
	return lvl, nil
}
