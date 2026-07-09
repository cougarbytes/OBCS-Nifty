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
	Tag        string  // groups the legs of one spread on the broker (optional)
}

// SpreadLeg pairs a leg with its transaction side. Used to price the whole
// spread's margin as a single hedged basket.
type SpreadLeg struct {
	Leg  Leg
	Side string // BUY | SELL
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

// FundsProvider is implemented by brokers that can report the account's real
// tradable equity. Live mode uses it so sizing is driven by broker cash rather
// than a hard-coded initial capital; paper brokers do not implement it.
type FundsProvider interface {
	AvailableEquity(ctx context.Context) (float64, error)
}

// MarginProvider is implemented by brokers that can price the broker-side margin
// for a hedged spread basket. Live mode uses it to record the true margin and to
// gate an entry on sufficient funds; paper brokers do not implement it.
type MarginProvider interface {
	SpreadMargin(ctx context.Context, underlying string, legs []SpreadLeg) (float64, error)
}
