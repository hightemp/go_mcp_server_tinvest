package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
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

// InvestClient инкапсулирует работу с InvestAPI и окружением
type InvestClient struct {
	ctx       context.Context
	sdk       *investgo.Client
	accountID string
}

func NewInvestClient() (*InvestClient, error) {
	// подхват .env при наличии
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env не найден или не загружен: %v", err)
	}

	token := os.Getenv("TINKOFF_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("переменная окружения TINKOFF_TOKEN не задана")
	}

	endpoint := os.Getenv("TINKOFF_ENDPOINT")
	if endpoint == "" {
		endpoint = "invest-public-api.tinkoff.ru:443"
	}

	appName := os.Getenv("APP_NAME")
	if appName == "" {
		appName = "go-mcp-tinvest"
	}

	ctx := context.Background()
	conf := investgo.Config{
		Token:    token,
		EndPoint: endpoint,
		AppName:  appName,
	}

	cli, err := investgo.NewClient(ctx, conf, stdLogger{})
	if err != nil {
		return nil, fmt.Errorf("ошибка инициализации клиента InvestAPI: %w", err)
	}

	return &InvestClient{
		ctx:       ctx,
		sdk:       cli,
		accountID: cli.Config.AccountId,
	}, nil
}

func (c *InvestClient) Close() { _ = c.sdk.Stop() }

func main() {
	// Параметры транспорта
	var transport string
	var host string
	var port string
	flag.StringVar(&transport, "t", "sse", "Тип транспорта (stdio или sse)")
	flag.StringVar(&host, "h", "0.0.0.0", "Хост SSE сервера")
	flag.StringVar(&port, "p", "8100", "Порт SSE сервера")
	flag.Parse()

	ic, err := NewInvestClient()
	if err != nil {
		log.Fatalf("Ошибка создания InvestClient: %v", err)
	}
	defer ic.Close()
	log.Printf("Используется аккаунт: %s", ic.accountID)

	// MCP сервер
	mcpServer := server.NewMCPServer(
		"Tinkoff Investments MCP",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)

	// Инструменты MCP и обработчики
	searchStocksTool := mcp.NewTool("search_stocks",
		mcp.WithDescription("Поиск акций по тикеру или названию"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Часть тикера или названия акции")),
	)
	mcpServer.AddTool(searchStocksTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return searchStocksHandler(ctx, req, ic)
	})

	searchBondsTool := mcp.NewTool("search_bonds",
		mcp.WithDescription("Поиск облигаций по тикеру или названию"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Часть тикера или названия облигации")),
	)
	mcpServer.AddTool(searchBondsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return searchBondsHandler(ctx, req, ic)
	})

	searchFundsTool := mcp.NewTool("search_funds",
		mcp.WithDescription("Поиск фондов/ETF по тикеру или названию"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Часть тикера или названия фонда (ETF)")),
	)
	mcpServer.AddTool(searchFundsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return searchFundsHandler(ctx, req, ic)
	})

	buyTool := mcp.NewTool("buy",
		mcp.WithDescription("Купить инструмент (рыночная заявка)"),
		mcp.WithString("ticker", mcp.Required(), mcp.Description("Тикер или часть названия для поиска инструмента")),
		mcp.WithNumber("lots", mcp.Required(), mcp.Description("Количество лотов")),
	)
	mcpServer.AddTool(buyTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return buyHandler(ctx, req, ic)
	})

	sellTool := mcp.NewTool("sell",
		mcp.WithDescription("Продать инструмент (рыночная заявка)"),
		mcp.WithString("ticker", mcp.Required(), mcp.Description("Тикер или часть названия для поиска инструмента")),
		mcp.WithNumber("lots", mcp.Required(), mcp.Description("Количество лотов")),
	)
	mcpServer.AddTool(sellTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return sellHandler(ctx, req, ic)
	})

	portfolioTool := mcp.NewTool("portfolio",
		mcp.WithDescription("Просмотр текущего портфеля (позиции и остатки)"),
	)
	mcpServer.AddTool(portfolioTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return portfolioHandler(ctx, req, ic)
	})

	// Запуск транспорта
	if transport == "sse" {
		sseServer := server.NewSSEServer(mcpServer, server.WithBaseURL(fmt.Sprintf("http://%s:%s", host, port)))
		log.Printf("SSE сервер слушает %s:%s URL: http://%s:%s/sse", host, port, host, port)
		if err := sseServer.Start(fmt.Sprintf("%s:%s", host, port)); err != nil {
			log.Fatalf("Ошибка запуска SSE сервера: %v", err)
		}
	} else {
		log.Printf("МCP-сервер запущен в режиме CLI (stdio)")
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Ошибка MCP сервера: %v", err)
		}
	}
}

// Handlers
func searchStocksHandler(ctx context.Context, req mcp.CallToolRequest, ic *InvestClient) (*mcp.CallToolResult, error) {
	query, _ := req.RequireString("query")
	instruments := ic.sdk.NewInstrumentsServiceClient()
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
}

func searchBondsHandler(ctx context.Context, req mcp.CallToolRequest, ic *InvestClient) (*mcp.CallToolResult, error) {
	query, _ := req.RequireString("query")
	instruments := ic.sdk.NewInstrumentsServiceClient()
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
}

func searchFundsHandler(ctx context.Context, req mcp.CallToolRequest, ic *InvestClient) (*mcp.CallToolResult, error) {
	query, _ := req.RequireString("query")
	instruments := ic.sdk.NewInstrumentsServiceClient()
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
}

func buyHandler(ctx context.Context, req mcp.CallToolRequest, ic *InvestClient) (*mcp.CallToolResult, error) {
	q, _ := req.RequireString("ticker")
	lotsF, _ := req.RequireFloat("lots")
	lots := int64(lotsF)

	instruments := ic.sdk.NewInstrumentsServiceClient()
	found, err := instruments.FindInstrument(q)
	if err != nil || len(found.GetInstruments()) == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("Инструмент %q не найден", q)), nil
	}
	inst := found.GetInstruments()[0]

	orders := ic.sdk.NewOrdersServiceClient()
	_, err = orders.Buy(&investgo.PostOrderRequestShort{
		InstrumentId: inst.GetFigi(),
		Quantity:     lots,
		Price:        nil, // market
		AccountId:    ic.accountID,
		OrderType:    pb.OrderType_ORDER_TYPE_MARKET,
		OrderId:      investgo.CreateUid(),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Ошибка покупки %s: %v", inst.GetTicker(), err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Отправлена заявка на покупку %d лотов %s (%s)", lots, inst.GetName(), inst.GetTicker())), nil
}

func sellHandler(ctx context.Context, req mcp.CallToolRequest, ic *InvestClient) (*mcp.CallToolResult, error) {
	q, _ := req.RequireString("ticker")
	lotsF, _ := req.RequireFloat("lots")
	lots := int64(lotsF)

	instruments := ic.sdk.NewInstrumentsServiceClient()
	found, err := instruments.FindInstrument(q)
	if err != nil || len(found.GetInstruments()) == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("Инструмент %q не найден", q)), nil
	}
	inst := found.GetInstruments()[0]

	orders := ic.sdk.NewOrdersServiceClient()
	_, err = orders.Sell(&investgo.PostOrderRequestShort{
		InstrumentId: inst.GetFigi(),
		Quantity:     lots,
		Price:        nil, // market
		AccountId:    ic.accountID,
		OrderType:    pb.OrderType_ORDER_TYPE_MARKET,
		OrderId:      investgo.CreateUid(),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Ошибка продажи %s: %v", inst.GetTicker(), err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Отправлена заявка на продажу %d лотов %s (%s)", lots, inst.GetName(), inst.GetTicker())), nil
}

func portfolioHandler(ctx context.Context, req mcp.CallToolRequest, ic *InvestClient) (*mcp.CallToolResult, error) {
	ops := ic.sdk.NewOperationsServiceClient()
	pf, err := ops.GetPortfolio(ic.accountID, pb.PortfolioRequest_CurrencyRequest(0))
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
}

// Вспомогательные функции
func formatList(items []string) string {
	result := ""
	for _, item := range items {
		result += " - " + item + "\n"
	}
	return result
}

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
