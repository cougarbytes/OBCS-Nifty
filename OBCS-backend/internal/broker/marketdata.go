package broker

import (
	"context"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/obcs-nifty/backend/internal/angelone"
)

// SyntheticMarketData produces a deterministic geometric-random-walk price
// series so paper mode works end-to-end without broker credentials. It is
// clearly labelled as synthetic in the status bar; it is NOT market data.
type SyntheticMarketData struct {
	base   float64
	sigma  float64
	mu     sync.Mutex
	rng    *rand.Rand
	closes []float64
	spot   float64
}

// NewSyntheticMarketData seeds a walk around `base` with daily vol `sigma`.
func NewSyntheticMarketData(base, sigma float64) *SyntheticMarketData {
	if base <= 0 {
		base = 24000
	}
	if sigma <= 0 {
		sigma = 0.01
	}
	m := &SyntheticMarketData{
		base:  base,
		sigma: sigma,
		rng:   rand.New(rand.NewSource(42)),
	}
	// Pre-generate ~300 daily closes ending near base.
	price := base * 0.85
	for i := 0; i < 300; i++ {
		price *= math.Exp(m.sigma*m.rng.NormFloat64() + 0.0003)
		m.closes = append(m.closes, price)
	}
	m.spot = m.closes[len(m.closes)-1]
	return m
}

// Source labels the data origin.
func (m *SyntheticMarketData) Source() string { return "synthetic" }

// Spot advances the walk one step and returns the new spot.
func (m *SyntheticMarketData) Spot(_ context.Context) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spot *= math.Exp(m.sigma * m.rng.NormFloat64())
	return m.spot, nil
}

// DailyCloses returns the most recent `lookback` closes, oldest first.
func (m *SyntheticMarketData) DailyCloses(_ context.Context, lookback int) ([]float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lookback <= 0 || lookback > len(m.closes) {
		lookback = len(m.closes)
	}
	out := make([]float64, lookback)
	copy(out, m.closes[len(m.closes)-lookback:])
	return out, nil
}

// AngelOneMarketData sources spot and daily closes from the AngelOne SmartAPI.
type AngelOneMarketData struct {
	client      *angelone.Client
	indexToken  string
	indexSymbol string
	exchange    string
}

// NewAngelOneMarketData wires an AngelOne market data source. The NIFTY 50 spot
// index token on NSE is 99926000.
func NewAngelOneMarketData(client *angelone.Client, indexToken, indexSymbol string) *AngelOneMarketData {
	if indexToken == "" {
		indexToken = "99926000"
	}
	if indexSymbol == "" {
		indexSymbol = "Nifty 50"
	}
	return &AngelOneMarketData{
		client:      client,
		indexToken:  indexToken,
		indexSymbol: indexSymbol,
		exchange:    "NSE",
	}
}

// Source labels the data origin.
func (m *AngelOneMarketData) Source() string { return "AngelOne" }

// Spot fetches the current index LTP.
func (m *AngelOneMarketData) Spot(ctx context.Context) (float64, error) {
	return m.client.LTP(ctx, m.exchange, m.indexSymbol, m.indexToken)
}

// DailyCloses fetches recent daily closes ending today.
func (m *AngelOneMarketData) DailyCloses(ctx context.Context, lookback int) ([]float64, error) {
	if lookback <= 0 {
		lookback = 60
	}
	to := time.Now()
	from := to.AddDate(0, 0, -(lookback*2 + 10)) // pad for weekends/holidays
	candles, err := m.client.History(ctx, m.exchange, m.indexToken,
		from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"))
	if err != nil {
		return nil, err
	}
	closes := make([]float64, 0, len(candles))
	for _, c := range candles {
		closes = append(closes, c.Close)
	}
	if len(closes) > lookback {
		closes = closes[len(closes)-lookback:]
	}
	return closes, nil
}
