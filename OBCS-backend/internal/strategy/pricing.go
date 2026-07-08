// Package strategy is a faithful Go port of the Overnight Bull Call Spread
// (OBCS) model from Backtest/simulator.py, extended with the option Greeks the
// live UI needs (delta, gamma, theta, vega, iv).
//
// Conventions match the reference implementation:
//   - Black-Scholes European call pricing with continuous dividend yield q.
//   - A single ATM sigma per trade with a linear volatility skew per leg.
//   - Time is measured in years (T = days / 365).
//
// Greeks are reported per-contract in the natural units traders expect:
//   - vega  : price change per +1 volatility POINT (i.e. per 0.01 of sigma)
//   - theta : price change per calendar DAY (annual theta / 365)
//
// This mirrors how AngelOne / most Indian chains display them.
package strategy

import "math"

const sqrt2 = 1.4142135623730951

// normCDF is the standard normal cumulative distribution function.
func normCDF(x float64) float64 {
	return 0.5 * (1.0 + math.Erf(x/sqrt2))
}

// normPDF is the standard normal probability density function.
func normPDF(x float64) float64 {
	return math.Exp(-0.5*x*x) / math.Sqrt(2.0*math.Pi)
}

// d1d2 returns the Black-Scholes d1 and d2 terms. Callers must guarantee
// T > 0 and sigma > 0.
func d1d2(s, k, t, r, q, sigma float64) (float64, float64) {
	srt := sigma * math.Sqrt(t)
	d1 := (math.Log(s/k) + (r-q+0.5*sigma*sigma)*t) / srt
	return d1, d1 - srt
}

// BSCall prices a European call. At or beyond expiry (or zero vol) it collapses
// to intrinsic value, matching simulator.bs_call.
func BSCall(s, k, t, r, q, sigma float64) float64 {
	if t <= 0 || sigma <= 0 {
		return math.Max(0.0, s-k)
	}
	d1, d2 := d1d2(s, k, t, r, q, sigma)
	return s*math.Exp(-q*t)*normCDF(d1) - k*math.Exp(-r*t)*normCDF(d2)
}

// Greeks holds the standard option sensitivities plus the implied volatility
// used to derive them.
type Greeks struct {
	Price float64 `json:"price"`
	Delta float64 `json:"delta"`
	Gamma float64 `json:"gamma"`
	Theta float64 `json:"theta"` // per calendar day
	Vega  float64 `json:"vega"`  // per +1 vol point (0.01)
	IV    float64 `json:"iv"`    // annualized, as a fraction (0.15 = 15%)
}

// CallGreeks returns price and Greeks for a European call. Vega is scaled to
// one volatility point and theta to one calendar day so the numbers line up
// with a broker option chain.
func CallGreeks(s, k, t, r, q, sigma float64) Greeks {
	if t <= 0 || sigma <= 0 {
		intrinsic := math.Max(0.0, s-k)
		delta := 0.0
		if s > k {
			delta = 1.0
		}
		return Greeks{Price: intrinsic, Delta: delta, IV: math.Max(0, sigma)}
	}
	sqrtT := math.Sqrt(t)
	d1, d2 := d1d2(s, k, t, r, q, sigma)
	pdf := normPDF(d1)
	discQ := math.Exp(-q * t)
	discR := math.Exp(-r * t)

	price := s*discQ*normCDF(d1) - k*discR*normCDF(d2)
	delta := discQ * normCDF(d1)
	gamma := discQ * pdf / (s * sigma * sqrtT)
	vegaAnnual := s * discQ * pdf * sqrtT
	thetaAnnual := -(s*discQ*pdf*sigma)/(2*sqrtT) -
		r*k*discR*normCDF(d2) + q*s*discQ*normCDF(d1)

	return Greeks{
		Price: price,
		Delta: delta,
		Gamma: gamma,
		Theta: thetaAnnual / 365.0, // per calendar day
		Vega:  vegaAnnual / 100.0,  // per +1 vol point
		IV:    sigma,
	}
}

// LegSigma applies the linear volatility smile: skewPts vol POINTS are added
// per +1% of moneyness. Equity-index call wings usually trade slightly below
// ATM, so a mildly negative skew raises the net debit. Mirrors
// simulator.leg_sigma; floored at 0.02 to stay numerically sane.
func LegSigma(sigmaATM, s, k, skewPts float64) float64 {
	moneyPct := (k/s - 1.0) * 100.0
	return math.Max(0.02, sigmaATM+skewPts*moneyPct/100.0)
}

// SpreadLegs prices the long (k1) and short (k2) call legs of a bull call
// spread at the per-leg skew-adjusted volatility.
func SpreadLegs(s, k1, k2, t, r, q, sigmaATM, skewPts float64) (c1, c2 float64) {
	c1 = BSCall(s, k1, t, r, q, LegSigma(sigmaATM, s, k1, skewPts))
	c2 = BSCall(s, k2, t, r, q, LegSigma(sigmaATM, s, k2, skewPts))
	return c1, c2
}

// SpreadValue is the net premium of the debit spread (long k1 minus short k2).
func SpreadValue(s, k1, k2, t, r, q, sigmaATM, skewPts float64) float64 {
	c1, c2 := SpreadLegs(s, k1, k2, t, r, q, sigmaATM, skewPts)
	return c1 - c2
}

// ImpliedVol solves for the Black-Scholes volatility that reproduces an
// observed call price, used to derive IV from a live broker quote. It uses a
// bracketed bisection which is slower than Newton but cannot diverge.
// Returns (iv, ok); ok is false when the price is outside the no-arbitrage band.
func ImpliedVol(price, s, k, t, r, q float64) (float64, bool) {
	if t <= 0 || price <= 0 {
		return 0, false
	}
	intrinsic := math.Max(0.0, s*math.Exp(-q*t)-k*math.Exp(-r*t))
	upper := s * math.Exp(-q*t) // call price is bounded above by discounted spot
	if price <= intrinsic || price >= upper {
		return 0, false
	}
	const hiBound = 5.0
	lo, hi := 1e-4, hiBound
	for i := 0; i < 100; i++ {
		mid := 0.5 * (lo + hi)
		if BSCall(s, k, t, r, q, mid) > price {
			hi = mid
		} else {
			lo = mid
		}
		if hi-lo < 1e-6 {
			break
		}
	}
	iv := 0.5 * (lo + hi)
	// If the root pinned to the upper bracket the true IV exceeds 500%; treat
	// as non-convergence rather than returning a saturated value.
	if iv >= hiBound-1e-3 {
		return 0, false
	}
	return iv, true
}

// RoundToStep rounds a price to the nearest strike step using conventional
// half-up rounding (not banker's rounding), matching simulator.round_to_step.
func RoundToStep(price float64, step int) int {
	return int(math.Floor(price/float64(step)+0.5)) * step
}
