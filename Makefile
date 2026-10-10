# Команды разработки A Tale Together. Запуск из WSL.
GO ?= $(HOME)/.local/go/bin/go
GOOSE ?= $(HOME)/.local/bin/goose
BIN := bin/bot
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
-include .env
export

PID := bin/bot.pid
LOG := logs/bot.log

.PHONY: build run start stop restart logs test test-integration lint fmt migrate migrate-down migrate-status tools check eval-interpreter

build: ## Сборка бота
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/bot

run: build ## Запуск бота в терминале (Ctrl+C — остановить)
	./$(BIN)

start: build ## Запуск бота в фоне: pid в bin/bot.pid, лог в logs/bot.log
	@mkdir -p logs
	@if [ -f $(PID) ] && kill -0 $$(cat $(PID)) 2>/dev/null; then echo "бот уже запущен, pid $$(cat $(PID)); make restart"; exit 1; fi
	@pkill -CONT -x bot -u $$USER 2>/dev/null; pkill -x bot -u $$USER 2>/dev/null && echo "остановлен экземпляр без pid-файла (в том числе приостановленный Ctrl+Z)" || true
	@setsid nohup ./$(BIN) >> $(LOG) 2>&1 & echo $$! > $(PID); sleep 2; \
	 if kill -0 $$(cat $(PID)) 2>/dev/null; then echo "бот запущен, pid $$(cat $(PID))"; else echo "бот не запустился, см. $(LOG)"; tail -5 $(LOG); exit 1; fi

stop: ## Остановить фонового бота
	@if [ -f $(PID) ]; then kill $$(cat $(PID)) 2>/dev/null && echo "бот остановлен" || echo "процесс уже не запущен"; rm -f $(PID); else echo "pid-файла нет"; fi
	@pkill -CONT -x bot -u $$USER 2>/dev/null; pkill -x bot -u $$USER 2>/dev/null && echo "остановлен экземпляр без pid-файла (в том числе приостановленный Ctrl+Z)" || true

restart: stop start ## Перезапуск бота после правок кода

logs: ## Последние строки лога фонового бота
	@tail -n 50 $(LOG)

test: ## Юнит-тесты
	$(GO) test ./...

test-integration: ## Тесты с БД (DATABASE_URL_TEST)
	$(GO) test -tags integration ./...

eval-interpreter: ## Оценочный набор интерпретатора на живом claude-cli (EVAL_ARGS="-v -case name -model haiku"; по умолчанию sonnet)
	$(GO) run ./cmd/eval-interpreter $(EVAL_ARGS)

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
