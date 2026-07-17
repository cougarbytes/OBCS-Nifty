// Package runner orchestrates the live/paper strategy loop in its own
// goroutine, independent of the HTTP request path (per spec: "live trades fired
// from a separate goroutine"). It enters an overnight bull call spread near the
// session close and exits it near the next session's open, honouring weekends,
// NSE holidays and the equity halt gate.
package runner

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/obcs-nifty/backend/internal/broker"
	"github.com/obcs-nifty/backend/internal/config"
	"github.com/obcs-nifty/backend/internal/db"
	"github.com/obcs-nifty/backend/internal/strategy"
)

// ErrHalted is returned by Start when the account has been halted (equity
// depleted) and the process must be restarted to reset.
var ErrHalted = errors.New("strategy halted (equity depleted); restart the process to reset")

// Runner drives the strategy loop.
type Runner struct {
	cfg    *config.Config
	store  *db.Store
	engine *strategy.Engine
	md     broker.MarketData
	brk    broker.Broker
	ist    *time.Location
	log    *slog.Logger

	mu             sync.Mutex
	running        bool
	halted         bool
	cancel         context.CancelFunc
	done           chan struct{}
	lastEquitySync time.Time
}

// equitySyncInterval throttles how often the live loop re-reads broker funds so
// a 30s tick does not hammer the RMS API.
const equitySyncInterval = 60 * time.Minute

// New constructs a runner.
func New(cfg *config.Config, store *db.Store, engine *strategy.Engine,
	md broker.MarketData, brk broker.Broker, log *slog.Logger) *Runner {
	return &Runner{
		cfg:    cfg,
		store:  store,
		engine: engine,
		md:     md,
		brk:    brk,
		ist:    config.IST(),
		log:    log,
	}
}

// Running reports whether the loop goroutine is active.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// resolveEquity returns the capital the strategy should size against. In live
// mode it queries the connected broker's available cash (via the FundsProvider
// interface); if that call fails, or in paper mode, it uses the stored equity
// and finally the configured initial capital.
func (r *Runner) resolveEquity(ctx context.Context, stored float64) float64 {
	if fp, ok := r.brk.(broker.FundsProvider); ok {
		if eq, err := fp.AvailableEquity(ctx); err != nil {
			r.log.Warn("broker funds fetch failed; using stored equity", "err", err)
		} else if eq > 0 {
			return eq
		}
	}
	if stored > 0 {
		return stored
	}
	return r.cfg.Strategy.InitialCapital
}

// AccountEquity resolves the current tradable equity for read-only callers (e.g.
// the preview endpoint), preferring the broker's live cash in live mode.
func (r *Runner) AccountEquity(ctx context.Context) float64 {
	st, _ := r.store.GetState(ctx)
	return r.resolveEquity(ctx, st.Equity)
}

// refreshEquity periodically syncs the stored equity with the broker's real
// available cash so the dashboard and the halt gate track the account between
// entries/exits. Throttled to equitySyncInterval; a no-op in paper mode (no
// FundsProvider) and when the strategy is not running.
func (r *Runner) refreshEquity(ctx context.Context) {
	fp, ok := r.brk.(broker.FundsProvider)
	if !ok {
		return
	}

	r.mu.Lock()
	due := r.running && time.Since(r.lastEquitySync) >= equitySyncInterval
	if due {
		r.lastEquitySync = time.Now()
	}
	r.mu.Unlock()
	if !due {
		return
	}

	eq, err := fp.AvailableEquity(ctx)
	if err != nil {
		r.log.Warn("periodic equity refresh failed; keeping last known equity", "err", err)
		return
	}
	if eq <= 0 {
		return
	}
	if err := r.store.UpdateEquity(ctx, eq); err != nil {
		r.log.Warn("persist refreshed equity failed", "err", err)
	}
}

// Start launches the loop. Idempotent: a second call while running is a no-op.
func (r *Runner) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil
	}
	if r.halted {
		r.mu.Unlock()
		return ErrHalted
	}

	// Seed equity: live mode uses the connected broker's real cash; paper mode
	// carries the stored equity, falling back to configured initial capital.
	st, err := r.store.GetState(ctx)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	equity := r.resolveEquity(ctx, st.Equity)
	if err := r.store.StartStrategy(ctx, string(r.cfg.TradingMode), equity); err != nil {
		r.mu.Unlock()
		return err
	}

	loopCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.running = true
	r.done = make(chan struct{})
	// Equity was just seeded from the broker above; suppress an immediate
	// re-fetch on the first loop tick.
	r.lastEquitySync = time.Now()
	r.mu.Unlock()

	go r.loop(loopCtx)
	r.log.Info("strategy started", "mode", r.cfg.TradingMode, "data", r.md.Source())
	return nil
}

// Stop halts the loop and accumulates runtime.
func (r *Runner) Stop(ctx context.Context) error {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return nil
	}
	r.cancel()
	done := r.done
	r.running = false
	r.mu.Unlock()

	<-done
	if err := r.store.StopStrategy(ctx); err != nil {
		return err
	}
	r.log.Info("strategy stopped")
	return nil
}

// loop ticks on an interval, evaluating the session schedule each tick.
func (r *Runner) loop(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	r.evaluate(ctx) // act immediately on start
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.evaluate(ctx)
		}
	}
}

// evaluate decides whether to enter or exit given the current IST time.
func (r *Runner) evaluate(ctx context.Context) {
	// Keep stored equity in step with the broker (live mode, throttled).
	r.refreshEquity(ctx)

	now := time.Now().In(r.ist)
	openTrade, err := r.store.GetOpenTrade(ctx)
	if err != nil {
		r.log.Error("get open trade", "err", err)
		return
	}

	// Morning exit window: close any position carried overnight.
	if openTrade != nil && r.inExitWindow(now) {
		if err := r.Exit(ctx, openTrade, false); err != nil {
			r.log.Error("exit failed", "err", err)
		}
		return
	}

	// Close-of-day entry window: open a fresh spread if none is open.
	if openTrade == nil && r.inEntryWindow(now) && r.isTradingDay(ctx, now) {
		if err := r.Enter(ctx, false); err != nil {
			r.log.Error("entry failed", "err", err)
		}
	}
}

func (r *Runner) inEntryWindow(now time.Time) bool {
	h, m := r.cfg.Strategy.EntryHM()
	start := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, r.ist)
	end := start.Add(10 * time.Minute) // 10-minute window
	return !now.Before(start) && now.Before(end)
}

func (r *Runner) inExitWindow(now time.Time) bool {
	h, m := r.cfg.Strategy.ExitHM()
	start := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, r.ist)
	end := start.Add(10 * time.Minute)
	return !now.Before(start) && now.Before(end)
}

func (r *Runner) isTradingDay(ctx context.Context, now time.Time) bool {
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return false
	}
	holiday, err := r.store.IsHoliday(ctx, now)
	if err != nil {
		r.log.Warn("holiday check failed; assuming trading day", "err", err)
		return true
	}
	return !holiday
}
