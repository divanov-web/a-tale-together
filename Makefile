# Команды разработки A Tale Together. Запуск из WSL.
GO ?= $(HOME)/.local/go/bin/go
GOOSE ?= $(HOME)/.local/bin/goose
BIN := bin/bot
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
-include .env
export

.PHONY: build run test test-integration lint fmt migrate migrate-down migrate-status tools check

build: ## Сборка бота
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/bot

run: build ## Запуск бота
	./$(BIN)

test: ## Юнит-тесты
	$(GO) test ./...

test-integration: ## Тесты с БД (DATABASE_URL_TEST)
	$(GO) test -tags integration ./...

lint: ## gofmt, go vet, golangci-lint при наличии
	@test -z "$$($(GO) fmt ./... | tee /dev/stderr)" || (echo "gofmt изменил файлы"; exit 1)
	$(GO) vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint не установлен, пропуск"

fmt: ## Форматирование
	$(GO) fmt ./...

tools: ## Установка goose CLI
	$(GO) install github.com/pressly/goose/v3/cmd/goose@latest

migrate: ## Применить миграции
	$(GOOSE) -dir migrations postgres "$(DATABASE_URL)" up

migrate-down: ## Откатить последнюю миграцию
	$(GOOSE) -dir migrations postgres "$(DATABASE_URL)" down

migrate-status: ## Статус миграций
	$(GOOSE) -dir migrations postgres "$(DATABASE_URL)" status

check: ## Проверка окружения: Go, Postgres, .env, токен бота
	@$(GO) version
	@test -f .env || (echo "нет .env: cp .env.example .env"; exit 1)
	@test -n "$(TELEGRAM_BOT_TOKEN)" || (echo "TELEGRAM_BOT_TOKEN пуст в .env"; exit 1)
	@psql "$(DATABASE_URL)" -Atqc 'select version()' >/dev/null && echo "postgres: ok ($(DATABASE_URL))" || (echo "postgres недоступен: $(DATABASE_URL)"; exit 1)
	@curl -sf "https://api.telegram.org/bot$(TELEGRAM_BOT_TOKEN)/getMe" | grep -o '"username":"[^"]*"' || (echo "telegram: токен не принят"; exit 1)
	@test -x "$(GOOSE)" && echo "goose: $(GOOSE)" || echo "goose не установлен: make tools"
