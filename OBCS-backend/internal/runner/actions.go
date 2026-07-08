package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/obcs-nifty/backend/internal/broker"
	"github.com/obcs-nifty/backend/internal/models"
	"github.com/obcs-nifty/backend/internal/strategy"
)

// lookbackDays is how many daily closes we pull for HV/EMA computation.
const lookbackDays = 120

// Enter opens a new overnight bull call spread. When `force` is true the entry
// window / trading-day checks are skipped (used by the manual paper trigger for
// demonstration; the handler restricts this to paper mode).
func (r *Runner) Enter(ctx context.Context, force bool) error {
	st, err := r.store.GetState(ctx)
	if err != nil {
		return err
	}
	equity := st.Equity
	if equity <= 0 {
		equity = r.cfg.Strategy.InitialCapital
	}

	spot, err := r.md.Spot(ctx)
	if err != nil {
		return fmt.Errorf("spot: %w", err)
	}
	closes, err := r.md.DailyCloses(ctx, lookbackDays)
	if err != nil {
		return fmt.Errorf("closes: %w", err)
	}

	aboveEMA := true
	if r.cfg.Strategy.UseEMAFilter {
		ema := strategy.EMA(closes, r.cfg.Strategy.EMAPeriod)
		aboveEMA = spot >= ema
	}

	recent, err := r.store.RecentReturns(ctx, r.cfg.Strategy.AGCWindow)
	if err != nil {
		return err
	}

	now := time.Now().In(r.ist)
	plan := r.engine.ComputeEntry(closes, spot, equity, aboveEMA, recent, now)
	if !plan.ShouldEnter {
		_ = r.store.UpdateStateMessage(ctx, equity, "no entry: "+plan.Reason)
		r.log.Info("no entry", "reason", plan.Reason)
		return nil
	}

	qty := plan.Lots * r.cfg.Strategy.LotSize
	longLeg := broker.Leg{Strike: plan.K1, Expiry: plan.Expiry, OptionType: "CE", Qty: qty, ModelPrice: plan.C1Exec}
	shortLeg := broker.Leg{Strike: plan.K2, Expiry: plan.Expiry, OptionType: "CE", Qty: qty, ModelPrice: plan.C2Exec}

	longFill, longRef, err := r.brk.Buy(ctx, r.cfg.Strategy.Underlying, longLeg)
	if err != nil {
		return fmt.Errorf("buy long leg: %w", err)
	}
	shortFill, shortRef, err := r.brk.Sell(ctx, r.cfg.Strategy.Underlying, shortLeg)
	if err != nil {
		return fmt.Errorf("sell short leg: %w", err)
	}

	entryDebit := longFill - shortFill
	marginUsed := entryDebit * float64(r.cfg.Strategy.LotSize) * float64(plan.Lots)

	kelly := plan.KellyF
	trade := models.Trade{
		Status:         models.TradeOpen,
		TradingMode:    string(r.cfg.TradingMode),
		Underlying:     r.cfg.Strategy.Underlying,
		EntryTime:      time.Now().UTC(),
		ContractExpiry: plan.Expiry,
		K1:             plan.K1,
		K2:             plan.K2,
		Lots:           plan.Lots,
		LotSize:        r.cfg.Strategy.LotSize,
		EntrySpot:      spot,
		SigmaATM:       plan.SigmaATM,
		EntryDebit:     entryDebit,
		MarginUsed:     marginUsed,
		KellyF:         &kelly,
		BrokerOrderRef: longRef + "," + shortRef,
	}
	id, err := r.store.InsertTrade(ctx, trade)
	if err != nil {
		return fmt.Errorf("insert trade: %w", err)
	}

	// Persist the LIVE option snapshot at entry (Greeks derived from the
	// executed premiums). Computed Greeks are never stored here.
	tIn := float64(plan.DTE) / 365.0
	live := r.engine.LiveGreeks(spot, float64(plan.K1), float64(plan.K2), tIn, longFill, shortFill, plan.SigmaATM)
	r.storeOptionData(ctx, id, "entry", live)

	_ = r.store.UpdateStateMessage(ctx, equity,
		fmt.Sprintf("entered %s spread K1=%d K2=%d lots=%d debit=%.2f", trade.Underlying, plan.K1, plan.K2, plan.Lots, entryDebit))
	r.log.Info("entered trade", "id", id, "k1", plan.K1, "k2", plan.K2, "lots", plan.Lots, "debit", entryDebit)
	return nil
}

// Exit closes an open spread and books realized P&L.
func (r *Runner) Exit(ctx context.Context, trade *models.Trade, force bool) error {
	spot, err := r.md.Spot(ctx)
	if err != nil {
		return fmt.Errorf("spot: %w", err)
	}

	// Recover the executed entry leg premiums from the stored live snapshot so
	// the cost model has both sides of the round trip.
	entryLong, entryShort := r.entryLegPrices(ctx, trade)

	elapsed := strategy.FractionalElapsed(trade.EntryTime, time.Now().UTC())
	tOut := float64(daysToExpiry(trade)) - elapsed
	if tOut < 0 {
		tOut = 0
	}
	tOutYears := tOut / 365.0
	if tOutYears < 1e-4 {
		tOutYears = 1e-4
	}

	// Determine exit leg fills.
	var exitLong, exitShort float64
	if r.brk.Mode() == r.cfg.TradingMode && r.cfg.TradingMode == "live" {
		// Live: reverse the spread (sell long, buy back short).
		qty := trade.Lots * trade.LotSize
		longLeg := broker.Leg{Strike: trade.K1, Expiry: trade.ContractExpiry, OptionType: "CE", Qty: qty}
		shortLeg := broker.Leg{Strike: trade.K2, Expiry: trade.ContractExpiry, OptionType: "CE", Qty: qty}
		if exitLong, _, err = r.brk.Sell(ctx, trade.Underlying, longLeg); err != nil {
			return fmt.Errorf("sell long leg: %w", err)
		}
		if exitShort, _, err = r.brk.Buy(ctx, trade.Underlying, shortLeg); err != nil {
			return fmt.Errorf("buy short leg: %w", err)
		}
	} else {
		// Paper: value the legs on the model at the exit spot/time.
		plan := planFromTrade(trade)
		res := r.engine.ComputeExit(plan, spot, elapsed)
		exitLong, exitShort = res.C1Exec, res.C2Exec
	}

	exitValue := exitLong - exitShort
	costs := strategy.TradeCashCosts(entryLong, entryShort, exitLong, exitShort,
		trade.LotSize, trade.Lots, r.engine.Params().Costs)
	gross := (exitValue - trade.EntryDebit) * float64(trade.LotSize) * float64(trade.Lots)
	net := gross - costs

	exitTime := time.Now().UTC()
	if err := r.store.CloseTrade(ctx, trade.ID, exitTime, spot, exitValue, gross, costs, net); err != nil {
		return fmt.Errorf("close trade: %w", err)
	}

	// Persist LIVE option snapshot at exit.
	live := r.engine.LiveGreeks(spot, float64(trade.K1), float64(trade.K2), tOutYears, exitLong, exitShort, trade.SigmaATM)
	r.storeOptionData(ctx, trade.ID, "exit", live)

	// Update account equity and enforce the halt gate.
	st, _ := r.store.GetState(ctx)
	equity := st.Equity
	if equity <= 0 {
		equity = r.cfg.Strategy.InitialCapital
	}
	equity += net
	msg := fmt.Sprintf("closed trade net=%.2f equity=%.2f", net, equity)
	_ = r.store.UpdateStateMessage(ctx, equity, msg)
	r.log.Info("closed trade", "id", trade.ID, "net", net, "equity", equity)

	if equity <= 0 {
		r.mu.Lock()
		r.halted = true
		r.mu.Unlock()
		_ = r.store.UpdateStateMessage(ctx, equity, "HALTED: equity depleted")
		go func() { _ = r.Stop(context.Background()) }()
	}
	return nil
}

func (r *Runner) storeOptionData(ctx context.Context, tradeID, phase string, g strategy.SpreadGreeks) {
	legs := []struct {
		name string
		gr   strategy.Greeks
	}{
		{"long", g.Long}, {"short", g.Short}, {"net", g.Net},
	}
	for _, l := range legs {
		if err := r.store.InsertOptionData(ctx, models.OptionData{
			TradeID: tradeID, Leg: l.name, Phase: phase,
			Price: l.gr.Price, Delta: l.gr.Delta, Gamma: l.gr.Gamma,
			Theta: l.gr.Theta, Vega: l.gr.Vega, IV: l.gr.IV,
		}); err != nil {
			r.log.Warn("store option data", "err", err)
		}
	}
}

func (r *Runner) entryLegPrices(ctx context.Context, trade *models.Trade) (long, short float64) {
	data, err := r.store.OptionDataForTrade(ctx, trade.ID)
	if err != nil {
		return 0, 0
	}
	for _, d := range data {
		if d.Phase != "entry" {
			continue
		}
		switch d.Leg {
		case "long":
			long = d.Price
		case "short":
			short = d.Price
		}
	}
	return long, short
}

func daysToExpiry(trade *models.Trade) int {
	d := int(trade.ContractExpiry.Sub(trade.EntryTime).Hours()/24) + 1
	if d < 1 {
		d = 1
	}
	return d
}

func planFromTrade(trade *models.Trade) strategy.EntryPlan {
	return strategy.EntryPlan{
		K1:          trade.K1,
		K2:          trade.K2,
		DTE:         daysToExpiry(trade),
		SigmaATM:    trade.SigmaATM,
		EntryDebit:  trade.EntryDebit,
		DebitPerLot: trade.EntryDebit * float64(trade.LotSize),
		Lots:        trade.Lots,
	}
}
