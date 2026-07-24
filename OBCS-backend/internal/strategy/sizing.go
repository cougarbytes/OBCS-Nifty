package strategy

import "math"

// kellyFCap is the absolute ceiling on the capital fraction at risk. Mirrors
// simulator._KELLY_F_CAP.
const kellyFCap = 0.60

// KellyFraction estimates the Kelly bet fraction for continuous outcomes using
// f* = E[r]/Var[r], where r is net P&L per rupee of premium at risk. It is
// scaled by kellyMult and hard-capped. Returns 0 when the estimated edge is
// non-positive or the sample is too small. Mirrors simulator.kelly_fraction.
//
// On short windows this estimator is noisy; the cap and the affordability gate
// (see AffordableLots) are what bound the damage estimation error can do.
func KellyFraction(returnsOnPremium []float64, kellyMult float64) float64 {
	if len(returnsOnPremium) < 5 {
		return 0.0
	}
	var sum float64
	for _, r := range returnsOnPremium {
		sum += r
	}
	mean := sum / float64(len(returnsOnPremium))

	var ss float64
	for _, r := range returnsOnPremium {
		d := r - mean
		ss += d * d
	}
	variance := ss / float64(len(returnsOnPremium)-1) // sample variance (ddof=1)

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
// premium debit can be funded from the available equity. Mirrors the
// equity // (debit * lot_size) cap in simulator.run_backtest.
func AffordableLots(equity, debitPerLot float64) int {
	if debitPerLot <= 0 {
		return 0
	}
	return int(equity / debitPerLot)
}

// LotsFromFraction is the desired lot count from the Kelly fraction, rounded
// to NEAREST. Truncation under-bets above one lot and, once floored, over-bets
// below it: expected log-growth is a downward parabola centred on the exact
// desired count, so the nearer integer wins. No floors — 0 means take no
// position. Mirrors simulator.lots_from_fraction.
func LotsFromFraction(kellyF, equity, debitPerLot float64) int {
	if kellyF <= 0 || equity <= 0 || debitPerLot <= 0 {
		return 0
	}
	// math.Round is half-away-from-zero; the guard keeps the argument strictly
	// positive, where that equals the simulator's half-up floor(x+0.5).
	return int(math.Round(kellyF * equity / debitPerLot))
}

// SizeLots clamps the desired lot count to the configured maximum and the
// affordability gate; 0 is never promoted to 1 — a zero-lot signal means take
// no position. Mirrors simulator.size_lots.
func SizeLots(desired, maxLots, affordable int) int {
	return max(0, min(desired, maxLots, affordable))
}
