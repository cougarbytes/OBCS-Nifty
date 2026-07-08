package strategy

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

// SizeLots resolves the final lot count from the sizing inputs, clamping to the
// affordability gate and the configured maximum, with a floor of one lot.
func SizeLots(desired, maxLots, affordable int) int {
	n := desired
	if n < 1 {
		n = 1
	}
	if n > maxLots {
		n = maxLots
	}
	if n > affordable {
		n = affordable
	}
	if n < 1 {
		n = 1
	}
	return n
}
