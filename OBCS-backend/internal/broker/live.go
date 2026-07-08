package broker

import (
	"context"
	"fmt"

	"github.com/obcs-nifty/backend/internal/angelone"
	"github.com/obcs-nifty/backend/internal/config"
)

// LiveBroker places real NFO option orders through the AngelOne SmartAPI. It is
// only constructed when TRADING_MODE=live with valid credentials. Each leg is
// resolved against the scrip master, submitted as a CARRYFORWARD market order,
// then the executed premium is approximated by the post-order LTP.
type LiveBroker struct {
	client *angelone.Client
	scrip  *angelone.ScripMaster
}

// NewLiveBroker wires a live broker with an authenticated client.
func NewLiveBroker(client *angelone.Client, scrip *angelone.ScripMaster) *LiveBroker {
	return &LiveBroker{client: client, scrip: scrip}
}

// Mode reports live mode.
func (b *LiveBroker) Mode() config.TradingMode { return config.ModeLive }

// Buy places a BUY market order for the leg.
func (b *LiveBroker) Buy(ctx context.Context, underlying string, leg Leg) (float64, string, error) {
	return b.execute(ctx, underlying, leg, "BUY")
}

// Sell places a SELL market order for the leg.
func (b *LiveBroker) Sell(ctx context.Context, underlying string, leg Leg) (float64, string, error) {
	return b.execute(ctx, underlying, leg, "SELL")
}

func (b *LiveBroker) execute(ctx context.Context, underlying string, leg Leg, side string) (float64, string, error) {
	inst, err := b.scrip.ResolveOption(ctx, underlying, leg.Expiry, leg.Strike, leg.OptionType)
	if err != nil {
		return 0, "", fmt.Errorf("resolve %s %d %s: %w", underlying, leg.Strike, leg.OptionType, err)
	}
	ref, err := b.client.PlaceOrder(ctx, angelone.OrderRequest{
		TradingSymbol:   inst.Symbol,
		SymbolToken:     inst.Token,
		TransactionType: side,
		Exchange:        "NFO",
		Quantity:        leg.Qty,
		ProductType:     "CARRYFORWARD",
	})
	if err != nil {
		return 0, "", err
	}
	// Approximate the executed premium with the current LTP. A production
	// system should poll the order book for the true average fill price.
	price, err := b.client.LTP(ctx, "NFO", inst.Symbol, inst.Token)
	if err != nil {
		// Order placed but price fetch failed; return the model price as a
		// best-effort estimate and surface the ref so it can be reconciled.
		return leg.ModelPrice, ref, nil
	}
	return price, ref, nil
}
