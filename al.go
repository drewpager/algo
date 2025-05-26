package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/alpacahq/alpaca-trade-api-go/v3/alpaca"
	"github.com/alpacahq/alpaca-trade-api-go/v3/marketdata"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
)

const (
	VolumeMultiplier = 2.5
	BollingerPeriod  = 20
	BollingerStdDev  = 2.0

	MaxPositionSize = 0.10
	MaxPositions    = 5

	StopLossPercent   = 0.05
	TakeProfitPercent = 0.15

	VolumeLookback = 50
	PriceLookback  = 20
)

type Strategy struct {
	alpacaClient *alpaca.Client
	dataClient   *marketdata.Client
	account      *alpaca.Account
	positions    map[string]*Position
}

type Position struct {
	Symbol       string
	EntryPrice   decimal.Decimal
	Quantity     int64
	StopLoss     decimal.Decimal
	TakeProfit   decimal.Decimal
	EntryTime    time.Time
	PositionType string
}

type Signal struct {
	Symbol    string
	Type      string // "buy" or "sell"
	Strength  float64
	Price     decimal.Decimal
	Volume    int64
	AvgVolume float64
	Reason    string
}

func NewStrategy(apiKey, apiSecret string) (*Strategy, error) {
	client := alpaca.NewClient(alpaca.ClientOpts{
		APIKey:    apiKey,
		APISecret: apiSecret,
		BaseURL:   "https://paper-api.alpaca.markets",
	})

	dataClient := marketdata.NewClient(marketdata.ClientOpts{
		APIKey:    apiKey,
		APISecret: apiSecret,
		BaseURL:   "https://data.alpaca.markets",
	})

	account, err := client.GetAccount()
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %v", err)
	}

	return &Strategy{
		alpacaClient: client,
		dataClient:   dataClient,
		account:      account,
		positions:    make(map[string]*Position),
	}, nil
}

func (s *Strategy) ScanForOpportunities(symbols []string) ([]Signal, error) {
	var signals []Signal

	for _, symbol := range symbols {
		signal, err := s.analyzeSymbol(symbol)
		if err != nil {
			log.Printf("Error analyzing symbol %s: %v", symbol, err)
			continue
		}

		signals = append(signals, signal)
	}

	return signals, nil
}

func (s *Strategy) analyzeSymbol(symbol string) (Signal, error) {
	end := time.Now()
	start := end.AddDate(0, 0, -VolumeLookback)

	barsReq := marketdata.GetBarsRequest{
		TimeFrame: marketdata.OneDay,
		Start:     start,
		End:       end,
		AsOf:      end.Format("01-02-2006"),
		PageLimit: 1000,
	}

	bars, err := s.dataClient.GetBars(symbol, barsReq)
	if err != nil {
		return Signal{}, err
	}

	symbolBars := bars
	if len(symbolBars) < VolumeLookback {
		return Signal{}, fmt.Errorf("not enough data for symbol %s", symbol)
	}

	avgVolume := s.calculateAverageVolume(symbolBars)
	currentBar := symbolBars[len(symbolBars)-1]
	volumeRatio := float64(currentBar.Volume) / avgVolume

	bb := s.calculateBollingerBands(symbolBars)

	signal := s.generateSignal(symbol, currentBar, volumeRatio, bb, avgVolume)

	return *signal, nil
}

func (s *Strategy) calculateAverageVolume(bars []marketdata.Bar) float64 {
	if len(bars) < VolumeLookback {
		return 0
	}

	var totalVolume int64
	start := len(bars) - VolumeLookback
	for i := start; i < len(bars)-1; i++ {
		totalVolume += int64(bars[i].Volume)
	}

	return float64(totalVolume) / float64(VolumeLookback-1)
}

type BollingerBands struct {
	Upper  decimal.Decimal
	Middle decimal.Decimal
	Lower  decimal.Decimal
}

func (s *Strategy) calculateBollingerBands(bars []marketdata.Bar) BollingerBands {
	if len(bars) < BollingerPeriod {
		return BollingerBands{}
	}

	var sum decimal.Decimal
	start := len(bars) - BollingerPeriod
	for i := start; i < len(bars); i++ {
		sum = sum.Add(decimal.NewFromFloat(bars[i].Close))
	}

	sma := sum.Div(decimal.NewFromInt(BollingerPeriod))

	var variance decimal.Decimal
	for i := start; i < len(bars); i++ {
		diff := decimal.NewFromFloat(bars[i].Close).Sub(sma)
		variance = variance.Add(diff.Mul(diff))
	}
	variance = variance.Div(decimal.NewFromInt(BollingerPeriod))
	stdDev := decimal.NewFromFloat(math.Sqrt(variance.InexactFloat64()))

	bandWidth := stdDev.Mul(decimal.NewFromFloat(BollingerStdDev))

	return BollingerBands{
		Upper:  sma.Add(bandWidth),
		Middle: sma,
		Lower:  sma.Sub(bandWidth),
	}
}

func (s *Strategy) generateSignal(symbol string, currentBar marketdata.Bar, volumeRatio float64, bb BollingerBands, avgVolume float64) *Signal {
	if volumeRatio > VolumeMultiplier {
		if decimal.NewFromFloat(currentBar.Close).GreaterThan(bb.Middle) && decimal.NewFromFloat(currentBar.Close).GreaterThan(decimal.NewFromFloat(currentBar.Open)) {
			return &Signal{
				Symbol:    symbol,
				Type:      "buy",
				Strength:  volumeRatio / VolumeMultiplier,
				Price:     decimal.NewFromFloat(currentBar.Close),
				Volume:    int64(currentBar.Volume),
				AvgVolume: avgVolume,
				Reason:    fmt.Sprintf("Volume breakout: %.1fx avg volume, price above SMA", volumeRatio),
			}
		}
	}

	if volumeRatio < 1.0 {
		if decimal.NewFromFloat(currentBar.Close).LessThanOrEqual(bb.Lower) {
			return &Signal{
				Symbol:    symbol,
				Type:      "buy",
				Strength:  bb.Middle.Sub(decimal.NewFromFloat(currentBar.Close)).Div(bb.Middle).InexactFloat64(),
				Price:     decimal.NewFromFloat(currentBar.Close),
				Volume:    int64(currentBar.Volume),
				AvgVolume: avgVolume,
				Reason:    fmt.Sprintf("Mean Reversion, price below lower band: %.1f% avg volume, price below lower band", bb.Middle.Sub(decimal.NewFromFloat(currentBar.Close)).Div(bb.Middle).InexactFloat64()),
			}
		}
	}
	if s.positions[symbol] != nil && decimal.NewFromFloat(currentBar.Close).GreaterThanOrEqual(bb.Upper) {
		return &Signal{
			Symbol:    symbol,
			Type:      "sell",
			Strength:  1.0 - volumeRatio,
			Price:     decimal.NewFromFloat(currentBar.Close),
			Volume:    int64(currentBar.Volume),
			AvgVolume: avgVolume,
			Reason:    fmt.Sprintf("Mean Reversion, price above upper band: %.1f% avg volume, price above upper band", bb.Upper.Sub(decimal.NewFromFloat(currentBar.Close)).Div(bb.Upper).InexactFloat64()),
		}
	}
	return nil
}

func (s *Strategy) ExecuteSignal(signal Signal) error {
	if signal.Type == "buy" {
		return s.enterPosition(signal)
	} else if signal.Type == "sell" {
		return s.exitPosition(signal.Symbol)
	}
	return nil
}

func (s *Strategy) enterPosition(signal Signal) error {
	if _, exists := s.positions[signal.Symbol]; exists {
		return fmt.Errorf("position already exists for %s", signal.Symbol)
	}

	if len(s.positions) >= MaxPositions {
		return fmt.Errorf("max positions reached")
	}

	account, err := s.alpacaClient.GetAccount()
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	equity, _ := account.Equity.Float64()
	positionValue := equity * MaxPositionSize
	quantity := int64(positionValue / signal.Price.InexactFloat64())

	if quantity <= 0 {
		return fmt.Errorf("position size too small")
	}

	qty := decimal.NewFromInt(quantity)
	order, err := s.alpacaClient.PlaceOrder(alpaca.PlaceOrderRequest{
		Symbol:      signal.Symbol,
		Qty:         &qty,
		Side:        alpaca.Buy,
		Type:        alpaca.Market,
		TimeInForce: alpaca.Day,
	})

	if err != nil {
		return fmt.Errorf("failed to place order: %v", err)
	}

	stopLoss := signal.Price.Mul(decimal.NewFromFloat(1 - StopLossPercent))
	takeProfit := signal.Price.Mul(decimal.NewFromFloat(1 + TakeProfitPercent))

	// Create Position Record
	s.positions[signal.Symbol] = &Position{
		Symbol:       signal.Symbol,
		EntryPrice:   signal.Price,
		Quantity:     quantity,
		StopLoss:     stopLoss,
		TakeProfit:   takeProfit,
		EntryTime:    time.Now(),
		PositionType: getPositionType(signal.Type),
	}

	if order != nil {
		log.Printf("Entered position: %s, Qty: %d, Price: %s, Reason: %s",
			signal.Symbol, quantity, signal.Price, signal.Reason)
	}

	// place stop loss order
	_, err = s.alpacaClient.PlaceOrder(alpaca.PlaceOrderRequest{
		Symbol:      signal.Symbol,
		Qty:         &qty,
		Side:        alpaca.Sell,
		Type:        alpaca.Stop,
		TimeInForce: alpaca.GTC,
		StopPrice:   &stopLoss,
	})

	if err != nil {
		return fmt.Errorf("failed to place stop loss order: %v", err)
	}

	return nil
}

func (s *Strategy) exitPosition(symbol string) error {
	position, exists := s.positions[symbol]
	if !exists {
		return fmt.Errorf("no position found for %s", symbol)
	}

	qty := decimal.NewFromInt(position.Quantity)
	_, err := s.alpacaClient.PlaceOrder(alpaca.PlaceOrderRequest{
		Symbol:      symbol,
		Qty:         &qty,
		Side:        alpaca.Sell,
		Type:        alpaca.Market,
		TimeInForce: alpaca.Day,
	})

	if err != nil {
		return fmt.Errorf("failed to place exit order: %v", err)
	}

	delete(s.positions, symbol)

	log.Printf("Exited position: %s", symbol)

	return nil
}

func getPositionType(reason string) string {
	if len(reason) > 6 && reason[:6] == "Volume" {
		return "breakout"
	}
	return "reversion"
}

func (s *Strategy) Run(symbols []string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// check if market is open
			clock, err := s.alpacaClient.GetClock()
			if err != nil {
				log.Printf("Error getting market clock: %v", err)
				continue
			}

			if !clock.IsOpen {
				log.Println("Market is closed, skipping scan")
				continue
			}

			signals, err := s.ScanForOpportunities(symbols)
			if err != nil {
				log.Printf("Error scanning for opportunities: %w", err)
				continue
			}

			for _, signal := range signals {
				if err := s.ExecuteSignal(signal); err != nil {
					log.Printf("Error executing signal for %s: %v", signal.Symbol, err)
				}
			}

			s.CheckPositions()
		}
	}
}

func (s *Strategy) CheckPositions() {
	for symbol, position := range s.positions {
		quote, err := s.dataClient.GetLatestQuote(symbol, marketdata.GetLatestQuoteRequest{})
		if err != nil {
			log.Printf("Error getting latest quote for %s: %v", symbol, err)
			continue
		}

		currentPrice := quote.AskPrice
		if decimal.NewFromFloat(currentPrice).IsZero() {
			currentPrice = quote.BidPrice
		}

		if decimal.NewFromFloat(currentPrice).LessThanOrEqual(position.StopLoss) {
			log.Printf("Stop loss triggered, exiting position for %s", symbol)
			s.exitPosition(symbol)
			continue
		}

		if decimal.NewFromFloat(currentPrice).GreaterThanOrEqual(position.TakeProfit) {
			log.Printf("Profit reached, exiting position for %s", symbol)
			s.exitPosition(symbol)
			continue
		}

		if position.PositionType == "reversion" {
			signal, _ := s.analyzeSymbol(symbol)
			if signal.Type == "sell" {
				log.Printf("Reversion signal, exiting position for %s", symbol)
				s.exitPosition(symbol)
				continue
			}
		}
	}
}

func main() {
	err := godotenv.Load(".env")
	if err != nil {
		fmt.Println("Error loading .env file")
	}
	apiKey := os.Getenv("ALPACA_API_KEY")
	apiSecret := os.Getenv("ALPACA_API_SECRET")

	if apiKey == "" || apiSecret == "" {
		log.Fatal("Please set APCA_API_KEY_ID and APCA_API_SECRET_KEY environment variables")
	}

	strategy, err := NewStrategy(apiKey, apiSecret)
	if err != nil {
		fmt.Printf("Error creating strategy: %v\n", err)
	}

	symbols := []string{"AAPL", "MSFT", "GOOGL", "AMZN", "TSLA", "NVDA", "META", "JPM", "BAC", "WMT"}
	fmt.Println("Starting O'neill trading strategy...")

	strategy.Run(symbols, 5*time.Minute)
}
