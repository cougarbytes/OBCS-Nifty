package strategy

import (
	"testing"
	"time"
)

// TestFractionalElapsedOvernight verifies the weekday and weekend day counts
// match simulator.run_backtest (0.740 for one night, 2.740 over a weekend).
func TestFractionalElapsedOvernight(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+30*60)

	// Weekday: Wed 15:20 -> Thu 09:20 = 1 calendar day.
	entry := time.Date(2026, 1, 7, 15, 20, 0, 0, ist).UTC()
	exit := time.Date(2026, 1, 8, 9, 20, 0, 0, ist).UTC()
	if got := FractionalElapsed(entry, exit); !approx(got, 0.740, 1e-3) {
		t.Fatalf("weekday elapsed = %.4f, want ~0.740", got)
	}

	// Weekend: Fri 15:20 -> Mon 09:20 = 3 calendar days.
	fri := time.Date(2026, 1, 9, 15, 20, 0, 0, ist).UTC()
	mon := time.Date(2026, 1, 12, 9, 20, 0, 0, ist).UTC()
	if got := FractionalElapsed(fri, mon); !approx(got, 2.740, 1e-3) {
		t.Fatalf("weekend elapsed = %.4f, want ~2.740", got)
	}
}
