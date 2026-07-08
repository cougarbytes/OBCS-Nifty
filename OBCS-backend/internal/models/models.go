// Package models holds the domain types shared across the API, DB and runner
// layers. Field tags define the JSON contract consumed by the frontend and the
// OpenAPI spec.
package models

import "time"

// StrategyStatus enumerates the runner lifecycle states.
type StrategyStatus string

const (
	StatusRunning StrategyStatus = "running"
	StatusStopped StrategyStatus = "stopped"
)

// StrategyState is the singleton runner state row.
type StrategyState struct {
	Status                    StrategyStatus `json:"status"`
	TradingMode               string         `json:"trading_mode"`
	StartedAt                 *time.Time     `json:"started_at,omitempty"`
	StoppedAt                 *time.Time     `json:"stopped_at,omitempty"`
	AccumulatedRuntimeSeconds int64          `json:"accumulated_runtime_seconds"`
	Equity                    float64        `json:"equity"`
	LastMessage               string         `json:"last_message,omitempty"`
	UpdatedAt                 time.Time      `json:"updated_at"`
	// RuntimeSeconds is derived: accumulated + current session if running.
	RuntimeSeconds int64 `json:"runtime_seconds"`
}

// TradeStatus enumerates a trade's lifecycle.
type TradeStatus string

const (
	TradeOpen   TradeStatus = "open"
	TradeClosed TradeStatus = "closed"
	TradeError  TradeStatus = "error"
)

// Trade is one overnight bull call spread round trip.
type Trade struct {
	ID             string      `json:"id"`
	Status         TradeStatus `json:"status"`
	TradingMode    string      `json:"trading_mode"`
	Underlying     string      `json:"underlying"`
	EntryTime      time.Time   `json:"entry_time"`
	ExitTime       *time.Time  `json:"exit_time,omitempty"`
	ContractExpiry time.Time   `json:"contract_expiry"`
	K1             int         `json:"k1"`
	K2             int         `json:"k2"`
	Lots           int         `json:"lots"`
	LotSize        int         `json:"lot_size"`
	EntrySpot      float64     `json:"entry_spot"`
	ExitSpot       *float64    `json:"exit_spot,omitempty"`
	SigmaATM       float64     `json:"sigma_atm"`
	EntryDebit     float64     `json:"entry_debit"`
	ExitValue      *float64    `json:"exit_value,omitempty"`
	MarginUsed     float64     `json:"margin_used"`
	GrossPnL       *float64    `json:"gross_pnl,omitempty"`
	Costs          *float64    `json:"costs,omitempty"`
	NetPnL         *float64    `json:"net_pnl,omitempty"`
	KellyF         *float64    `json:"kelly_f,omitempty"`
	BrokerOrderRef string      `json:"broker_order_ref,omitempty"`
	Note           string      `json:"note,omitempty"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	// OptionData is the live option snapshot(s) for this trade (entry/exit).
	OptionData []OptionData `json:"option_data,omitempty"`
}

// OptionData is a persisted LIVE option Greek snapshot for a trade leg.
type OptionData struct {
	ID         string    `json:"id"`
	TradeID    string    `json:"trade_id"`
	Leg        string    `json:"leg"`   // long | short | net
	Phase      string    `json:"phase"` // entry | exit
	DataType   string    `json:"data_type"`
	Price      float64   `json:"price"`
	Delta      float64   `json:"delta"`
	Gamma      float64   `json:"gamma"`
	Theta      float64   `json:"theta"`
	Vega       float64   `json:"vega"`
	IV         float64   `json:"iv"`
	CapturedAt time.Time `json:"captured_at"`
}

// DailyPnL is one aggregated day of realized P&L (from v_daily_pnl).
type DailyPnL struct {
	Day    string  `json:"day"` // YYYY-MM-DD (IST)
	NetPnL float64 `json:"net_pnl"`
	Trades int     `json:"trades"`
	Wins   int     `json:"wins"`
}

// Holiday is an NSE trading holiday.
type Holiday struct {
	Date        string `json:"date"` // YYYY-MM-DD
	Description string `json:"description"`
	Year        int    `json:"year"`
}
