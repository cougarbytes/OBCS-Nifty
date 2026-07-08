package strategy

import "time"

// nseExpirySwitchYMD is the SEBI/NSE regime-change date (2025-09-01): index
// weeklies expired on Thursday before it and Tuesday on/after. The comparison
// is done on the IST calendar date (not a UTC instant) so a late-night UTC
// timestamp near the boundary is classified by its Indian trading date.
var (
	nseSwitchYear  = 2025
	nseSwitchMonth = time.September
	nseSwitchDay   = 1
)

// istExpiryZone is the fixed +05:30 zone used for expiry date classification.
var istExpiryZone = time.FixedZone("IST", 5*3600+30*60)

// OvernightGapDays is the fraction of a day that elapses between the 15:30
// close entry and the next 09:15 open exit: 24h - 6h15m expressed in days.
// Used to charge partial theta decay. Mirrors _OVERNIGHT_GAP_DAYS.
const OvernightGapDays = 6.25 / 24.0

var weekdayMap = map[string]time.Weekday{
	"Monday":    time.Monday,
	"Tuesday":   time.Tuesday,
	"Wednesday": time.Wednesday,
	"Thursday":  time.Thursday,
	"Friday":    time.Friday,
}

// nseExpiryWeekday returns the exchange expiry weekday for a given entry date
// under the Auto (NSE) rule. Mirrors simulator._nse_expiry_weekday, comparing
// on the IST calendar date.
func nseExpiryWeekday(entry time.Time) time.Weekday {
	e := entry.In(istExpiryZone)
	switch1 := time.Date(nseSwitchYear, nseSwitchMonth, nseSwitchDay, 0, 0, 0, 0, istExpiryZone)
	ed := time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, istExpiryZone)
	if ed.Before(switch1) {
		return time.Thursday
	}
	return time.Tuesday
}

// PickExpiryDTE returns the number of calendar days from the entry date to the
// listed weekly expiry closest to targetDTE (never below minDTE). Exchange
// holidays are not modelled here (a holiday expiry settles a session earlier);
// the holiday calendar is applied separately by the runner. Mirrors
// simulator.pick_expiry_dte.
func PickExpiryDTE(entry time.Time, targetDTE int, weekday string, minDTE, maxWeeks int) int {
	if minDTE < 1 {
		minDTE = 1
	}
	if maxWeeks < 1 {
		maxWeeks = 10
	}

	var wd time.Weekday
	if mapped, ok := weekdayMap[weekday]; ok {
		wd = mapped
	} else {
		wd = nseExpiryWeekday(entry)
	}

	// Days until the first occurrence of the target weekday (0..6).
	first := int((wd - entry.Weekday() + 7) % 7)

	best := -1
	for w := 0; w < maxWeeks; w++ {
		cand := first + 7*w
		if cand < minDTE {
			continue
		}
		if best < 0 || abs(cand-targetDTE) < abs(best-targetDTE) {
			best = cand
		}
	}
	if best < 0 {
		if targetDTE > minDTE {
			return targetDTE
		}
		return minDTE
	}
	return best
}

// ExpiryDate resolves the entry date plus the chosen DTE into a concrete expiry
// calendar date.
func ExpiryDate(entry time.Time, dte int) time.Time {
	return entry.AddDate(0, 0, dte)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
