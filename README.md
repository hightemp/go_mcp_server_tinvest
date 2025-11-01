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

## MCP инструменты

Ниже перечислены доступные инструменты MCP, их параметры и примеры аргументов вызова (JSON).

- search_stocks — поиск акций
  - params: query (string) — часть тикера или названия
  - пример: {"query":"SBER"}

- search_bonds — поиск облигаций
  - params: query (string)
  - пример: {"query":"OFZ"}

- search_funds — поиск фондов/ETF
  - params: query (string)
  - пример: {"query":"FXUS"}

- buy — покупка (рыночная заявка)
  - params:
    - ticker (string) — тикер или часть названия для поиска
    - lots (number) — количество лотов
  - пример: {"ticker":"SBER","lots":1}
  - примечание: отправляется рыночная заявка в счёт, выбранный сервером (см. переменные окружения)

- sell — продажа (рыночная заявка)
  - params:
    - ticker (string)
    - lots (number)
  - пример: {"ticker":"SBER","lots":1}

- portfolio — текущее состояние портфеля
  - params: нет
  - пример: {}

- last_price — последняя цена инструмента
  - params: query (string) — тикер/название/FIGI
  - пример: {"query":"SBER"}
  - результат: последняя цена и время котировки

- orderbook — стакан заявок по инструменту
  - params:
    - query (string) — тикер/название/FIGI
    - depth (number) — глубина стакана (1–50)
  - пример: {"query":"GAZP","depth":10}
  - результат: верхние уровни bid/ask с ценой и количеством

- candles — исторические свечи за период
  - params:
    - query (string) — тикер/название/FIGI
    - from (string, RFC3339) — начало периода, напр. "2024-10-01T00:00:00Z"
    - to (string, RFC3339) — конец периода, напр. "2024-10-02T00:00:00Z"
    - interval (string) — один из: "1m","5m","15m","1h","1d"
  - пример: {"query":"YNDX","from":"2024-10-01T00:00:00Z","to":"2024-10-02T00:00:00Z","interval":"1h"}
  - примечание: вывод ограничен первыми 50 свечами для компактности

- trading_status — статус торгов по инструменту
  - params: query (string) — тикер/название/FIGI
  - пример: {"query":"TCSG"}
  - результат: enum статуса торгов по инструменту

![](https://asdertasd.site/counter/go_mcp_server_tinvest)