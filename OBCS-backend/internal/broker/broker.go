// Package broker abstracts market data and order execution so the strategy
// runner is identical in paper and live mode. Paper mode (the default) never
// contacts the live order API: fills equal the strategy's model prices. Live
// mode routes orders through the AngelOne SmartAPI and is only constructed when
// TRADING_MODE=live with valid credentials.
package broker

import (
	"context"
	"time"

	"github.com/obcs-nifty/backend/internal/config"
)

// Leg describes one option leg to execute.
type Leg struct {
	Strike     int
	Expiry     time.Time
	OptionType string  // "CE"
	Qty        int     // total contracts = lots * lot_size
	ModelPrice float64 // strategy model premium (points); paper fill uses this
}

// MarketData provides the underlying prices the strategy needs.
type MarketData interface {
	// Spot returns the current underlying index level.
	Spot(ctx context.Context) (float64, error)
	// DailyCloses returns up to `lookback` recent daily closes, oldest first.
	DailyCloses(ctx context.Context, lookback int) ([]float64, error)
	// Source labels where the data came from (e.g. "AngelOne", "synthetic").
	Source() string
}

// Broker executes single-leg option orders and returns the executed premium.
type Broker interface {
	Mode() config.TradingMode
	// Buy/Sell place a market order for the leg and return the executed premium
	// (points), a broker order reference and an error.
	Buy(ctx context.Context, underlying string, leg Leg) (price float64, ref string, err error)
	Sell(ctx context.Context, underlying string, leg Leg) (price float64, ref string, err error)
}
