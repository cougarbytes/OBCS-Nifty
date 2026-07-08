// Package holidays scrapes the NSE trading-holiday calendar for the current
// year. NSE's public endpoints require a browser-like session (cookie priming);
// the scraper primes cookies and sets realistic headers. If scraping fails
// (NSE anti-bot, no network), it falls back to a curated static list so the
// application always initializes with a usable calendar.
package holidays

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"time"

	"github.com/obcs-nifty/backend/internal/models"
)

// Scraper fetches NSE holidays.
type Scraper struct {
	apiURL string
	http   *http.Client
}

// New constructs a scraper with a cookie jar (needed to satisfy NSE).
func New(apiURL string) *Scraper {
	jar, _ := cookiejar.New(nil)
	return &Scraper{
		apiURL: apiURL,
		http: &http.Client{
			Timeout: 20 * time.Second,
			Jar:     jar,
		},
	}
}

// nseHolidayResponse models the /api/holiday-master JSON shape:
// { "CM": [ {"tradingDate":"26-Jan-2026","description":"Republic Day"}, ... ] }
type nseHolidayResponse map[string][]struct {
	TradingDate string `json:"tradingDate"`
	Description string `json:"description"`
}

// FetchForYear returns NSE trading holidays for the given year. It tries live
// scraping first, then falls back to the static list. The bool return reports
// whether live data was used.
func (s *Scraper) FetchForYear(ctx context.Context, year int) ([]models.Holiday, bool) {
	if hs, err := s.scrape(ctx, year); err == nil && len(hs) > 0 {
		return hs, true
	}
	return staticHolidays(year), false
}

func (s *Scraper) scrape(ctx context.Context, year int) ([]models.Holiday, error) {
	// Prime cookies by hitting the NSE homepage first.
	if err := s.prime(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiURL, nil)
	if err != nil {
		return nil, err
	}
	s.setBrowserHeaders(req)
	req.Header.Set("Referer", "https://www.nseindia.com/resources/exchange-communication-holidays")

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nse holiday api status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var parsed nseHolidayResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	// "CM" = cash market segment; fall back to any segment present.
	rows, ok := parsed["CM"]
	if !ok {
		for _, v := range parsed {
			rows = v
			break
		}
	}
	seen := map[string]bool{}
	var out []models.Holiday
	for _, r := range rows {
		t, err := time.Parse("02-Jan-2006", r.TradingDate)
		if err != nil || t.Year() != year {
			continue
		}
		key := t.Format("2006-01-02")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, models.Holiday{
			Date:        key,
			Description: r.Description,
			Year:        year,
		})
	}
	return out, nil
}

func (s *Scraper) prime(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.nseindia.com/", nil)
	if err != nil {
		return err
	}
	s.setBrowserHeaders(req)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	return nil
}

func (s *Scraper) setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Connection", "keep-alive")
}
