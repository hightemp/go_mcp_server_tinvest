package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/tinkoff/invest-api-go-sdk/investgo"
)

func main() {
	// 1. Читаем токен из окружения
	token := os.Getenv("TINKOFF_TOKEN")
	if token == "" {
		log.Fatal("Необходимо установить API-токен Тинькофф в переменную окружения TINKOFF_TOKEN")
	}

	ctx := context.Background()

	// 2. Инициализируем клиент API Тинькофф (песочница)
	client := investgo.NewSandboxRestClient(token)
	// Открываем аккаунт в песочнице, если еще нет
	account, err := client.Register(ctx, investgo.AccountType("Tinkoff"))
	if err != nil {
		log.Fatalf("Ошибка регистрации аккаунта в песочнице: %v", err)
	}
	accountID := account.ID
	log.Printf("Используется аккаунт %s (тип: %s)\n", accountID, account.Type)

	// 3. Создаем MCP-сервер
	mcpServer := server.NewMCPServer(
		"Tinkoff Investments MCP",
		"1.0.0",
		server.WithToolCapabilities(true), // включаем возможность вызывать Tools
		server.WithRecovery(),             // перехватывать паники в хендлерах
	)

	// 4. Определяем инструменты MCP и их обработчики:

	// Поиск акций
	searchStocksTool := mcp.NewTool("search_stocks",
		mcp.WithDescription("Поиск акций по тикеру или названию"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Часть тикера или названия акции")),
	)
	mcpServer.AddTool(searchStocksTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.RequireString("query")
		insts, err := client.InstrumentByTicker(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка поиска акций: %v", err)), nil
		}
		var results []string
		for _, inst := range insts {
			if string(inst.Type) == "Stock" { // фильтруем только акции
				results = append(results, fmt.Sprintf("%s (%s) – FIGI: %s", inst.Name, inst.Ticker, inst.FIGI))
			}
		}
		if len(results) == 0 {
			return mcp.NewToolResultText("Акции по запросу не найдены"), nil
		}
		return mcp.NewToolResultText("Найдено акций:\n" + formatList(results)), nil
	})

	// Поиск облигаций
	searchBondsTool := mcp.NewTool("search_bonds",
		mcp.WithDescription("Поиск облигаций по тикеру или названию"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Часть тикера или названия облигации")),
	)
	mcpServer.AddTool(searchBondsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.RequireString("query")
		insts, err := client.InstrumentByTicker(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка поиска облигаций: %v", err)), nil
		}
		var results []string
		for _, inst := range insts {
			if string(inst.Type) == "Bond" {
				results = append(results, fmt.Sprintf("%s (%s) – FIGI: %s", inst.Name, inst.Ticker, inst.FIGI))
			}
		}
		if len(results) == 0 {
			return mcp.NewToolResultText("Облигации по запросу не найдены"), nil
		}
		return mcp.NewToolResultText("Найдено облигаций:\n" + formatList(results)), nil
	})

	// Поиск фондов (ETF)
	searchFundsTool := mcp.NewTool("search_funds",
		mcp.WithDescription("Поиск фондов/ETF по тикеру или названию"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Часть тикера или названия фонда (ETF)")),
	)
	mcpServer.AddTool(searchFundsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.RequireString("query")
		insts, err := client.InstrumentByTicker(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка поиска фондов: %v", err)), nil
		}
		var results []string
		for _, inst := range insts {
			if string(inst.Type) == "Etf" {
				results = append(results, fmt.Sprintf("%s (%s) – FIGI: %s", inst.Name, inst.Ticker, inst.FIGI))
			}
		}
		if len(results) == 0 {
			return mcp.NewToolResultText("Фонды по запросу не найдены"), nil
		}
		return mcp.NewToolResultText("Найдено фондов:\n" + formatList(results)), nil
	})

	// Покупка инструмента
	buyTool := mcp.NewTool("buy",
		mcp.WithDescription("Купить инструмент (рыночная заявка)"),
		mcp.WithString("ticker", mcp.Required(), mcp.Description("Тикер инструмента для покупки")),
		mcp.WithNumber("lots", mcp.Required(), mcp.Description("Количество лотов для покупки")),
	)
	mcpServer.AddTool(buyTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ticker, _ := req.RequireString("ticker")
		lots, _ := req.RequireFloat("lots")
		insts, err := client.InstrumentByTicker(ctx, ticker)
		if err != nil || len(insts) == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("Инструмент %s не найден", ticker)), nil
		}
		inst := insts[0] // берем первый найденный инструмент
		// Исполняем рыночную заявку на покупку
		_, err = client.MarketOrder(ctx, accountID, inst.FIGI, int(lots), investgo.BUY)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка покупки %s: %v", ticker, err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Заявка на покупку %d лотов %s (%s) успешно отправлена", int(lots), inst.Name, inst.Ticker)), nil
	})

	// Продажа инструмента
	sellTool := mcp.NewTool("sell",
		mcp.WithDescription("Продать инструмент (рыночная заявка)"),
		mcp.WithString("ticker", mcp.Required(), mcp.Description("Тикер инструмента для продажи")),
		mcp.WithNumber("lots", mcp.Required(), mcp.Description("Количество лотов для продажи")),
	)
	mcpServer.AddTool(sellTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ticker, _ := req.RequireString("ticker")
		lots, _ := req.RequireFloat("lots")
		insts, err := client.InstrumentByTicker(ctx, ticker)
		if err != nil || len(insts) == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("Инструмент %s не найден", ticker)), nil
		}
		inst := insts[0]
		_, err = client.MarketOrder(ctx, accountID, inst.FIGI, int(lots), investgo.SELL)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка продажи %s: %v", ticker, err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Заявка на продажу %d лотов %s (%s) успешно отправлена", int(lots), inst.Name, inst.Ticker)), nil
	})

	// Просмотр портфеля
	portfolioTool := mcp.NewTool("portfolio",
		mcp.WithDescription("Просмотр текущего портфеля (активы и остатки)"),
	)
	mcpServer.AddTool(portfolioTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		pf, err := client.Portfolio(ctx, accountID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка получения портфеля: %v", err)), nil
		}
		var lines []string
		// Список ценных бумаг
		for _, pos := range pf.Positions {
			line := fmt.Sprintf("%s (%s): %.2f шт (лотов: %d)", pos.Name, pos.Ticker, pos.Balance, pos.Lots)
			lines = append(lines, line)
		}
		// Остатки валют
		for _, cur := range pf.Currencies {
			line := fmt.Sprintf("Валюта %s: %.2f (свободные средства)", cur.Currency, cur.Balance)
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			return mcp.NewToolResultText("Портфель пуст"), nil
		}
		return mcp.NewToolResultText("Текущий портфель:\n" + formatList(lines)), nil
	})

	// 5. Запуск MCP-сервера: режим CLI или HTTP (SSE)
	if len(os.Args) > 1 && os.Args[1] == "--http" {
		// HTTP-сервер с использованием SSE
		sseServer := server.NewSSEServer(mcpServer)
		http.Handle("/mcp/", sseServer) // обрабатываем все пути /mcp/...
		log.Printf("MCP-сервер запущен в режиме HTTP на порту 8080\n")
		log.Fatal(http.ListenAndServe(":8080", nil))
	} else {
		// CLI (stdio) сервер
		log.Printf("MCP-сервер запущен в режиме CLI (stdio)\n")
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Ошибка MCP сервера: %v", err)
		}
	}
}

// Вспомогательная функция для форматирования списка строк (каждый элемент с новой строки, с отступом)
func formatList(items []string) string {
	result := ""
	for _, item := range items {
		result += " - " + item + "\n"
	}
	return result
}
