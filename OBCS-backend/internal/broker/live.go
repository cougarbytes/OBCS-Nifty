package broker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	ack, err := b.client.PlaceOrder(ctx, angelone.OrderRequest{
		TradingSymbol:   inst.Symbol,
		SymbolToken:     inst.Token,
		TransactionType: side,
		Exchange:        "NFO",
		Quantity:        leg.Qty,
		ProductType:     "CARRYFORWARD",
		OrderTag:        leg.Tag,
	})
	if err != nil {
		return 0, "", err
	}

	// Resolve the true average fill from the order-status API. A hard rejection
	// is a real error (so the caller can unwind the other leg); a still-pending
	// order falls back to the current LTP, then the model price.
	price, done, err := b.settledFillPrice(ctx, ack.UniqueOrderID)
	if err != nil {
		// Stamp the broker order id onto a terminal rejection so the persisted
		// reason can be reconciled against the broker's order book.
		var rej *OrderRejectedError
		if errors.As(err, &rej) {
			rej.OrderRef = ack.OrderID
		}
		return 0, ack.OrderID, fmt.Errorf("order %s %s: %w", side, ack.OrderID, err)
	}
	if done {
		return price, ack.OrderID, nil
	}
	if ltp, err := b.client.LTP(ctx, "NFO", inst.Symbol, inst.Token); err == nil && ltp > 0 {
		return ltp, ack.OrderID, nil
	}
	// Order placed but fill/price unresolved; return the model price as a
	// best-effort estimate and surface the ref so it can be reconciled.
	return leg.ModelPrice, ack.OrderID, nil
}

// settledFillPrice polls the order-status API for the average fill price.
// Returns (price, true, nil) once filled, (0, false, err) on rejection/cancel,
// and (0, false, nil) if no terminal state is reached before the retries or the
// context is exhausted (the caller then falls back to LTP/model).
func (b *LiveBroker) settledFillPrice(ctx context.Context, uniqueOrderID string) (float64, bool, error) {
	if uniqueOrderID == "" {
		return 0, false, nil
	}
	for i := 0; i < 6; i++ {
		d, err := b.client.OrderStatus(ctx, uniqueOrderID)
		if err == nil {
			switch strings.ToLower(d.Status) {
			case "complete":
				if d.AveragePrice > 0 {
					return d.AveragePrice, true, nil
				}
				return 0, false, nil
			case "rejected", "cancelled", "canceled":
				// Terminal broker verdict: surface it as a typed error carrying
				// the broker's reason text (d.Text) so the runner can persist
				// why the leg failed and budget its retries.
				return 0, false, &OrderRejectedError{
					Status: strings.ToLower(d.Status),
					Reason: d.Text,
				}
			}
		}
		select {
		case <-ctx.Done():
			return 0, false, nil
		case <-time.After(400 * time.Millisecond):
		}
	}
	return 0, false, nil
}

// AvailableEquity reports the account's tradable cash from the RMS funds API,
// preferring free cash and falling back to net funds. Implements FundsProvider.
func (b *LiveBroker) AvailableEquity(ctx context.Context) (float64, error) {
	f, err := b.client.Funds(ctx)
	if err != nil {
		return 0, err
	}
	if f.AvailableCash > 0 {
		return f.AvailableCash, nil
	}
	return f.Net, nil
}

// SpreadMargin prices the broker-side margin for the whole spread as one hedged
// basket, so the recorded figure reflects the real requirement (with hedge
// benefit) rather than the premium debit. Implements MarginProvider.
func (b *LiveBroker) SpreadMargin(ctx context.Context, underlying string, legs []SpreadLeg) (float64, error) {
	positions := make([]angelone.MarginPosition, 0, len(legs))
	for _, l := range legs {
		inst, err := b.scrip.ResolveOption(ctx, underlying, l.Leg.Expiry, l.Leg.Strike, l.Leg.OptionType)
		if err != nil {
			return 0, fmt.Errorf("resolve %s %d %s: %w", underlying, l.Leg.Strike, l.Leg.OptionType, err)
		}
		positions = append(positions, angelone.MarginPosition{
			Exchange:    "NFO",
			SymbolToken: inst.Token,
			Qty:         l.Leg.Qty,
			TradeType:   l.Side,
			ProductType: "CARRYFORWARD",
		})
	}
	res, err := b.client.RequiredMargin(ctx, positions)
	if err != nil {
		return 0, err
	}
	return res.TotalMarginRequired, nil
}
