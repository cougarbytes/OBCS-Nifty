// Package config loads and validates runtime configuration from the
// environment. No secrets are ever hard-coded; everything comes from env vars
// (see .env.example). This follows OWASP A05:2021 (Security Misconfiguration)
// by failing closed when required security parameters are missing.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// TradingMode controls whether real broker orders are placed.
type TradingMode string

const (
	// ModePaper simulates fills locally and never contacts the live broker
	// order API. This is the safe default.
	ModePaper TradingMode = "paper"
	// ModeLive places real orders through the AngelOne SmartAPI. It requires
	// TRADING_MODE=live AND a complete set of AngelOne credentials.
	ModeLive TradingMode = "live"
)

// Config is the fully-resolved application configuration.
type Config struct {
	// HTTP server
	Port           string
	AllowedOrigins []string

	// Supabase / Postgres
	DatabaseURL        string
	SupabaseURL        string
	SupabaseAnonKey    string
	SupabaseServiceKey string
	SupabaseJWTSecret  string

	// Trading
	TradingMode TradingMode
	Strategy    StrategyConfig
	AngelOne    AngelOneConfig

	// Ops
	HolidaySource string
}

// StrategyConfig mirrors the tunable parameters of the OBCS backtest so live
// behaviour is consistent with simulator.py.
type StrategyConfig struct {
	Underlying     string
	LotSize        int
	Lots           int
	StrikeStep     int
	StrikeDistPct  float64 // OTM distance of the short leg, percent
	DTETarget      int     // days-to-expiry target for the long/short calls
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

	// Kelly adaptive gearing controller.
	UseAGC    bool
	KellyMult float64
	AGCWindow int
	MaxLots   int

	// Cost model (Indian retail option round trip).
	EnableCosts       bool
	SlippagePts       float64
	BrokeragePerOrder float64
	STTPct            float64
	ExchPct           float64
	GSTPct            float64
	StampPct          float64

	// Session windows in IST. Entry near close, exit near next open.
	EntryTime string // "15:20"
	ExitTime  string // "09:20"
}

// AngelOneConfig holds SmartAPI connection parameters. Empty when not
// configured; live mode validation catches that case.
type AngelOneConfig struct {
	APIKey     string
	ClientCode string
	MPIN       string
	TOTPSecret string
	BaseURL    string
	LocalIP    string
	PublicIP   string
	MACAddr    string
}

// Configured reports whether every AngelOne credential required for login is
// present.
func (a AngelOneConfig) Configured() bool {
	return a.APIKey != "" && a.ClientCode != "" && a.MPIN != "" && a.TOTPSecret != ""
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvFloat(key string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func getenvBool(key string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

// Load reads configuration from the environment, applies safe defaults and
// validates security-critical fields. It returns an error rather than panicking
// so main can log and exit cleanly.
func Load() (*Config, error) {
	mode := TradingMode(strings.ToLower(getenv("TRADING_MODE", string(ModePaper))))
	if mode != ModePaper && mode != ModeLive {
		return nil, fmt.Errorf("invalid TRADING_MODE %q (want paper|live)", mode)
	}

	cfg := &Config{
		Port:               getenv("PORT", "8080"),
		AllowedOrigins:     splitCSV(getenv("ALLOWED_ORIGINS", "http://localhost:3000")),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		SupabaseURL:        os.Getenv("SUPABASE_URL"),
		SupabaseAnonKey:    os.Getenv("SUPABASE_ANON_KEY"),
		SupabaseServiceKey: os.Getenv("SUPABASE_SERVICE_ROLE_KEY"),
		SupabaseJWTSecret:  os.Getenv("SUPABASE_JWT_SECRET"),
		TradingMode:        mode,
		HolidaySource:      getenv("NSE_HOLIDAY_URL", "https://www.nseindia.com/api/holiday-master?type=trading"),
		AngelOne: AngelOneConfig{
			APIKey:     os.Getenv("AO_API_KEY"),
			ClientCode: os.Getenv("AO_CLIENT_CODE"),
			MPIN:       os.Getenv("AO_MPIN"),
			TOTPSecret: os.Getenv("AO_TOTP_SECRET"),
			BaseURL:    getenv("AO_BASE_URL", "https://apiconnect.angelone.in"),
			LocalIP:    getenv("AO_LOCAL_IP", "127.0.0.1"),
			PublicIP:   os.Getenv("AO_PUBLIC_IP"),
			MACAddr:    os.Getenv("AO_MAC_ADDR"),
		},
		Strategy: StrategyConfig{
			Underlying:        getenv("STRAT_UNDERLYING", "NIFTY"),
			LotSize:           getenvInt("STRAT_LOT_SIZE", 65),
			Lots:              getenvInt("STRAT_LOTS", 2),
			StrikeStep:        getenvInt("STRAT_STRIKE_STEP", 50),
			StrikeDistPct:     getenvFloat("STRAT_STRIKE_DIST_PCT", 1.0),
			DTETarget:         getenvInt("STRAT_DTE_TARGET", 14),
			RiskFreeRate:      getenvFloat("STRAT_RISK_FREE_RATE", 0.06),
			DivYield:          getenvFloat("STRAT_DIV_YIELD", 0.0125),
			HVWindow:          getenvInt("STRAT_HV_WINDOW", 20),
			UseDynamicHV:      getenvBool("STRAT_USE_DYNAMIC_HV", true),
			FixedIV:           getenvFloat("STRAT_FIXED_IV", 0.155),
			IVMult:            getenvFloat("STRAT_IV_MULT", 1.10),
			IVAdd:             getenvFloat("STRAT_IV_ADD", 0.0),
			SkewPts:           getenvFloat("STRAT_SKEW_PTS", -0.25),
			UseEMAFilter:      getenvBool("STRAT_USE_EMA", false),
			EMAPeriod:         getenvInt("STRAT_EMA_PERIOD", 55),
			UseExpiryCal:      getenvBool("STRAT_USE_EXPIRY_CAL", true),
			ExpiryWeekday:     getenv("STRAT_EXPIRY_WEEKDAY", "Auto (NSE)"),
			InitialCapital:    getenvFloat("STRAT_INITIAL_CAPITAL", 200000),
			UseAGC:            getenvBool("STRAT_USE_AGC", false),
			KellyMult:         getenvFloat("STRAT_KELLY_MULT", 0.5),
			AGCWindow:         getenvInt("STRAT_AGC_WINDOW", 30),
			MaxLots:           getenvInt("STRAT_MAX_LOTS", 10),
			EnableCosts:       getenvBool("STRAT_ENABLE_COSTS", true),
			SlippagePts:       getenvFloat("STRAT_SLIPPAGE_PTS", 0.10),
			BrokeragePerOrder: getenvFloat("STRAT_BROKERAGE_PER_ORDER", 20.0),
			STTPct:            getenvFloat("STRAT_STT_PCT", 0.10),
			ExchPct:           getenvFloat("STRAT_EXCH_PCT", 0.035),
			GSTPct:            getenvFloat("STRAT_GST_PCT", 18.0),
			StampPct:          getenvFloat("STRAT_STAMP_PCT", 0.003),
			EntryTime:         getenv("STRAT_ENTRY_TIME", "15:20"),
			ExitTime:          getenv("STRAT_EXIT_TIME", "09:20"),
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var errs []string
	if c.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}
	if c.SupabaseJWTSecret == "" {
		errs = append(errs, "SUPABASE_JWT_SECRET is required (used to verify session tokens)")
	}
	if c.TradingMode == ModeLive && !c.AngelOne.Configured() {
		errs = append(errs, "TRADING_MODE=live requires AO_API_KEY, AO_CLIENT_CODE, AO_MPIN and AO_TOTP_SECRET")
	}
	if c.Strategy.LotSize < 1 {
		errs = append(errs, "STRAT_LOT_SIZE must be >= 1")
	}
	if len(errs) > 0 {
		return errors.New("config error: " + strings.Join(errs, "; "))
	}
	return nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// EntryTimeParsed / ExitTimeParsed parse the "HH:MM" session windows into
// hour/minute pairs, defaulting safely on malformed input.
func (s StrategyConfig) parseHM(v, def string) (int, int) {
	parse := func(x string) (int, int, bool) {
		parts := strings.Split(x, ":")
		if len(parts) != 2 {
			return 0, 0, false
		}
		h, err1 := strconv.Atoi(parts[0])
		m, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			return 0, 0, false
		}
		return h, m, true
	}
	if h, m, ok := parse(v); ok {
		return h, m
	}
	h, m, _ := parse(def)
	return h, m
}

// EntryHM returns the configured entry hour and minute (IST).
func (s StrategyConfig) EntryHM() (int, int) { return s.parseHM(s.EntryTime, "15:20") }

// ExitHM returns the configured exit hour and minute (IST).
func (s StrategyConfig) ExitHM() (int, int) { return s.parseHM(s.ExitTime, "09:20") }

// IST is the exchange timezone. NSE operates in Asia/Kolkata.
func IST() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		// Fixed +05:30 fallback when tzdata is unavailable in the container.
		return time.FixedZone("IST", 5*3600+30*60)
	}
	return loc
}
