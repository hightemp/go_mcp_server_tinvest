SHELL := /bin/bash
APP_NAME := go_mcp_server_tinvest
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP_NAME)
HOST ?= localhost #0.0.0.0
PORT ?= 8100
GO ?= go

.PHONY: help deps build run run-sse test clean fmt vet env

help: ## Показать справку по целям Makefile
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk -F ':.*?## ' '{printf "\033[36m%-12s\033[0m %s\n", $$1, $$2}'

deps: ## Обновить зависимости (go mod tidy)
	$(GO) mod tidy

build: ## Сборка бинаря в ./bin
	@mkdir -p $(BIN_DIR)
	$(GO) build -v -o $(BIN) .

run: ## Запуск MCP сервера (stdio)
	$(GO) run . -t stdio

run-sse: ## Запуск MCP сервера (SSE) с параметрами HOST и PORT
	$(GO) run . -t sse -h $(HOST) -p $(PORT)

test: ## Запуск тестов
	$(GO) test ./...

fmt: ## Форматирование кода (go fmt)
	$(GO) fmt ./...

vet: ## Анализ кода (go vet)
	$(GO) vet ./...

env: ## Показать важные переменные окружения
	@echo "TINKOFF_TOKEN=$${TINKOFF_TOKEN:-<not set>}"
	@echo "TINKOFF_ENDPOINT=$${TINKOFF_ENDPOINT:-sandbox-invest-public-api.tinkoff.ru:443}"
	@echo "APP_NAME=$${APP_NAME:-go-mcp-tinvest}"

clean: ## Очистить артефакты сборки
	rm -rf $(BIN_DIR)