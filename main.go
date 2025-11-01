package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/tinkoff/invest-api-go-sdk/investgo"
	pb "github.com/tinkoff/invest-api-go-sdk/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("переменная окружения TINKOFF_TOKEN не задана")
	}

	endpoint := os.Getenv("TINKOFF_ENDPOINT")
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		// Замечание: убедитесь, что endpoint соответствует типу токена (prod vs sandbox)
		// Для sandbox используйте: sandbox-invest-public-api.tinkoff.ru:443
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
	// ВАЖНО: чтобы investgo.NewClient не дергал SandboxService при пустом AccountId (что ломает прод с 40003),
	// заполним AccountId из окружения заранее.
	accEnv := strings.TrimSpace(os.Getenv("TINKOFF_ACCOUNT_ID"))
	if accEnv != "" {
		conf.AccountId = accEnv
		log.Printf("AccountID из окружения: %s", accEnv)
	}

	cli, err := investgo.NewClient(ctx, conf, stdLogger{})
	if err != nil {
		return nil, fmt.Errorf("ошибка инициализации клиента InvestAPI: %w", err)
	}

	// Определяем AccountID:
	// 1) из окружения TINKOFF_ACCOUNT_ID
	// 2) из конфигурации клиента (если там задан)
	// 3) автоматически выбираем первый OPEN счёт через UsersService
	accID := os.Getenv("TINKOFF_ACCOUNT_ID")
	if accID == "" {
		if cli.Config.AccountId != "" {
			accID = cli.Config.AccountId
		} else {
			accID, err = resolveAccountID(cli)
			if err != nil {
				return nil, fmt.Errorf("не удалось определить AccountID: %w", err)
			}
		}
	}
	log.Printf("Используется endpoint: %s, app: %s", endpoint, appName)
	log.Printf("Выбран AccountID: %s", accID)

	return &InvestClient{
		ctx:       ctx,
		sdk:       cli,
		accountID: accID,
	}, nil
}

func resolveAccountID(cli *investgo.Client) (string, error) {
	users := cli.NewUsersServiceClient()
	resp, err := users.GetAccounts()
	if err != nil {
		return "", fmt.Errorf("ошибка получения списка счетов: %w", err)
	}
	for _, acc := range resp.GetAccounts() {
		if acc.GetStatus() == pb.AccountStatus_ACCOUNT_STATUS_OPEN {
			return acc.GetId(), nil
		}
	}
	return "", fmt.Errorf("не найден ни один счёт в статусе OPEN")
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

	// Market Data инструменты
	lastPriceTool := mcp.NewTool("last_price",
		mcp.WithDescription("Последняя цена инструмента"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Тикер/название или FIGI инструмента")),
	)
	mcpServer.AddTool(lastPriceTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return lastPriceHandler(ctx, req, ic)
	})

	orderbookTool := mcp.NewTool("orderbook",
		mcp.WithDescription("Стакан заявок по инструменту"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Тикер/название или FIGI инструмента")),
		mcp.WithNumber("depth", mcp.Required(), mcp.Description("Глубина стакана (1-50)")),
	)
	mcpServer.AddTool(orderbookTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return orderbookHandler(ctx, req, ic)
	})

	candlesTool := mcp.NewTool("candles",
		mcp.WithDescription("Исторические свечи по инструменту за период"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Тикер/название или FIGI инструмента")),
		mcp.WithString("from", mcp.Required(), mcp.Description("Начало периода (RFC3339), напр. 2024-01-01T00:00:00Z")),
		mcp.WithString("to", mcp.Required(), mcp.Description("Конец периода (RFC3339), напр. 2024-01-31T23:59:59Z")),
		mcp.WithString("interval", mcp.Required(), mcp.Description("Интервал: 1m,5m,15m,1h,1d")),
	)
	mcpServer.AddTool(candlesTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return candlesHandler(ctx, req, ic)
	})

	tradingStatusTool := mcp.NewTool("trading_status",
		mcp.WithDescription("Статус торгов по инструменту"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Тикер/название или FIGI инструмента")),
	)
	mcpServer.AddTool(tradingStatusTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tradingStatusHandler(ctx, req, ic)
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
	if ic.accountID == "" {
		return mcp.NewToolResultError("Ошибка получения портфеля: AccountID не задан. Укажите переменную окружения TINKOFF_ACCOUNT_ID либо откройте счёт и перезапустите сервер."), nil
	}
	pf, err := ops.GetPortfolio(ic.accountID, pb.PortfolioRequest_CurrencyRequest(0))
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
			return mcp.NewToolResultError("Ошибка получения портфеля: счёт не найден (NotFound/50004). Проверьте: корректность AccountID, соответствие endpoint среде (sandbox vs prod), и права токена."), nil
		}
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

// Market Data handlers
type InstrumentRef struct {
	Figi   string
	Ticker string
	Name   string
}

func findInstrumentRef(ic *InvestClient, q string) (*InstrumentRef, error) {
	instruments := ic.sdk.NewInstrumentsServiceClient()
	resp, err := instruments.FindInstrument(q)
	if err != nil {
		return nil, fmt.Errorf("ошибка поиска инструмента: %w", err)
	}
	if len(resp.GetInstruments()) == 0 {
		return nil, fmt.Errorf("инструмент по запросу %q не найден", q)
	}
	it := resp.GetInstruments()[0]
	return &InstrumentRef{
		Figi:   it.GetFigi(),
		Ticker: it.GetTicker(),
		Name:   it.GetName(),
	}, nil
}

func lastPriceHandler(ctx context.Context, req mcp.CallToolRequest, ic *InvestClient) (*mcp.CallToolResult, error) {
	q, _ := req.RequireString("query")
	inst, err := findInstrumentRef(ic, q)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	md := ic.sdk.NewMarketDataServiceClient()
	lpResp, err := md.GetLastPrices([]string{inst.Figi})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Ошибка получения последней цены: %v", err)), nil
	}
	lps := lpResp.GetLastPrices()
	if len(lps) == 0 {
		return mcp.NewToolResultText("Нет данных о последней цене"), nil
	}
	lp := lps[0]
	price := lp.GetPrice()
	ts := lp.GetTime().AsTime()
	text := fmt.Sprintf("%s (%s), FIGI %s — последняя цена: %s, время: %s",
		inst.Name, inst.Ticker, inst.Figi, quotationToStr(price), ts.Format(time.RFC3339))
	return mcp.NewToolResultText(text), nil
}

func orderbookHandler(ctx context.Context, req mcp.CallToolRequest, ic *InvestClient) (*mcp.CallToolResult, error) {
	q, _ := req.RequireString("query")
	depthF, _ := req.RequireFloat("depth")
	if depthF < 1 {
		depthF = 1
	}
	if depthF > 50 {
		depthF = 50
	}
	depth := int32(depthF)

	inst, err := findInstrumentRef(ic, q)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	md := ic.sdk.NewMarketDataServiceClient()
	ob, err := md.GetOrderBook(inst.Figi, depth)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Ошибка получения стакана: %v", err)), nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Стакан %s (%s), FIGI %s, глубина %d", inst.Name, inst.Ticker, inst.Figi, depth))
	lines = append(lines, "BIDS (покупка):")
	for i, b := range ob.GetBids() {
		if int32(i) >= depth {
			break
		}
		lines = append(lines, fmt.Sprintf("  #%d %s × %d", i+1, quotationToStr(b.GetPrice()), b.GetQuantity()))
	}
	lines = append(lines, "ASKS (продажа):")
	for i, a := range ob.GetAsks() {
		if int32(i) >= depth {
			break
		}
		lines = append(lines, fmt.Sprintf("  #%d %s × %d", i+1, quotationToStr(a.GetPrice()), a.GetQuantity()))
	}

	return mcp.NewToolResultText(strings.Join(lines, "\n")), nil
}

func candlesHandler(ctx context.Context, req mcp.CallToolRequest, ic *InvestClient) (*mcp.CallToolResult, error) {
	q, _ := req.RequireString("query")
	fromStr, _ := req.RequireString("from")
	toStr, _ := req.RequireString("to")
	intervalStr, _ := req.RequireString("interval")

	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Некорректный формат from: %v", err)), nil
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Некорректный формат to: %v", err)), nil
	}
	if !to.After(from) {
		return mcp.NewToolResultError("Параметр 'to' должен быть позже, чем 'from'"), nil
	}
	interval, err := parseCandleInterval(intervalStr)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	inst, err := findInstrumentRef(ic, q)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	md := ic.sdk.NewMarketDataServiceClient()
	candlesResp, err := md.GetCandles(inst.Figi, interval, from, to)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Ошибка получения свечей: %v", err)), nil
	}
	candles := candlesResp.GetCandles()
	if len(candles) == 0 {
		return mcp.NewToolResultText("Свечи не найдены за указанный период"), nil
	}

	var out []string
	out = append(out, fmt.Sprintf("Свечи %s (%s) FIGI %s, %s, %s → %s, всего: %d",
		inst.Name, inst.Ticker, inst.Figi, strings.ToUpper(intervalStr),
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), len(candles),
	))
	// Выведем до 50 строк, чтобы не шуметь
	limit := len(candles)
	if limit > 50 {
		limit = 50
	}
	for i := 0; i < limit; i++ {
		c := candles[i]
		ts := c.GetTime().AsTime().UTC().Format(time.RFC3339)
		o := quotationToStr(c.GetOpen())
		h := quotationToStr(c.GetHigh())
		l := quotationToStr(c.GetLow())
		cl := quotationToStr(c.GetClose())
		v := c.GetVolume()
		out = append(out, fmt.Sprintf(" - %s  O:%s H:%s L:%s C:%s V:%d", ts, o, h, l, cl, v))
	}
	if len(candles) > limit {
		out = append(out, fmt.Sprintf(" ... и ещё %d свечей", len(candles)-limit))
	}

	return mcp.NewToolResultText(strings.Join(out, "\n")), nil
}

func tradingStatusHandler(ctx context.Context, req mcp.CallToolRequest, ic *InvestClient) (*mcp.CallToolResult, error) {
	q, _ := req.RequireString("query")
	inst, err := findInstrumentRef(ic, q)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	md := ic.sdk.NewMarketDataServiceClient()
	st, err := md.GetTradingStatus(inst.Figi)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Ошибка получения статуса торгов: %v", err)), nil
	}

	text := fmt.Sprintf("Статус торгов для %s (%s), FIGI %s: %v",
		inst.Name, inst.Ticker, inst.Figi, st.GetTradingStatus())
	return mcp.NewToolResultText(text), nil
}

// Вспомогательные функции
func parseCandleInterval(s string) (pb.CandleInterval, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1m", "1min":
		return pb.CandleInterval_CANDLE_INTERVAL_1_MIN, nil
	case "5m", "5min":
		return pb.CandleInterval_CANDLE_INTERVAL_5_MIN, nil
	case "15m", "15min":
		return pb.CandleInterval_CANDLE_INTERVAL_15_MIN, nil
	case "1h", "60m":
		return pb.CandleInterval_CANDLE_INTERVAL_HOUR, nil
	case "1d", "1day", "day", "d":
		return pb.CandleInterval_CANDLE_INTERVAL_DAY, nil
	default:
		return 0, fmt.Errorf("неизвестный interval %q. Допустимо: 1m,5m,15m,1h,1d", s)
	}
}

func quotationToStr(q *pb.Quotation) string {
	return decimalToStr(q.GetUnits(), q.GetNano())
}

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
