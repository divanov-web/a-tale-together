# Команды разработки A Tale Together. Запуск из WSL.
GO ?= $(HOME)/.local/go/bin/go
GOOSE ?= $(HOME)/.local/bin/goose
BIN := bin/bot
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
-include .env
DATABASE_URL_SIM ?= postgres://tale:tale@localhost:5432/tale_sim?sslmode=disable
export

PID := bin/bot.pid
LOG := logs/bot.log

.PHONY: build run start stop restart logs test test-integration lint fmt migrate migrate-down migrate-status tools check eval-interpreter eval-narrator wipe sim migrate-sim release

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

wipe: ## Вайп песочницы (только APP_ENV=dev): stop, scripts/wipe-sandbox.sql, очистка logs/, start
	@test "$(APP_ENV)" = "dev" || (echo "make wipe только при APP_ENV=dev (сейчас: $(APP_ENV))"; exit 1)
	@$(MAKE) --no-print-directory stop
	psql "$(DATABASE_URL)" -v ON_ERROR_STOP=1 -q -f scripts/wipe-sandbox.sql
	@rm -rf logs/chats/* logs/images/*; mkdir -p logs; : > $(LOG); echo "игры и логи удалены"
	@$(MAKE) --no-print-directory start

test: ## Юнит-тесты
	$(GO) test ./...

test-integration: ## Тесты с БД (DATABASE_URL_TEST)
	$(GO) test -tags integration -p 1 ./...

eval-interpreter: ## Оценочный набор интерпретатора на живом claude-cli (EVAL_ARGS="-v -case name -model haiku"; по умолчанию sonnet)
	$(GO) run ./cmd/eval-interpreter $(EVAL_ARGS)

eval-narrator: ## Оценочный набор рассказчика на живом claude-cli (EVAL_ARGS="-v -case name -model sonnet"; по умолчанию opus)
	$(GO) run ./cmd/eval-narrator $(EVAL_ARGS)

sim: ## Прогон партии ИИ-игроками на БД DATABASE_URL_SIM (SIM_ARGS="-run sim/runs/basic.json -rounds 3 -profile sim-sonnet"); отчёт в logs/sim/<метка>/
	$(GO) run ./cmd/simulate $(SIM_ARGS)

migrate-sim: ## Применить миграции к БД прогонов имитатора
	$(GOOSE) -dir migrations postgres "$(DATABASE_URL_SIM)" up

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

release: ## Релиз на прод с домашней машины (RELEASE_ARGS="all" | "check" | "check build upload stop migrate" ...; /tale-release, только по команде руководителя)
	deploy/release.sh $(RELEASE_ARGS)

check: ## Проверка окружения: Go, Postgres, .env, токен бота
	@$(GO) version
	@test -f .env || (echo "нет .env: cp .env.example .env"; exit 1)
	@test -n "$(TELEGRAM_BOT_TOKEN)" || (echo "TELEGRAM_BOT_TOKEN пуст в .env"; exit 1)
	@psql "$(DATABASE_URL)" -Atqc 'select version()' >/dev/null && echo "postgres: ok ($(DATABASE_URL))" || (echo "postgres недоступен: $(DATABASE_URL)"; exit 1)
	@curl -sf "https://api.telegram.org/bot$(TELEGRAM_BOT_TOKEN)/getMe" | grep -o '"username":"[^"]*"' || (echo "telegram: токен не принят"; exit 1)
	@test -x "$(GOOSE)" && echo "goose: $(GOOSE)" || echo "goose не установлен: make tools"
