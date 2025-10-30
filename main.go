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
	pb "github.com/tinkoff/invest-api-go-sdk/proto"
)

// stdLogger — простой логгер, удовлетворяющий investgo.Logger
type stdLogger struct{}

func (l stdLogger) Infof(format string, args ...any)  { log.Printf("[INFO] "+format, args...) }
func (l stdLogger) Errorf(format string, args ...any) { log.Printf("[ERROR] "+format, args...) }
func (l stdLogger) Fatalf(format string, args ...any) { log.Fatalf("[FATAL] "+format, args...) }

func main() {
	// 1) Токен из окружения
	token := os.Getenv("TINKOFF_TOKEN")
	if token == "" {
		log.Fatal("Необходимо установить API-токен Тинькофф в переменную окружения TINKOFF_TOKEN")
	}

	ctx := context.Background()

	// 2) Конфиг SDK и клиент (песочница по умолчанию)
	conf := investgo.Config{
		Token:    token,
		EndPoint: "sandbox-invest-public-api.tinkoff.ru:443",
		AppName:  "go-mcp-tinvest",
		// AccountId можно не задавать — SDK сам откроет/выберет sandbox-счёт
	}
	client, err := investgo.NewClient(ctx, conf, stdLogger{})
	if err != nil {
		log.Fatalf("Ошибка инициализации клиента InvestAPI: %v", err)
	}
	defer func() { _ = client.Stop() }()

	accountID := client.Config.AccountId
	log.Printf("Используется sandbox аккаунт: %s\n", accountID)

	// Сервисы SDK
	instruments := client.NewInstrumentsServiceClient()
	orders := client.NewOrdersServiceClient()
	ops := client.NewOperationsServiceClient()

	// 3) MCP-сервер
	mcpServer := server.NewMCPServer(
		"Tinkoff Investments MCP",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)

	// 4) Инструменты MCP

	// Поиск акций
	searchStocksTool := mcp.NewTool("search_stocks",
		mcp.WithDescription("Поиск акций по тикеру или названию"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Часть тикера или названия акции")),
	)
	mcpServer.AddTool(searchStocksTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.RequireString("query")
		resp, err := instruments.FindInstrument(query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка поиска акций: %v", err)), nil
		}
		var out []string
		for _, it := range resp.GetInstruments() {
			if it.GetInstrumentKind() == pb.InstrumentType_INSTRUMENT_TYPE_SHARE {
				out = append(out, fmt.Sprintf("%s (%s) – FIGI: %s", it.GetName(), it.GetTicker(), it.GetFigi()))
			}
		}
		if len(out) == 0 {
			return mcp.NewToolResultText("Акции по запросу не найдены"), nil
		}
		return mcp.NewToolResultText("Найдено акций:\n" + formatList(out)), nil
	})

	// Поиск облигаций
	searchBondsTool := mcp.NewTool("search_bonds",
		mcp.WithDescription("Поиск облигаций по тикеру или названию"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Часть тикера или названия облигации")),
	)
	mcpServer.AddTool(searchBondsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.RequireString("query")
		resp, err := instruments.FindInstrument(query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка поиска облигаций: %v", err)), nil
		}
		var out []string
		for _, it := range resp.GetInstruments() {
			if it.GetInstrumentKind() == pb.InstrumentType_INSTRUMENT_TYPE_BOND {
				out = append(out, fmt.Sprintf("%s (%s) – FIGI: %s", it.GetName(), it.GetTicker(), it.GetFigi()))
			}
		}
		if len(out) == 0 {
			return mcp.NewToolResultText("Облигации по запросу не найдены"), nil
		}
		return mcp.NewToolResultText("Найдено облигаций:\n" + formatList(out)), nil
	})

	// Поиск фондов (ETF)
	searchFundsTool := mcp.NewTool("search_funds",
		mcp.WithDescription("Поиск фондов/ETF по тикеру или названию"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Часть тикера или названия фонда (ETF)")),
	)
	mcpServer.AddTool(searchFundsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.RequireString("query")
		resp, err := instruments.FindInstrument(query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка поиска фондов: %v", err)), nil
		}
		var out []string
		for _, it := range resp.GetInstruments() {
			if it.GetInstrumentKind() == pb.InstrumentType_INSTRUMENT_TYPE_ETF {
				out = append(out, fmt.Sprintf("%s (%s) – FIGI: %s", it.GetName(), it.GetTicker(), it.GetFigi()))
			}
		}
		if len(out) == 0 {
			return mcp.NewToolResultText("Фонды по запросу не найдены"), nil
		}
		return mcp.NewToolResultText("Найдено фондов:\n" + formatList(out)), nil
	})

	// Покупка инструмента (рыночная)
	buyTool := mcp.NewTool("buy",
		mcp.WithDescription("Купить инструмент (рыночная заявка)"),
		mcp.WithString("ticker", mcp.Required(), mcp.Description("Тикер или часть названия для поиска инструмента")),
		mcp.WithNumber("lots", mcp.Required(), mcp.Description("Количество лотов")),
	)
	mcpServer.AddTool(buyTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q, _ := req.RequireString("ticker")
		lotsF, _ := req.RequireFloat("lots")
		lots := int64(lotsF)

		found, err := instruments.FindInstrument(q)
		if err != nil || len(found.GetInstruments()) == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("Инструмент %q не найден", q)), nil
		}
		inst := found.GetInstruments()[0]

		_, err = orders.Buy(&investgo.PostOrderRequestShort{
			InstrumentId: inst.GetFigi(),
			Quantity:     lots,
			Price:        nil, // market
			AccountId:    accountID,
			OrderType:    pb.OrderType_ORDER_TYPE_MARKET,
			OrderId:      investgo.CreateUid(),
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка покупки %s: %v", inst.GetTicker(), err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Отправлена заявка на покупку %d лотов %s (%s)", lots, inst.GetName(), inst.GetTicker())), nil
	})

	// Продажа инструмента (рыночная)
	sellTool := mcp.NewTool("sell",
		mcp.WithDescription("Продать инструмент (рыночная заявка)"),
		mcp.WithString("ticker", mcp.Required(), mcp.Description("Тикер или часть названия для поиска инструмента")),
		mcp.WithNumber("lots", mcp.Required(), mcp.Description("Количество лотов")),
	)
	mcpServer.AddTool(sellTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q, _ := req.RequireString("ticker")
		lotsF, _ := req.RequireFloat("lots")
		lots := int64(lotsF)

		found, err := instruments.FindInstrument(q)
		if err != nil || len(found.GetInstruments()) == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("Инструмент %q не найден", q)), nil
		}
		inst := found.GetInstruments()[0]

		_, err = orders.Sell(&investgo.PostOrderRequestShort{
			InstrumentId: inst.GetFigi(),
			Quantity:     lots,
			Price:        nil, // market
			AccountId:    accountID,
			OrderType:    pb.OrderType_ORDER_TYPE_MARKET,
			OrderId:      investgo.CreateUid(),
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка продажи %s: %v", inst.GetTicker(), err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Отправлена заявка на продажу %d лотов %s (%s)", lots, inst.GetName(), inst.GetTicker())), nil
	})

	// Портфель
	portfolioTool := mcp.NewTool("portfolio",
		mcp.WithDescription("Просмотр текущего портфеля (позиции и остатки)"),
	)
	mcpServer.AddTool(portfolioTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		pf, err := ops.GetPortfolio(accountID, pb.PortfolioRequest_CurrencyRequest(0))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ошибка получения портфеля: %v", err)), nil
		}
		var lines []string
		for _, pos := range pf.GetPositions() {
			qty := pos.GetQuantity()
			lots := pos.GetQuantityLots()
			lines = append(lines, fmt.Sprintf("FIGI %s: %s шт, %s лотов, тип=%s",
				pos.GetFigi(),
				decimalToStr(qty.GetUnits(), qty.GetNano()),
				decimalToStr(lots.GetUnits(), lots.GetNano()),
				pos.GetInstrumentType(),
			))
		}
		if len(lines) == 0 {
			return mcp.NewToolResultText("Портфель пуст"), nil
		}
		return mcp.NewToolResultText("Текущий портфель:\n" + formatList(lines)), nil
	})

	// 5) Запуск MCP-сервера (CLI или HTTP/SSE)
	if len(os.Args) > 1 && os.Args[1] == "--http" {
		sseServer := server.NewSSEServer(mcpServer)
		http.Handle("/mcp/", sseServer)
		log.Printf("MCP-сервер запущен в режиме HTTP на порту 8080\n")
		log.Fatal(http.ListenAndServe(":8080", nil))
	} else {
		log.Printf("MCP-сервер запущен в режиме CLI (stdio)\n")
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Ошибка MCP сервера: %v", err)
		}
	}
}

// formatList — вывод элементов построчно
func formatList(items []string) string {
	result := ""
	for _, item := range items {
		result += " - " + item + "\n"
	}
	return result
}

// decimalToStr — форматирует Quotation (units/nano) в строку
func decimalToStr(units int64, nano int32) string {
	sign := ""
	if units == 0 && nano < 0 {
		sign = "-"
	}
	if nano < 0 {
		nano = -nano
	}
	return fmt.Sprintf("%s%d.%09d", sign, units, nano)
}
