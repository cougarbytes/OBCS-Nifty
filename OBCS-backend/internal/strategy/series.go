package strategy

import "math"

// annualizationFactor is the trading-day count used to annualize daily
// volatility (sqrt(252)).
var annualizationFactor = math.Sqrt(252.0)

// HistoricalVol computes annualized close-to-close historical volatility over a
// rolling window, matching simulator.compute_hv. It returns the most recent HV
// value; when there is insufficient data it falls back to 0.15 (15%).
func HistoricalVol(closes []float64, window int) float64 {
	if window < 2 || len(closes) <= window {
		return 0.15
	}
	// Log returns of the last `window` observations.
	logRets := make([]float64, 0, window)
	start := len(closes) - window
	for i := start; i < len(closes); i++ {
		prev := closes[i-1]
		if prev <= 0 || closes[i] <= 0 {
			return 0.15
		}
		logRets = append(logRets, math.Log(closes[i]/prev))
	}
	var sum float64
	for _, r := range logRets {
		sum += r
	}
	mean := sum / float64(len(logRets))
	var ss float64
	for _, r := range logRets {
		d := r - mean
		ss += d * d
	}
	std := math.Sqrt(ss / float64(len(logRets)-1))
	hv := std * annualizationFactor
	if hv <= 0 || math.IsNaN(hv) {
		return 0.15
	}
	return hv
}

// EMA computes the exponential moving average with span `period`
// (alpha = 2/(period+1), adjust=false), matching pandas ewm used in the
// simulator. It returns the final EMA value.
func EMA(closes []float64, period int) float64 {
	if len(closes) == 0 {
		return 0
	}
	if period < 1 {
		period = 1
	}
	alpha := 2.0 / (float64(period) + 1.0)
	ema := closes[0]
	for i := 1; i < len(closes); i++ {
		ema = alpha*closes[i] + (1-alpha)*ema
	}
	return ema
}

// ClampSigma bounds the pricing volatility to a sane range, matching the
// min(1.50, max(0.03, sigma)) guard in simulator.run_backtest.
func ClampSigma(sigma float64) float64 {
	if sigma < 0.03 {
		return 0.03
	}
	if sigma > 1.50 {
		return 1.50
	}
	return sigma
}
