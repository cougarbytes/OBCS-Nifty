package strategy

import (
	"math"
	"testing"
	"time"
)

// testParams is a minimal fixed-IV configuration so entry pricing is
// deterministic without a close series.
func testParams() Params {
	return Params{
		Underlying:    "NIFTY",
		LotSize:       75,
		Lots:          6,
		StrikeStep:    50,
		StrikeDistPct: 1.0,
		DTETarget:     14,
		RiskFreeRate:  0.06,
		DivYield:      0.0125,
		UseDynamicHV:  false,
		FixedIV:       0.15,
		MaxLots:       10,
	}
}

var testEntryDate = time.Date(2026, 7, 28, 15, 20, 0, 0, time.UTC)

// TestComputeEntryMarginDenominator verifies that a broker margin figure, not
// the premium debit, drives the affordability gate and the recorded capital.
func TestComputeEntryMarginDenominator(t *testing.T) {
	e := NewEngine(testParams())
	equity := 100_000.0

	plan := e.ComputeEntry(nil, 24000, equity, true, nil, testEntryDate, 40_000)
	if !plan.ShouldEnter {
		t.Fatalf("expected entry, got reason %q", plan.Reason)
	}
	if plan.CapitalPerLot != 40_000 {
		t.Errorf("CapitalPerLot = %v, want broker margin 40000", plan.CapitalPerLot)
	}
	// equity/marginPerLot affords 2 lots; the premium debit would afford more.
	if plan.Lots != 2 {
		t.Errorf("Lots = %d, want 2 (affordability on margin)", plan.Lots)
	}
	if plan.MarginUsed != 80_000 {
		t.Errorf("MarginUsed = %v, want CapitalPerLot*Lots = 80000", plan.MarginUsed)
	}

	prem := e.ComputeEntry(nil, 24000, equity, true, nil, testEntryDate, 0)
	if prem.DebitPerLot <= 0 || prem.DebitPerLot >= 16_000 {
		t.Fatalf("unexpected DebitPerLot %v; premium-based case not meaningful", prem.DebitPerLot)
	}
	if prem.Lots <= plan.Lots {
		t.Errorf("premium-based Lots = %d, want more than margin-based %d", prem.Lots, plan.Lots)
	}
}

// TestComputeEntryPremiumFallback verifies the capital base when no broker
// margin is supplied: the premium debit scaled by PremiumMarginMult, with
// sub-1 multipliers clamped (a debit spread never blocks less than premium).
func TestComputeEntryPremiumFallback(t *testing.T) {
	cases := []struct {
		name string
		mult float64
		want float64 // CapitalPerLot as multiple of DebitPerLot
	}{
		{"unset mult defaults to premium", 0, 1},
		{"mult scales the fallback", 3, 3},
		{"sub-1 mult clamps to premium", 0.5, 1},
	}
	for _, c := range cases {
		p := testParams()
		p.PremiumMarginMult = c.mult
		plan := NewEngine(p).ComputeEntry(nil, 24000, 1_000_000, true, nil, testEntryDate, 0)
		if !plan.ShouldEnter {
			t.Fatalf("%s: expected entry, got reason %q", c.name, plan.Reason)
		}
		want := plan.DebitPerLot * c.want
		if math.Abs(plan.CapitalPerLot-want) > 1e-9 {
			t.Errorf("%s: CapitalPerLot = %v, want %v", c.name, plan.CapitalPerLot, want)
		}
	}
}

// TestComputeEntryKellyUsesCapital verifies the Kelly lot division runs on the
// margin-based capital per lot, not the premium.
func TestComputeEntryKellyUsesCapital(t *testing.T) {
	p := testParams()
	p.UseAGC = true
	p.KellyMult = 0.5
	p.AGCWindow = 5
	// Strong positive edge: f = 0.5*mean/var far exceeds the 0.60 cap.
	recent := []float64{0.10, 0.20, -0.05, 0.15, 0.10}

	plan := NewEngine(p).ComputeEntry(nil, 24000, 200_000, true, recent, testEntryDate, 40_000)
	if !plan.ShouldEnter {
		t.Fatalf("expected entry, got reason %q", plan.Reason)
	}
	if plan.KellyF != kellyFCap {
		t.Errorf("KellyF = %v, want capped %v", plan.KellyF, kellyFCap)
	}
	// round(0.6 * 200000 / 40000) = 3 lots.
	if plan.Lots != 3 {
		t.Errorf("Lots = %d, want 3 (Kelly on margin capital)", plan.Lots)
	}
}

// TestSizingFailure verifies the sizing/hard gate classification the runner
// uses to decide whether fetching a broker margin figure could change the
// outcome.
func TestSizingFailure(t *testing.T) {
	// Hard gate: EMA filter — plan carries no strikes, margin cannot help.
	pEMA := testParams()
	pEMA.UseEMAFilter = true
	ema := NewEngine(pEMA).ComputeEntry(nil, 24000, 100_000, false, nil, testEntryDate, 0)
	if ema.ShouldEnter || ema.SizingFailure() {
		t.Errorf("EMA skip: ShouldEnter=%v SizingFailure()=%v, want false/false (reason %q)",
			ema.ShouldEnter, ema.SizingFailure(), ema.Reason)
	}

	// Sizing gate: equity cannot fund one lot.
	afford := NewEngine(testParams()).ComputeEntry(nil, 24000, 1_000, true, nil, testEntryDate, 0)
	if afford.ShouldEnter || !afford.SizingFailure() {
		t.Errorf("affordability: ShouldEnter=%v SizingFailure()=%v, want false/true (reason %q)",
			afford.ShouldEnter, afford.SizingFailure(), afford.Reason)
	}

	// Sizing gate: Kelly sees no edge.
	pK := testParams()
	pK.UseAGC = true
	pK.KellyMult = 0.5
	pK.AGCWindow = 5
	losing := []float64{-0.10, -0.12, -0.08, -0.11, -0.09}
	kz := NewEngine(pK).ComputeEntry(nil, 24000, 100_000, true, losing, testEntryDate, 0)
	if kz.ShouldEnter || !kz.SizingFailure() {
		t.Errorf("kelly zero: ShouldEnter=%v SizingFailure()=%v, want false/true (reason %q)",
			kz.ShouldEnter, kz.SizingFailure(), kz.Reason)
	}

	// An accepted plan is not a sizing failure.
	ok := NewEngine(testParams()).ComputeEntry(nil, 24000, 1_000_000, true, nil, testEntryDate, 0)
	if !ok.ShouldEnter || ok.SizingFailure() {
		t.Errorf("accepted plan: ShouldEnter=%v SizingFailure()=%v, want true/false (reason %q)",
			ok.ShouldEnter, ok.SizingFailure(), ok.Reason)
	}
}

// TestComputeExitReturnOnCapital verifies the realized return is normalized on
// the capital the entry was sized against (falling back to premium when the
// plan predates the capital base).
func TestComputeExitReturnOnCapital(t *testing.T) {
	e := NewEngine(testParams())
	plan := e.ComputeEntry(nil, 24000, 200_000, true, nil, testEntryDate, 40_000)
	if !plan.ShouldEnter {
		t.Fatalf("expected entry, got reason %q", plan.Reason)
	}

	res := e.ComputeExit(plan, 24100, 1.0)
	want := (res.NetPnL / float64(plan.Lots)) / plan.CapitalPerLot
	if math.Abs(res.ReturnRisk-want) > 1e-12 {
		t.Errorf("ReturnRisk = %v, want %v (net per lot over CapitalPerLot)", res.ReturnRisk, want)
	}

	// Legacy plan without CapitalPerLot: premium base.
	legacy := plan
	legacy.CapitalPerLot = 0
	resL := e.ComputeExit(legacy, 24100, 1.0)
	wantL := (resL.NetPnL / float64(legacy.Lots)) / legacy.DebitPerLot
	if math.Abs(resL.ReturnRisk-wantL) > 1e-12 {
		t.Errorf("legacy ReturnRisk = %v, want %v (premium fallback)", resL.ReturnRisk, wantL)
	}
}
