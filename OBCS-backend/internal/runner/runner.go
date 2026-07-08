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

	mu      sync.Mutex
	running bool
	halted  bool
	cancel  context.CancelFunc
	done    chan struct{}
}

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

	// Seed equity with initial capital on first start.
	st, err := r.store.GetState(ctx)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	equity := st.Equity
	if equity <= 0 {
		equity = r.cfg.Strategy.InitialCapital
	}
	if err := r.store.StartStrategy(ctx, string(r.cfg.TradingMode), equity); err != nil {
		r.mu.Unlock()
		return err
	}

	loopCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.running = true
	r.done = make(chan struct{})
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
