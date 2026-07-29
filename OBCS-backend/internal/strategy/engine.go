package strategy

import (
	"math"
	"time"
)

// Params are the resolved strategy inputs for one live session. They map 1:1
// from config.StrategyConfig so the live engine and the backtest share
// behaviour.
type Params struct {
	Underlying     string
	LotSize        int
	Lots           int
	StrikeStep     int
	StrikeDistPct  float64
	DTETarget      int
	RiskFreeRate   float64
	DivYield       float64
	HVWindow       int
	UseDynamicHV   bool
	FixedIV        float64
	IVMult         float64
	IVAdd          float64
	SkewPts        float64
	UseEMAFilter   bool
	EMAPeriod      int
	UseExpiryCal   bool
	ExpiryWeekday  string
	InitialCapital float64

	UseAGC    bool
	KellyMult float64
	AGCWindow int
	MaxLots   int

	// PremiumMarginMult estimates the capital blocked per lot as a multiple of
	// the premium debit when ComputeEntry is given no broker margin figure
	// (paper mode, preview, margin API failure). Values below 1 are treated as
	// 1: a debit spread never blocks less than its premium.
	PremiumMarginMult float64

	Costs CostParams
}

// SpreadGreeks bundles per-leg and net Greeks for the bull call spread.
type SpreadGreeks struct {
	Long  Greeks `json:"long"`  // bought K1 call
	Short Greeks `json:"short"` // sold K2 call
	Net   Greeks `json:"net"`   // long minus short
}

// netGreeks derives the spread-level (long minus short) sensitivities.
func netGreeks(long, short Greeks) Greeks {
	return Greeks{
		Price: long.Price - short.Price,
		Delta: long.Delta - short.Delta,
		Gamma: long.Gamma - short.Gamma,
		Theta: long.Theta - short.Theta,
		Vega:  long.Vega - short.Vega,
		// A spread does not have one IV; expose the long-leg IV as reference.
		IV: long.IV,
	}
}

// Entry rejection reasons. The sizing gates depend on the capital-per-lot
// denominator; the hard gates do not (see EntryPlan.SizingFailure).
const (
	reasonEMAFilter     = "ema_filter: spot below EMA"
	reasonBadDebit      = "non-positive entry debit"
	reasonAffordability = "affordability: equity cannot fund one lot"
	reasonKellyZero     = "kelly_zero"
	reasonSizeZero      = "size_zero"
)

// EntryPlan is the fully-specified overnight trade the engine wants to open.
type EntryPlan struct {
	ShouldEnter bool      `json:"should_enter"`
	Reason      string    `json:"reason,omitempty"`
	EntrySpot   float64   `json:"entry_spot"`
	K1          int       `json:"k1"`
	K2          int       `json:"k2"`
	DTE         int       `json:"dte_days"`
	Expiry      time.Time `json:"expiry"`
	SigmaATM    float64   `json:"sigma_atm"`
	C1Exec      float64   `json:"c1_exec"`       // slipped long-leg premium (points)
	C2Exec      float64   `json:"c2_exec"`       // slipped short-leg premium (points)
	EntryDebit  float64   `json:"entry_debit"`   // net premium in points
	DebitPerLot float64   `json:"debit_per_lot"` // rupees of premium per lot
	// CapitalPerLot is the rupee capital blocked per lot — the broker's margin
	// figure when one was supplied, else the premium-multiple fallback. It is
	// the denominator for the affordability gate and Kelly sizing.
	CapitalPerLot float64      `json:"capital_per_lot"`
	Lots          int          `json:"lots"`
	MarginUsed    float64      `json:"margin_used"` // CapitalPerLot * Lots, rupees
	KellyF        float64      `json:"kelly_f"`
	Greeks        SpreadGreeks `json:"greeks"` // model (computed) Greeks at entry
}

// SizingFailure reports whether a rejected plan fell only to a sizing gate
// (affordability, Kelly) — gates whose outcome depends on the capital-per-lot
// denominator, so re-running with the broker's real margin figure is
// meaningful. Hard gates (trend filter, non-positive debit) return false: no
// margin input can revive those, and their plans may lack strikes entirely.
func (p EntryPlan) SizingFailure() bool {
	switch p.Reason {
	case reasonAffordability, reasonKellyZero, reasonSizeZero:
		return true
	}
	return false
}

// ExitResult is the realized outcome of closing an overnight spread.
type ExitResult struct {
	ExitSpot   float64      `json:"exit_spot"`
	C1Exec     float64      `json:"c1_exec"`
	C2Exec     float64      `json:"c2_exec"`
	ExitValue  float64      `json:"exit_value"`
	GrossPnL   float64      `json:"gross_pnl"`
	Costs      float64      `json:"costs"`
	NetPnL     float64      `json:"net_pnl"`
	ReturnRisk float64      `json:"return_on_risk"`
	Greeks     SpreadGreeks `json:"greeks"` // model Greeks at exit
}

// Engine evaluates the OBCS strategy for live/paper trading. It is stateless;
// all account state (equity, recent returns) is passed in per call so the same
// engine can serve the runner and the on-demand "computed Greeks" endpoint.
type Engine struct {
	p Params
}

// NewEngine constructs an Engine from resolved parameters.
func NewEngine(p Params) *Engine { return &Engine{p: p} }

// Params exposes the engine's parameters (read-only use).
func (e *Engine) Params() Params { return e.p }

// sigmaForEntry resolves the ATM pricing volatility from recent closes.
func (e *Engine) sigmaForEntry(closes []float64) float64 {
	var sigma float64
	if e.p.UseDynamicHV {
		hv := HistoricalVol(closes, e.p.HVWindow)
		sigma = hv*e.p.IVMult + e.p.IVAdd
	} else {
		sigma = e.p.FixedIV
	}
	return ClampSigma(sigma)
}

// strikes selects the long (ATM) and short (OTM) strikes for a given spot.
func (e *Engine) strikes(spot float64) (k1, k2 int) {
	k1 = RoundToStep(spot, e.p.StrikeStep)
	k2 = RoundToStep(spot*(1+e.p.StrikeDistPct/100.0), e.p.StrikeStep)
	if k2 < k1+e.p.StrikeStep {
		k2 = k1 + e.p.StrikeStep
	}
	return k1, k2
}

// dte resolves the effective days-to-expiry for an entry date.
func (e *Engine) dte(entry time.Time) int {
	if e.p.UseExpiryCal {
		return PickExpiryDTE(entry, e.p.DTETarget, e.p.ExpiryWeekday, 2, 10)
	}
	return e.p.DTETarget
}

// slippage returns the per-leg slippage in index points (0 when costs are off).
func (e *Engine) slippage() float64 {
	if e.p.Costs.Enable {
		return e.p.Costs.SlippagePts
	}
	return 0.0
}

// ComputeEntry builds the trade plan at the entry window. `closes` is the
// recent daily close series (most recent last), `spot` the current entry price,
// `equity` the available capital and `aboveEMA` the trend-filter state.
// `marginPerLot` is the broker's margin requirement for ONE lot of the
// candidate spread (rupees); pass 0 when unknown and the premium-multiple
// fallback estimates it instead. The affordability gate and Kelly sizing both
// divide by that capital-per-lot figure, so lots are sized against the rupees
// the account actually loses access to, not just the premium. With
// marginPerLot=0 and PremiumMarginMult<=1 this reproduces the premium-based
// per-bar entry logic of simulator.run_backtest exactly.
func (e *Engine) ComputeEntry(closes []float64, spot, equity float64, aboveEMA bool, recentReturns []float64, entryDate time.Time, marginPerLot float64) EntryPlan {
	plan := EntryPlan{EntrySpot: spot}

	if e.p.UseEMAFilter && !aboveEMA {
		plan.Reason = reasonEMAFilter
		return plan
	}

	sigmaATM := e.sigmaForEntry(closes)
	plan.SigmaATM = sigmaATM

	k1, k2 := e.strikes(spot)
	plan.K1, plan.K2 = k1, k2

	dte := e.dte(entryDate)
	plan.DTE = dte
	plan.Expiry = ExpiryDate(entryDate, dte)
	tIn := float64(dte) / 365.0

	slip := e.slippage()
	c1, c2 := SpreadLegs(spot, float64(k1), float64(k2), tIn,
		e.p.RiskFreeRate, e.p.DivYield, sigmaATM, e.p.SkewPts)
	// Buy the long leg worse (pay slip), sell the short leg worse (receive less).
	c1Exec := c1 + slip
	c2Exec := math.Max(0.0, c2-slip)
	entryDebit := c1Exec - c2Exec
	plan.C1Exec, plan.C2Exec, plan.EntryDebit = c1Exec, c2Exec, entryDebit

	if entryDebit <= 0 {
		plan.Reason = reasonBadDebit
		return plan
	}

	debitPerLot := entryDebit * float64(e.p.LotSize)
	plan.DebitPerLot = debitPerLot

	capPerLot := marginPerLot
	if capPerLot <= 0 {
		capPerLot = debitPerLot * math.Max(1.0, e.p.PremiumMarginMult)
	}
	plan.CapitalPerLot = capPerLot

	affordable := AffordableLots(equity, capPerLot)
	if affordable < 1 {
		plan.Reason = reasonAffordability
		return plan
	}

	desired := e.p.Lots
	agcLive := e.p.UseAGC && len(recentReturns) >= e.p.AGCWindow
	if agcLive {
		window := recentReturns
		if len(window) > e.p.AGCWindow {
			window = window[len(window)-e.p.AGCWindow:]
		}
		kf := KellyFraction(window, e.p.KellyMult)
		plan.KellyF = kf
		desired = LotsFromFraction(kf, equity, capPerLot)
	}
	nLots := SizeLots(desired, e.p.MaxLots, affordable)
	if nLots < 1 {
		if agcLive && plan.KellyF <= 0 {
			plan.Reason = reasonKellyZero
		} else {
			plan.Reason = reasonSizeZero
		}
		return plan
	}
	plan.Lots = nLots
	plan.MarginUsed = capPerLot * float64(nLots)

	plan.Greeks = e.GreeksAt(spot, float64(k1), float64(k2), tIn, sigmaATM)
	plan.ShouldEnter = true
	return plan
}

// GreeksAt returns the model (computed) spread Greeks at a given spot, strikes,
// time-to-expiry and ATM volatility, using the per-leg skew.
func (e *Engine) GreeksAt(spot, k1, k2, t, sigmaATM float64) SpreadGreeks {
	long := CallGreeks(spot, k1, t, e.p.RiskFreeRate, e.p.DivYield, LegSigma(sigmaATM, spot, k1, e.p.SkewPts))
	short := CallGreeks(spot, k2, t, e.p.RiskFreeRate, e.p.DivYield, LegSigma(sigmaATM, spot, k2, e.p.SkewPts))
	return SpreadGreeks{Long: long, Short: short, Net: netGreeks(long, short)}
}

// ComputeExit values the spread at the exit window for the paper broker and for
// the model-vs-live comparison. `elapsedDays` is the fractional day count held
// (see OvernightGapDays). Mirrors the exit valuation in simulator.run_backtest.
func (e *Engine) ComputeExit(plan EntryPlan, exitSpot, elapsedDays float64) ExitResult {
	tOut := math.Max(1e-4, (float64(plan.DTE)-elapsedDays)/365.0)
	slip := e.slippage()

	c1, c2 := SpreadLegs(exitSpot, float64(plan.K1), float64(plan.K2), tOut,
		e.p.RiskFreeRate, e.p.DivYield, plan.SigmaATM, e.p.SkewPts)
	// Sell the long leg worse, buy back the short leg worse.
	c1Exec := math.Max(0.0, c1-slip)
	c2Exec := c2 + slip
	exitValue := c1Exec - c2Exec

	costs := TradeCashCosts(plan.C1Exec, plan.C2Exec, c1Exec, c2Exec,
		e.p.LotSize, plan.Lots, e.p.Costs)
	gross := (exitValue - plan.EntryDebit) * float64(e.p.LotSize) * float64(plan.Lots)
	net := gross - costs

	// Normalize on the same capital base the entry was sized against, so this
	// return is directly comparable to the Kelly window (db.RecentReturns).
	capPerLot := plan.CapitalPerLot
	if capPerLot <= 0 {
		capPerLot = plan.DebitPerLot
	}
	retRisk := 0.0
	if capPerLot > 0 && plan.Lots > 0 {
		retRisk = (net / float64(plan.Lots)) / capPerLot
	}

	return ExitResult{
		ExitSpot:   exitSpot,
		C1Exec:     c1Exec,
		C2Exec:     c2Exec,
		ExitValue:  exitValue,
		GrossPnL:   gross,
		Costs:      costs,
		NetPnL:     net,
		ReturnRisk: retRisk,
		Greeks:     e.GreeksAt(exitSpot, float64(plan.K1), float64(plan.K2), tOut, plan.SigmaATM),
	}
}

// LiveGreeks derives spread Greeks from observed live leg premiums by backing
// out each leg's implied volatility. Used to store the live option snapshot
// after a real execution. Falls back to the model sigma when a premium is
// outside the no-arbitrage band.
func (e *Engine) LiveGreeks(spot, k1, k2, t, longPrem, shortPrem, fallbackSigma float64) SpreadGreeks {
	longIV, ok := ImpliedVol(longPrem, spot, k1, t, e.p.RiskFreeRate, e.p.DivYield)
	if !ok {
		longIV = LegSigma(fallbackSigma, spot, k1, e.p.SkewPts)
	}
	shortIV, ok := ImpliedVol(shortPrem, spot, k2, t, e.p.RiskFreeRate, e.p.DivYield)
	if !ok {
		shortIV = LegSigma(fallbackSigma, spot, k2, e.p.SkewPts)
	}
	long := CallGreeks(spot, k1, t, e.p.RiskFreeRate, e.p.DivYield, longIV)
	short := CallGreeks(spot, k2, t, e.p.RiskFreeRate, e.p.DivYield, shortIV)
	// Override prices with the observed live premiums.
	long.Price, short.Price = longPrem, shortPrem
	return SpreadGreeks{Long: long, Short: short, Net: netGreeks(long, short)}
}

// istZone is the fixed +05:30 exchange timezone used for calendar-date maths so
// the day count is independent of how timestamps were stored (UTC).
var istZone = time.FixedZone("IST", 5*3600+30*60)

// FractionalElapsed returns the fractional day count for an overnight hold,
// charging OvernightGapDays of intraday decay. It counts CALENDAR days between
// the entry and exit dates (so a Fri->Mon weekend hold is 3 days, matching
// simulator.run_backtest's (exit_date - entry_date).days), then subtracts the
// 6h15m intraday gap: 1 day -> 0.740, 3 days -> 2.740.
func FractionalElapsed(entry, exit time.Time) float64 {
	e := entry.In(istZone)
	x := exit.In(istZone)
	ed := time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, istZone)
	xd := time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, istZone)
	calDays := int(xd.Sub(ed).Hours()/24.0 + 0.5)
	if calDays < 1 {
		calDays = 1
	}
	elapsed := float64(calDays) - OvernightGapDays
	if elapsed < 0.05 {
		elapsed = 0.05
	}
	return elapsed
}
