package strategy

import "testing"

// TestLotsFromFraction verifies nearest-integer rounding and the no-floor
// contract: a sub-half-lot signal is 0, not 1.
func TestLotsFromFraction(t *testing.T) {
	cases := []struct {
		name   string
		kellyF float64
		equity float64
		debit  float64
		want   int
	}{
		{"non-positive edge", 0.0, 1_000_000, 20_000, 0},
		{"negative edge", -0.1, 1_000_000, 20_000, 0},
		{"sub-half lot rounds down to zero", 0.008, 1_000_000, 20_000, 0}, // 0.4 lots
		{"over-half lot rounds up to one", 0.012, 1_000_000, 20_000, 1},   // 0.6 lots
		{"nearest above one lot", 0.058, 1_000_000, 20_000, 3},            // 2.9 lots
		{"zero debit", 0.05, 1_000_000, 0, 0},
		{"zero equity", 0.05, 0, 20_000, 0},
	}
	for _, c := range cases {
		if got := LotsFromFraction(c.kellyF, c.equity, c.debit); got != c.want {
			t.Errorf("%s: LotsFromFraction(%v, %v, %v) = %d, want %d",
				c.name, c.kellyF, c.equity, c.debit, got, c.want)
		}
	}
}

// TestSizeLots verifies clamping and that zero is never promoted to one.
func TestSizeLots(t *testing.T) {
	cases := []struct {
		name                         string
		desired, maxLots, affordable int
		want                         int
	}{
		{"zero stays zero", 0, 10, 5, 0},
		{"negative clamps to zero", -3, 10, 5, 0},
		{"max lots clamp", 8, 4, 10, 4},
		{"affordability clamp", 8, 10, 3, 3},
		{"within bounds passes through", 2, 10, 5, 2},
	}
	for _, c := range cases {
		if got := SizeLots(c.desired, c.maxLots, c.affordable); got != c.want {
			t.Errorf("%s: SizeLots(%d, %d, %d) = %d, want %d",
				c.name, c.desired, c.maxLots, c.affordable, got, c.want)
		}
	}
}
