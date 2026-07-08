package angelone

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// scripMasterURL is AngelOne's public instrument dump.
const scripMasterURL = "https://margincalculator.angelbroking.com/OpenAPI_File/files/OpenAPIScripMaster.json"

// Instrument is one row of the scrip master relevant to option resolution.
type Instrument struct {
	Token          string `json:"token"`
	Symbol         string `json:"symbol"`
	Name           string `json:"name"`
	Expiry         string `json:"expiry"` // e.g. 29JAN2025
	Strike         string `json:"strike"` // in paise, e.g. 2400000.000000
	InstrumentType string `json:"instrumenttype"`
	ExchSeg        string `json:"exch_seg"`
}

// ScripMaster caches the instrument dump for symbol/token resolution.
type ScripMaster struct {
	mu     sync.RWMutex
	loaded time.Time
	items  []Instrument
	http   *http.Client
}

// NewScripMaster constructs an empty cache.
func NewScripMaster() *ScripMaster {
	return &ScripMaster{http: &http.Client{Timeout: 120 * time.Second}}
}

// ensure downloads the dump if it is missing or older than 24h.
func (s *ScripMaster) ensure(ctx context.Context) error {
	s.mu.RLock()
	fresh := len(s.items) > 0 && time.Since(s.loaded) < 24*time.Hour
	s.mu.RUnlock()
	if fresh {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scripMasterURL, nil)
	if err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var items []Instrument
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("parse scrip master: %w", err)
	}
	s.mu.Lock()
	s.items = items
	s.loaded = time.Now()
	s.mu.Unlock()
	return nil
}

// ResolveOption finds the NFO option instrument for an underlying name, expiry
// date, integer strike and option type (CE/PE). Returns the matching
// instrument or an error when none is found (fail closed).
func (s *ScripMaster) ResolveOption(ctx context.Context, name string, expiry time.Time, strike int, optType string) (Instrument, error) {
	if err := s.ensure(ctx); err != nil {
		return Instrument{}, err
	}
	expStr := strings.ToUpper(expiry.Format("02Jan2006")) // 29JAN2025
	strikePaise := float64(strike) * 100.0

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, it := range s.items {
		if it.ExchSeg != "NFO" {
			continue
		}
		if !strings.EqualFold(it.Name, name) {
			continue
		}
		if !strings.EqualFold(it.InstrumentType, "OPTIDX") {
			continue
		}
		if !strings.EqualFold(it.Expiry, expStr) {
			continue
		}
		if !strings.HasSuffix(strings.ToUpper(it.Symbol), strings.ToUpper(optType)) {
			continue
		}
		st, _ := strconv.ParseFloat(it.Strike, 64)
		if int(st) == int(strikePaise) {
			return it, nil
		}
	}
	return Instrument{}, fmt.Errorf("option not found: %s %s %d %s", name, expStr, strike, optType)
}
