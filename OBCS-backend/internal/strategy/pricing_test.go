package strategy

import (
	"math"
	"testing"
	"time"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// TestBSCallKnownValue checks the classic textbook case:
// S=100, K=100, T=1, r=5%, q=0, sigma=20% -> call ~= 10.4506.
func TestBSCallKnownValue(t *testing.T) {
	got := BSCall(100, 100, 1, 0.05, 0.0, 0.20)
	if !approx(got, 10.4506, 1e-3) {
		t.Fatalf("BSCall = %.4f, want ~10.4506", got)
	}
}

// TestPutCallConsistencyGreeks verifies delta is in (0,1) and gamma/vega are
// positive for an ATM call.
func TestGreeksSanity(t *testing.T) {
	g := CallGreeks(100, 100, 1, 0.05, 0.0, 0.20)
	if g.Delta <= 0 || g.Delta >= 1 {
		t.Fatalf("delta out of range: %.4f", g.Delta)
	}
	if g.Gamma <= 0 {
		t.Fatalf("gamma must be positive: %.6f", g.Gamma)
	}
	if g.Vega <= 0 {
		t.Fatalf("vega must be positive: %.6f", g.Vega)
	}
	if g.Theta >= 0 {
		t.Fatalf("call theta should be negative: %.6f", g.Theta)
	}
}

// TestImpliedVolRoundTrip prices a call then recovers the vol.
func TestImpliedVolRoundTrip(t *testing.T) {
	price := BSCall(100, 105, 0.5, 0.06, 0.01, 0.25)
	iv, ok := ImpliedVol(price, 100, 105, 0.5, 0.06, 0.01)
	if !ok {
		t.Fatal("ImpliedVol failed to converge")
	}
	if !approx(iv, 0.25, 1e-3) {
		t.Fatalf("recovered iv = %.4f, want ~0.25", iv)
	}
}

// TestRoundToStepHalfUp confirms conventional half-up rounding.
func TestRoundToStepHalfUp(t *testing.T) {
	if got := RoundToStep(24975, 50); got != 25000 { // 499.5 -> half-up -> 500
		t.Fatalf("RoundToStep(24975,50) = %d, want 25000", got)
	}
	if got := RoundToStep(24974, 50); got != 24950 { // 499.48 -> 499
		t.Fatalf("RoundToStep(24974,50) = %d, want 24950", got)
	}
}

// TestPickExpiryDTETuesday checks the post-switch Tuesday expiry selection.
func TestPickExpiryDTE(t *testing.T) {
	// 2025-09-02 is a Tuesday; nearest Tue expiry to DTE=14 should be ~14.
	entry := time.Date(2025, 9, 2, 15, 30, 0, 0, time.UTC)
	dte := PickExpiryDTE(entry, 14, "Auto (NSE)", 2, 10)
	if dte < 7 || dte > 21 {
		t.Fatalf("unexpected DTE %d", dte)
	}
	if (entry.AddDate(0, 0, dte)).Weekday() != time.Tuesday {
		t.Fatalf("expiry not a Tuesday: %v", entry.AddDate(0, 0, dte).Weekday())
	}
}

// TestSpreadDebitPositive ensures a bull call spread has a positive debit.
func TestSpreadDebitPositive(t *testing.T) {
	v := SpreadValue(24000, 24000, 24250, 14.0/365.0, 0.06, 0.0125, 0.15, -0.25)
	if v <= 0 {
		t.Fatalf("spread value should be positive, got %.4f", v)
	}
}

// TestKellyFraction verifies the continuous-outcome estimator and cap.
func TestKellyFraction(t *testing.T) {
	// Positive edge, low variance -> capped at 0.60.
	rs := []float64{0.1, 0.12, 0.09, 0.11, 0.1, 0.1}
	if f := KellyFraction(rs, 1.0); f != kellyFCap {
		t.Fatalf("expected cap %.2f, got %.4f", kellyFCap, f)
	}
	// Negative mean -> zero.
	rs2 := []float64{-0.1, -0.2, 0.05, -0.3, -0.1}
	if f := KellyFraction(rs2, 1.0); f != 0 {
		t.Fatalf("expected 0 on negative edge, got %.4f", f)
	}
}
