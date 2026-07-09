// Package api wires the gin HTTP layer: middleware, routes and handlers. The
// backend is the only writer to the database; every mutating endpoint is
// authenticated and validated.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/obcs-nifty/backend/internal/broker"
	"github.com/obcs-nifty/backend/internal/config"
	"github.com/obcs-nifty/backend/internal/db"
	"github.com/obcs-nifty/backend/internal/models"
	"github.com/obcs-nifty/backend/internal/runner"
	"github.com/obcs-nifty/backend/internal/strategy"
)

// Server bundles the handler dependencies.
type Server struct {
	cfg        *config.Config
	store      *db.Store
	runner     *runner.Runner
	engine     *strategy.Engine
	md         broker.MarketData
	allowedSub string // Supabase subject of the single app user (may be empty)
}

// NewServer constructs the HTTP server dependencies. allowedSub binds API
// access to the single provisioned user; pass "" to accept any authenticated
// Supabase user (not recommended for production).
func NewServer(cfg *config.Config, store *db.Store, run *runner.Runner,
	engine *strategy.Engine, md broker.MarketData, allowedSub string) *Server {
	return &Server{cfg: cfg, store: store, runner: run, engine: engine, md: md, allowedSub: allowedSub}
}

func reqCtx(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 30*time.Second)
}

// health is an unauthenticated liveness probe. It deliberately discloses no
// operational detail (trading mode / data source) to anonymous callers.
func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// getState returns the runner state (with derived total runtime).
func (s *Server) getState(c *gin.Context) {
	ctx, cancel := reqCtx(c)
	defer cancel()
	st, err := s.store.GetState(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load state"})
		return
	}
	c.JSON(http.StatusOK, st)
}

// startStrategy launches the runner loop.
func (s *Server) startStrategy(c *gin.Context) {
	ctx, cancel := reqCtx(c)
	defer cancel()
	if err := s.runner.Start(ctx); err != nil {
		if errors.Is(err, runner.ErrHalted) {
			c.JSON(http.StatusConflict, gin.H{"error": runner.ErrHalted.Error()})
			return
		}
		slog.Error("start strategy", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start strategy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "running"})
}

// stopStrategy halts the runner loop.
func (s *Server) stopStrategy(c *gin.Context) {
	ctx, cancel := reqCtx(c)
	defer cancel()
	if err := s.runner.Stop(ctx); err != nil {
		slog.Error("stop strategy", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop strategy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

// enterManual forces an entry now. Restricted to paper mode so live capital is
// never exposed to an out-of-schedule click.
func (s *Server) enterManual(c *gin.Context) {
	if s.cfg.TradingMode != config.ModePaper {
		c.JSON(http.StatusForbidden, gin.H{"error": "manual entry is only allowed in paper mode"})
		return
	}
	ctx, cancel := reqCtx(c)
	defer cancel()
	if err := s.runner.Enter(ctx, true); err != nil {
		slog.Error("manual enter", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "entry failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "entered"})
}

// exitManual forces an exit of the open trade now. Paper mode only.
func (s *Server) exitManual(c *gin.Context) {
	if s.cfg.TradingMode != config.ModePaper {
		c.JSON(http.StatusForbidden, gin.H{"error": "manual exit is only allowed in paper mode"})
		return
	}
	ctx, cancel := reqCtx(c)
	defer cancel()
	open, err := s.store.GetOpenTrade(ctx)
	if err != nil {
		slog.Error("get open trade", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load open trade"})
		return
	}
	if open == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "no open trade"})
		return
	}
	if err := s.runner.Exit(ctx, open, true); err != nil {
		slog.Error("manual exit", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "exit failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "closed"})
}

// listTrades returns recent trades. Each trade includes its live option data.
func (s *Server) listTrades(c *gin.Context) {
	ctx, cancel := reqCtx(c)
	defer cancel()
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	trades, err := s.store.ListTrades(ctx, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load trades"})
		return
	}
	for i := range trades {
		od, _ := s.store.OptionDataForTrade(ctx, trades[i].ID)
		trades[i].OptionData = od
	}
	c.JSON(http.StatusOK, gin.H{"trades": trades})
}

// getTradeOptions returns the persisted LIVE option snapshots for a trade.
func (s *Server) getTradeOptions(c *gin.Context) {
	ctx, cancel := reqCtx(c)
	defer cancel()
	id := c.Param("id")
	data, err := s.store.OptionDataForTrade(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load option data"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"option_data": data})
}

// getTradeComputed computes fresh (model) Greeks for a trade at the current
// spot and remaining time so the UI can compare live vs computed. Per spec,
// these computed values are NOT persisted.
func (s *Server) getTradeComputed(c *gin.Context) {
	ctx, cancel := reqCtx(c)
	defer cancel()
	id := c.Param("id")
	trade, err := s.store.GetTradeByID(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load trade"})
		return
	}
	if trade == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "trade not found"})
		return
	}
	spot, err := s.md.Spot(ctx)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "spot unavailable"})
		return
	}
	// Remaining time to expiry in years.
	remDays := trade.ContractExpiry.Sub(time.Now().UTC()).Hours() / 24.0
	if remDays < 0 {
		remDays = 0
	}
	t := remDays / 365.0
	if t < 1e-4 {
		t = 1e-4
	}
	greeks := s.engine.GreeksAt(spot, float64(trade.K1), float64(trade.K2), t, trade.SigmaATM)
	c.JSON(http.StatusOK, gin.H{
		"trade_id":  trade.ID,
		"spot":      spot,
		"data_type": "computed",
		"greeks":    greeks,
		"note":      "computed on demand for comparison; not persisted",
	})
}

// preview computes the current entry plan (with computed Greeks) WITHOUT
// placing an order, for the UI to display candidate strikes/margin/Greeks.
func (s *Server) preview(c *gin.Context) {
	ctx, cancel := reqCtx(c)
	defer cancel()
	spot, err := s.md.Spot(ctx)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "spot unavailable"})
		return
	}
	closes, err := s.md.DailyCloses(ctx, 120)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "market data unavailable"})
		return
	}
	// Live mode reflects the connected broker's real available cash; paper mode
	// carries the stored equity (falling back to configured initial capital).
	equity := s.runner.AccountEquity(ctx)
	aboveEMA := true
	if s.cfg.Strategy.UseEMAFilter {
		aboveEMA = spot >= strategy.EMA(closes, s.cfg.Strategy.EMAPeriod)
	}
	recent, _ := s.store.RecentReturns(ctx, s.cfg.Strategy.AGCWindow)
	plan := s.engine.ComputeEntry(closes, spot, equity, aboveEMA, recent, time.Now().In(config.IST()))
	c.JSON(http.StatusOK, gin.H{"plan": plan, "data_source": s.md.Source()})
}

// getDailyPnL returns realized P&L aggregated by day for a date range
// (defaults to the current calendar year).
func (s *Server) getDailyPnL(c *gin.Context) {
	ctx, cancel := reqCtx(c)
	defer cancel()
	now := time.Now().In(config.IST())
	from := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, config.IST())
	to := time.Date(now.Year(), 12, 31, 0, 0, 0, 0, config.IST())
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			from = t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			to = t
		}
	}
	rows, err := s.store.DailyPnL(ctx, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load pnl"})
		return
	}
	if rows == nil {
		rows = []models.DailyPnL{}
	}
	c.JSON(http.StatusOK, gin.H{"daily": rows})
}

// getHolidays returns the NSE holiday calendar for a year (defaults current).
func (s *Server) getHolidays(c *gin.Context) {
	ctx, cancel := reqCtx(c)
	defer cancel()
	year := time.Now().In(config.IST()).Year()
	if v := c.Query("year"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			year = n
		}
	}
	hs, err := s.store.HolidaysForYear(ctx, year)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load holidays"})
		return
	}
	if hs == nil {
		hs = []models.Holiday{}
	}
	c.JSON(http.StatusOK, gin.H{"holidays": hs, "year": year})
}
