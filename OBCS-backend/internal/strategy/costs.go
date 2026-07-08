package strategy

import "math"

// CostParams captures the Indian retail option round-trip cost stack. Rates are
// percentages (e.g. STTPct = 0.10 means 0.10%). Mirrors simulator COST_DEFAULTS.
type CostParams struct {
	Enable            bool
	SlippagePts       float64
	BrokeragePerOrder float64
	STTPct            float64
	ExchPct           float64
	GSTPct            float64
	StampPct          float64
}

// TradeCashCosts computes the total cash charges for a four-leg round trip
// (buy K1 / sell K2 at entry, sell K1 / buy K2 at exit). The premiums passed in
// are the already-slipped executed prices. Mirrors simulator.trade_cash_costs.
func TradeCashCosts(c1In, c2In, c1Out, c2Out float64, lotSize, nLots int, p CostParams) float64 {
	if !p.Enable {
		return 0.0
	}
	qty := float64(lotSize * nLots)
	sellPrem := math.Max(0.0, c2In+c1Out) * qty // premium received on sells
	buyPrem := math.Max(0.0, c1In+c2Out) * qty  // premium paid on buys
	turnover := sellPrem + buyPrem

	brk := 4.0 * p.BrokeragePerOrder
	stt := p.STTPct / 100.0 * sellPrem
	exch := p.ExchPct / 100.0 * turnover
	gst := p.GSTPct / 100.0 * (brk + exch)
	stamp := p.StampPct / 100.0 * buyPrem
	return brk + stt + exch + gst + stamp
}
