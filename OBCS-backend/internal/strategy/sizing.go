package strategy

import "math"

// kellyFCap is the absolute ceiling on the capital fraction at risk. Mirrors
// simulator._KELLY_F_CAP.
const kellyFCap = 0.60

// KellyFraction estimates the Kelly bet fraction for continuous outcomes using
// f* = E[r]/Var[r], where r is net P&L per rupee of capital blocked for the
// trade (broker margin in live mode, premium debit in paper mode — see
// db.RecentReturns). It is scaled by kellyMult and hard-capped. Returns 0 when
// the estimated edge is non-positive or the sample is too small. Mirrors
// simulator.kelly_fraction, whose capital base is the premium.
//
// On short windows this estimator is noisy; the cap and the affordability gate
// (see AffordableLots) are what bound the damage estimation error can do.
func KellyFraction(returnsOnCapital []float64, kellyMult float64) float64 {
	if len(returnsOnCapital) < 5 {
		return 0.0
	}
	var sum float64
	for _, r := range returnsOnCapital {
		sum += r
	}
	mean := sum / float64(len(returnsOnCapital))

	var ss float64
	for _, r := range returnsOnCapital {
		d := r - mean
		ss += d * d
	}
	variance := ss / float64(len(returnsOnCapital)-1) // sample variance (ddof=1)

	if variance <= 1e-8 || mean <= 0 {
		return 0.0
	}
	f := kellyMult * mean / variance
	if f > kellyFCap {
		return kellyFCap
	}
	return f
}

// AffordableLots is the affordability gate: the number of lots whose combined
// capital requirement can be funded from the available equity. Mirrors the
// equity // (debit * lot_size) cap in simulator.run_backtest, where the
// capital per lot is the premium debit.
func AffordableLots(equity, capitalPerLot float64) int {
	if capitalPerLot <= 0 {
		return 0
	}
	return int(equity / capitalPerLot)
}

// LotsFromFraction is the desired lot count from the Kelly fraction, rounded
// to NEAREST. Truncation under-bets above one lot and, once floored, over-bets
// below it: expected log-growth is a downward parabola centred on the exact
// desired count, so the nearer integer wins. No floors — 0 means take no
// position. Mirrors simulator.lots_from_fraction.
func LotsFromFraction(kellyF, equity, capitalPerLot float64) int {
	if kellyF <= 0 || equity <= 0 || capitalPerLot <= 0 {
		return 0
	}
	// math.Round is half-away-from-zero; the guard keeps the argument strictly
	// positive, where that equals the simulator's half-up floor(x+0.5).
	return int(math.Round(kellyF * equity / capitalPerLot))
}

// SizeLots clamps the desired lot count to the configured maximum and the
// affordability gate; 0 is never promoted to 1 — a zero-lot signal means take
// no position. Mirrors simulator.size_lots.
func SizeLots(desired, maxLots, affordable int) int {
	return max(0, min(desired, maxLots, affordable))
}
