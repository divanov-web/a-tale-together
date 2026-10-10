# Команды разработки A Tale Together. Запуск из WSL.
GO ?= $(HOME)/.local/go/bin/go
GOOSE ?= $(HOME)/go/bin/goose
BIN := bin/bot
-include .env
export

.PHONY: build run test test-integration lint fmt migrate migrate-down migrate-status tools

build: ## Сборка бота
	$(GO) build -o $(BIN) ./cmd/bot

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
