// Command backend is the OBCS-Nifty API server and strategy runner.
//
// Boot sequence:
//  1. load & validate config (fails closed on missing security params)
//  2. connect Postgres and apply migrations
//  3. seed the NSE holiday calendar for the current year
//  4. provision the single Supabase Auth user (once)
//  5. build the strategy engine, market-data source and broker for the mode
//  6. start the HTTP server; the strategy loop runs in its own goroutine
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/obcs-nifty/backend/internal/angelone"
	"github.com/obcs-nifty/backend/internal/api"
	"github.com/obcs-nifty/backend/internal/authseed"
	"github.com/obcs-nifty/backend/internal/broker"
	"github.com/obcs-nifty/backend/internal/config"
	"github.com/obcs-nifty/backend/internal/db"
	"github.com/obcs-nifty/backend/internal/holidays"
	"github.com/obcs-nifty/backend/internal/runner"
	"github.com/obcs-nifty/backend/internal/strategy"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log.Info("config loaded", "trading_mode", cfg.TradingMode, "port", cfg.Port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── database + migrations ────────────────────────────────────────────────
	store, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()

	migCtx, migCancel := context.WithTimeout(ctx, 60*time.Second)
	defer migCancel()
	if err := db.Migrate(migCtx, store.Pool()); err != nil {
		return err
	}
	log.Info("migrations applied")

	// ── holiday calendar ─────────────────────────────────────────────────────
	seedHolidays(ctx, cfg, store, log)

	// ── app user (Supabase Auth) ──────────────────────────────────────────────
	if err := authseed.EnsureUser(ctx, store, cfg.SupabaseURL, cfg.SupabaseServiceKey, log); err != nil {
		log.Warn("user seed failed (continuing)", "err", err)
	}

	// ── strategy engine ───────────────────────────────────────────────────────
	engine := strategy.NewEngine(toEngineParams(cfg.Strategy))

	// ── market data + broker ──────────────────────────────────────────────────
	md, brk, err := buildMarketAndBroker(ctx, cfg, log)
	if err != nil {
		return err
	}
	log.Info("market data + broker ready", "data", md.Source(), "broker_mode", brk.Mode())

	// ── strategy runner (separate goroutine on Start) ─────────────────────────
	run := runner.New(cfg, store, engine, md, brk, log)

	// ── HTTP server ────────────────────────────────────────────────────────────
	allowedSub, _ := store.SeededUserID(ctx) // bind API access to the app user
	if allowedSub == "" {
		log.Warn("no seeded user id available; API accepts any authenticated Supabase user")
	}
	srv := api.NewServer(cfg, store, run, engine, md, allowedSub)
	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      40 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Info("http listening", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
		}
	}()

	// ── graceful shutdown ──────────────────────────────────────────────────────
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info("shutting down")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutCancel()
	_ = run.Stop(shutCtx)
	return httpSrv.Shutdown(shutCtx)
}

func seedHolidays(ctx context.Context, cfg *config.Config, store *db.Store, log *slog.Logger) {
	year := time.Now().In(config.IST()).Year()
	n, err := store.CountHolidays(ctx, year)
	if err != nil {
		log.Warn("count holidays failed", "err", err)
		return
	}
	if n > 0 {
		log.Info("holidays already present", "year", year, "count", n)
		return
	}
	sc := holidays.New(cfg.HolidaySource)
	fetchCtx, fetchCancel := context.WithTimeout(ctx, 30*time.Second)
	defer fetchCancel()
	hs, live := sc.FetchForYear(fetchCtx, year)
	if err := store.UpsertHolidays(ctx, hs); err != nil {
		log.Warn("store holidays failed", "err", err)
		return
	}
	log.Info("holidays seeded", "year", year, "count", len(hs), "live", live)
}

// buildMarketAndBroker selects the data source and broker for the trading mode.
// Live mode requires an authenticated AngelOne client; paper mode uses AngelOne
// data when credentials are present, otherwise a synthetic series.
func buildMarketAndBroker(ctx context.Context, cfg *config.Config, log *slog.Logger) (broker.MarketData, broker.Broker, error) {
	if cfg.TradingMode == config.ModeLive {
		client := angelone.New(cfg.AngelOne)
		loginCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := client.Login(loginCtx); err != nil {
			return nil, nil, err
		}
		scrip := angelone.NewScripMaster()
		md := broker.NewAngelOneMarketData(client, "", "")
		brk := broker.NewLiveBroker(client, scrip)
		return md, brk, nil
	}

	// Paper mode.
	if cfg.AngelOne.Configured() {
		client := angelone.New(cfg.AngelOne)
		loginCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := client.Login(loginCtx); err != nil {
			log.Warn("angelone login failed in paper mode; using synthetic data", "err", err)
			return broker.NewSyntheticMarketData(cfg.Strategy.InitialCapital/8.0, 0.01), broker.NewPaperBroker(), nil
		}
		return broker.NewAngelOneMarketData(client, "", ""), broker.NewPaperBroker(), nil
	}
	return broker.NewSyntheticMarketData(24000, 0.01), broker.NewPaperBroker(), nil
}

func toEngineParams(s config.StrategyConfig) strategy.Params {
	return strategy.Params{
		Underlying:     s.Underlying,
		LotSize:        s.LotSize,
		Lots:           s.Lots,
		StrikeStep:     s.StrikeStep,
		StrikeDistPct:  s.StrikeDistPct,
		DTETarget:      s.DTETarget,
		RiskFreeRate:   s.RiskFreeRate,
		DivYield:       s.DivYield,
		HVWindow:       s.HVWindow,
		UseDynamicHV:   s.UseDynamicHV,
		FixedIV:        s.FixedIV,
		IVMult:         s.IVMult,
		IVAdd:          s.IVAdd,
		SkewPts:        s.SkewPts,
		UseEMAFilter:   s.UseEMAFilter,
		EMAPeriod:      s.EMAPeriod,
		UseExpiryCal:   s.UseExpiryCal,
		ExpiryWeekday:  s.ExpiryWeekday,
		InitialCapital: s.InitialCapital,
		UseAGC:         s.UseAGC,
		KellyMult:      s.KellyMult,
		AGCWindow:      s.AGCWindow,
		MaxLots:        s.MaxLots,
		Costs: strategy.CostParams{
			Enable:            s.EnableCosts,
			SlippagePts:       s.SlippagePts,
			BrokeragePerOrder: s.BrokeragePerOrder,
			STTPct:            s.STTPct,
			ExchPct:           s.ExchPct,
			GSTPct:            s.GSTPct,
			StampPct:          s.StampPct,
		},
	}
}
