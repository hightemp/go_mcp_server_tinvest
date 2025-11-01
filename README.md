# Go MCP сервер для Tinkoff Invest API

Лёгкий MCP‑сервер на Go для работы с Тинькофф Инвестициями (поиск инструментов, заявки, портфель).

## Make команды
- make deps — синхронизировать зависимости (go mod tidy)
- make build — собрать бинарник в ./bin
- make run — запустить MCP сервер в stdio
- make run-sse HOST=localhost PORT=8100 — запустить SSE сервер (http://HOST:PORT/sse)
- make env — показать важные переменные окружения
- make clean — удалить ./bin

## .env (обязательные переменные)
Пример (выберите ЭТОТ или sandbox эндпоинт — не оба сразу):

```env
TINKOFF_TOKEN=ваш_api_token
# Боевой эндпоинт:
TINKOFF_ENDPOINT=invest-public-api.tinkoff.ru:443
# Sandbox эндпоинт (если токен песочницы):
# TINKOFF_ENDPOINT=sandbox-invest-public-api.tinkoff.ru:443
# Рекомендуется в проде: ID открытого брокерского счёта
TINKOFF_ACCOUNT_ID=ваш_account_id
APP_NAME=go-mcp-tinvest
```

Критично: токен и эндпоинт должны соответствовать среде (иначе Unauthenticated 40003). Для портфеля укажите корректный `TINKOFF_ACCOUNT_ID` (иначе NotFound 50004).

## Запуск
- stdio: `make run`
- SSE: `make run-sse HOST=localhost PORT=8100` и подключение к http://HOST:PORT/sse

Коротко об инструментах MCP: search_stocks, search_bonds, search_funds, buy, sell, portfolio.