package broker

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/obcs-nifty/backend/internal/config"
)

// PaperBroker simulates fills at the strategy's model price. It is the default
// and never touches a live order API, so it is safe to run unattended.
type PaperBroker struct{}

// NewPaperBroker constructs a paper broker.
func NewPaperBroker() *PaperBroker { return &PaperBroker{} }

// Mode reports paper mode.
func (b *PaperBroker) Mode() config.TradingMode { return config.ModePaper }

// Buy fills at the model price.
func (b *PaperBroker) Buy(_ context.Context, _ string, leg Leg) (float64, string, error) {
	return leg.ModelPrice, paperRef("BUY"), nil
}

// Sell fills at the model price.
func (b *PaperBroker) Sell(_ context.Context, _ string, leg Leg) (float64, string, error) {
	return leg.ModelPrice, paperRef("SELL"), nil
}

func paperRef(side string) string {
	return fmt.Sprintf("PAPER-%s-%s", side, uuid.NewString()[:8])
}
