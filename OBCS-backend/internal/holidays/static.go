package holidays

import "github.com/obcs-nifty/backend/internal/models"

// staticHolidays is the offline fallback used when NSE scraping fails. These
// dates MUST be verified against the official NSE circular; they exist only so
// the app initializes with a non-empty calendar. Live scraping is authoritative.
func staticHolidays(year int) []models.Holiday {
	byYear := map[int][][2]string{
		2025: {
			{"2025-02-26", "Mahashivratri"},
			{"2025-03-14", "Holi"},
			{"2025-03-31", "Id-Ul-Fitr (Ramzan Id)"},
			{"2025-04-10", "Shri Mahavir Jayanti"},
			{"2025-04-14", "Dr. Baba Saheb Ambedkar Jayanti"},
			{"2025-04-18", "Good Friday"},
			{"2025-05-01", "Maharashtra Day"},
			{"2025-08-15", "Independence Day"},
			{"2025-08-27", "Ganesh Chaturthi"},
			{"2025-10-02", "Mahatma Gandhi Jayanti / Dussehra"},
			{"2025-10-21", "Diwali Laxmi Pujan"},
			{"2025-10-22", "Balipratipada"},
			{"2025-11-05", "Prakash Gurpurb Sri Guru Nanak Dev"},
			{"2025-12-25", "Christmas"},
		},
		2026: {
			{"2026-01-26", "Republic Day"},
			{"2026-02-16", "Mahashivratri"},
			{"2026-03-04", "Holi"},
			{"2026-03-21", "Id-Ul-Fitr (Ramzan Id)"},
			{"2026-03-31", "Shri Mahavir Jayanti"},
			{"2026-04-03", "Good Friday"},
			{"2026-04-14", "Dr. Baba Saheb Ambedkar Jayanti"},
			{"2026-05-01", "Maharashtra Day"},
			{"2026-08-15", "Independence Day"},
			{"2026-09-14", "Ganesh Chaturthi"},
			{"2026-10-02", "Mahatma Gandhi Jayanti"},
			{"2026-11-09", "Diwali Laxmi Pujan"},
			{"2026-11-10", "Balipratipada"},
			{"2026-11-24", "Prakash Gurpurb Sri Guru Nanak Dev"},
			{"2026-12-25", "Christmas"},
		},
	}
	rows := byYear[year]
	out := make([]models.Holiday, 0, len(rows))
	for _, r := range rows {
		out = append(out, models.Holiday{Date: r[0], Description: r[1], Year: year})
	}
	return out
}
