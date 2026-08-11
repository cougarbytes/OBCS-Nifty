package runner

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/obcs-nifty/backend/internal/broker"
	"github.com/obcs-nifty/backend/internal/db"
	"github.com/obcs-nifty/backend/internal/models"
	"github.com/obcs-nifty/backend/internal/strategy"
)

// lookbackDays is how many daily closes we pull for HV/EMA computation.
const lookbackDays = 120

// maxOrderRetries caps how many failed order attempts the runner makes per
// scope — one entry window (IST day) or one trade's exit — before standing
// down. Without a cap the 30s tick re-tries all window long: on entry each
// failed attempt buys the long leg and unwinds it after the short-leg
// rejection, crossing the bid-ask spread and paying brokerage twice per tick.
const maxOrderRetries = 3

// shortCoveredMarker is persisted on trade.Note the moment an exit's short-leg
// buy-back fills, so a retried exit (after a failed long-leg sell, or a
// process restart) never buys the short back a second time.
const shortCoveredMarker = "EXIT-PARTIAL short_covered="

// rejectionText extracts the broker's rejection reason when the error chain
// carries one, else falls back to the raw error text. Non-rejection failures
// (e.g. a placement timeout) are recorded and budgeted too: a timed-out order
// may still have reached the broker, so blind retries are not free.
func rejectionText(err error) string {
	if reason, ok := broker.RejectionReason(err); ok {
		return reason
	}
	return err.Error()
}

// shortCoveredPrice parses the persisted buy-back price out of a trade note.
func shortCoveredPrice(note string) (float64, bool) {
	i := strings.Index(note, shortCoveredMarker)
	if i < 0 {
		return 0, false
	}
	v := note[i+len(shortCoveredMarker):]
	if j := strings.IndexByte(v, ' '); j >= 0 {
		v = v[:j]
	}
	p, err := strconv.ParseFloat(v, 64)
	return p, err == nil && p >= 0
}

// recordRejection persists one failed leg attempt (the durable retry budget)
// and logs it. A failed insert is logged loudly: if the budget cannot be
// persisted the read-side gate still fails closed on the next tick only when
// the read also fails, so this must never be silent.
func (r *Runner) recordRejection(ctx context.Context, rj models.OrderRejection) {
	if err := r.store.InsertOrderRejection(ctx, rj); err != nil {
		r.log.Error("PERSIST ORDER REJECTION FAILED — retry budget may not advance",
			"err", err, "phase", rj.Phase, "leg", rj.Leg, "reason", rj.Reason)
		return
	}
	r.log.Warn("broker leg order failed", "phase", rj.Phase, "leg", rj.Leg, "side", rj.Side,
		"attempt", rj.Attempt, "max", maxOrderRetries, "fatal", rj.Fatal, "reason", rj.Reason)
}

// Enter opens a new overnight bull call spread. When `force` is true the entry
// window / trading-day checks are skipped (used by the manual paper trigger for
// demonstration; the handler restricts this to paper mode).
func (r *Runner) Enter(ctx context.Context, force bool) error {
	st, err := r.store.GetState(ctx)
	if err != nil {
		return err
	}
	// Live mode sizes against the broker's real available cash; paper mode uses
	// the stored equity (falling back to configured initial capital).
	equity := r.resolveEquity(ctx, st.Equity)

	// Retry gate: leg rejections are persisted per IST session. Once the
	// budget is spent — or a prior unwind failed, leaving a naked long leg at
	// the broker — the runner stands down for the day instead of buying and
	// unwinding the long leg again on every tick of the entry window. A gate
	// read failure blocks the entry (fail closed): trading blind on the retry
	// state is exactly the loop this exists to prevent.
	now := time.Now().In(r.ist)
	sessionDate := now.Format("2006-01-02")
	rej, err := r.store.EntryRejections(ctx, sessionDate)
	if err != nil {
		return fmt.Errorf("entry rejection state: %w", err)
	}
	if rej.Fatal {
		return r.noEntry(ctx, equity, fmt.Sprintf(
			"entry suspended: earlier long-leg unwind FAILED (%s); reconcile broker positions manually", rej.LastReason))
	}
	if rej.Attempts >= maxOrderRetries {
		return r.noEntry(ctx, equity, fmt.Sprintf(
			"entry suspended after %d broker rejections today (last: %s)", rej.Attempts, rej.LastReason))
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

	recent, err := r.store.RecentReturns(ctx, r.cfg.Strategy.AGCWindow, string(r.cfg.TradingMode))
	if err != nil {
		return err
	}

	underlying := r.cfg.Strategy.Underlying

	// Two-phase sizing: a dry plan on the premium fallback fixes the strikes
	// and expiry, then the broker prices ONE lot of that spread and the final
	// plan re-sizes on the capital actually blocked. Kelly and the
	// affordability gate then divide by the rupees the account loses access
	// to, not the (smaller) premium debit.
	dry := r.engine.ComputeEntry(closes, spot, equity, aboveEMA, recent, now, 0)
	if !dry.ShouldEnter && !dry.SizingFailure() {
		// Hard gate (trend filter, degenerate debit): no margin figure can
		// revive this entry, and the plan may not even carry strikes.
		return r.noEntry(ctx, equity, dry.Reason)
	}

	marginPerLot := 0.0
	if mp, ok := r.brk.(broker.MarginProvider); ok {
		oneLot := r.cfg.Strategy.LotSize
		basket := []broker.SpreadLeg{
			{Leg: broker.Leg{Strike: dry.K1, Expiry: dry.Expiry, OptionType: "CE", Qty: oneLot}, Side: "BUY"},
			{Leg: broker.Leg{Strike: dry.K2, Expiry: dry.Expiry, OptionType: "CE", Qty: oneLot}, Side: "SELL"},
		}
		if m, err := mp.SpreadMargin(ctx, underlying, basket); err != nil {
			r.log.Warn("per-lot margin unavailable; sizing on premium fallback", "err", err)
		} else if m > 0 {
			marginPerLot = m
		}
	}

	plan := dry
	if marginPerLot > 0 {
		plan = r.engine.ComputeEntry(closes, spot, equity, aboveEMA, recent, now, marginPerLot)
	}
	if !plan.ShouldEnter {
		return r.noEntry(ctx, equity, plan.Reason)
	}

	qty := plan.Lots * r.cfg.Strategy.LotSize
	// Tag both legs with a shared id so the spread is identifiable as one hedged
	// basket on the broker (AngelOne has no atomic multi-leg order).
	tag := fmt.Sprintf("OBCS%d", now.Unix())
	longLeg := broker.Leg{Strike: plan.K1, Expiry: plan.Expiry, OptionType: "CE", Qty: qty, ModelPrice: plan.C1Exec, Tag: tag}
	shortLeg := broker.Leg{Strike: plan.K2, Expiry: plan.Expiry, OptionType: "CE", Qty: qty, ModelPrice: plan.C2Exec, Tag: tag}

	// Final pre-trade margin gate (live): price the sized basket as one hedged
	// unit and refuse to open if the broker requirement exceeds available cash.
	// Per-lot margin scales linearly for identical spreads, but the broker's
	// own total is authoritative — it keeps the recorded margin honest and
	// avoids a partial fill on rejection.
	brokerMargin := 0.0
	if mp, ok := r.brk.(broker.MarginProvider); ok {
		basket := []broker.SpreadLeg{{Leg: longLeg, Side: "BUY"}, {Leg: shortLeg, Side: "SELL"}}
		if m, err := mp.SpreadMargin(ctx, underlying, basket); err != nil {
			r.log.Warn("spread margin check failed; proceeding on sized estimate", "err", err)
		} else {
			brokerMargin = m
			if m > equity {
				r.log.Warn("insufficient margin", "required", m, "equity", equity)
				return r.noEntry(ctx, equity,
					fmt.Sprintf("required margin %.2f exceeds available equity %.2f", m, equity))
			}
		}
	}

	// Buy the long (hedge) leg first so the short leg is never left unhedged. If
	// the short leg then fails, unwind the long leg rather than carrying a naked
	// long (and never a naked short). Every failure consumes one unit of the
	// session's retry budget and stores the broker's rejection reason.
	longFill, longRef, err := r.brk.Buy(ctx, underlying, longLeg)
	if err != nil {
		r.recordRejection(ctx, models.OrderRejection{
			Phase: "entry", Leg: "long", Side: "BUY",
			Attempt: rej.Attempts + 1, BrokerOrderRef: longRef,
			Reason: rejectionText(err), SessionDate: sessionDate,
		})
		return fmt.Errorf("buy long leg: %w", err)
	}
	shortFill, shortRef, err := r.brk.Sell(ctx, underlying, shortLeg)
	if err != nil {
		rejRow := models.OrderRejection{
			Phase: "entry", Leg: "short", Side: "SELL",
			Attempt: rej.Attempts + 1, BrokerOrderRef: shortRef,
			Reason: rejectionText(err), SessionDate: sessionDate,
		}
		if _, unwindRef, uErr := r.brk.Sell(ctx, underlying, longLeg); uErr != nil {
			// Naked long call left at the broker: fatal for the day — never
			// stack more legs on top of an unreconciled position.
			rejRow.Fatal = true
			rejRow.Reason = fmt.Sprintf("short leg: %s; long-leg unwind FAILED (%s) — naked long K1=%d at broker",
				rejRow.Reason, rejectionText(uErr), plan.K1)
			r.log.Error("short leg failed AND long-leg unwind failed; manual intervention required",
				"long_ref", longRef, "short_err", err, "unwind_err", uErr)
		} else {
			r.log.Warn("short leg failed; unwound long leg", "long_ref", longRef, "unwind_ref", unwindRef, "short_err", err)
		}
		r.recordRejection(ctx, rejRow)
		return fmt.Errorf("sell short leg (long leg unwound): %w", err)
	}

	entryDebit := longFill - shortFill
	// Record the capital actually blocked: the broker's own total for the sized
	// basket when available, else the per-lot figure scaled by lots, else the
	// executed premium debit (max loss for a debit spread — paper mode lands
	// here). This is the Kelly denominator db.RecentReturns reads back.
	marginUsed := entryDebit * float64(r.cfg.Strategy.LotSize) * float64(plan.Lots)
	if brokerMargin > 0 {
		marginUsed = brokerMargin
	} else if marginPerLot > 0 {
		marginUsed = marginPerLot * float64(plan.Lots)
	}

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

// noEntry records a skipped entry and lets the scheduler loop continue.
func (r *Runner) noEntry(ctx context.Context, equity float64, reason string) error {
	_ = r.store.UpdateStateMessage(ctx, equity, "no entry: "+reason)
	r.log.Info("no entry", "reason", reason)
	return nil
}

// Exit closes an open spread and books realized P&L.
func (r *Runner) Exit(ctx context.Context, trade *models.Trade, force bool) error {
	// Retry gate: exit attempts are budgeted per trade. Once the budget is
	// spent, stop hammering the broker on every tick and flag the trade for
	// manual reconciliation — status 'error' removes it from GetOpenTrade, so
	// the loop stands down. The note records exactly which legs are still open
	// at the broker. Gate read failure blocks the exit attempt (fail closed).
	rej, err := r.store.ExitRejections(ctx, trade.ID)
	if err != nil {
		return fmt.Errorf("exit rejection state: %w", err)
	}
	if rej.Attempts >= maxOrderRetries || rej.Fatal {
		return r.abandonExit(ctx, trade, rej)
	}

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
		// Live: reverse the spread. Buy back the SHORT leg FIRST so the long
		// hedge is never removed while the short is still open (which would leave
		// a naked short call — full SPAN+exposure margin and open-ended risk).
		qty := trade.Lots * trade.LotSize
		tag := fmt.Sprintf("OBCSX%d", time.Now().Unix())
		longLeg := broker.Leg{Strike: trade.K1, Expiry: trade.ContractExpiry, OptionType: "CE", Qty: qty, Tag: tag}
		shortLeg := broker.Leg{Strike: trade.K2, Expiry: trade.ContractExpiry, OptionType: "CE", Qty: qty, Tag: tag}
		sessionDate := time.Now().In(r.ist).Format("2006-01-02")

		// A prior attempt may have covered the short leg and then failed on the
		// long-leg sell. NEVER buy the short back twice: each duplicate buy-back
		// stacks a fresh long K2 position on top of the book.
		if price, ok := shortCoveredPrice(trade.Note); ok {
			exitShort = price
			r.log.Info("short leg already covered by earlier attempt; selling long leg only",
				"trade", trade.ID, "covered_at", price)
		} else {
			var shortRef string
			if exitShort, shortRef, err = r.brk.Buy(ctx, trade.Underlying, shortLeg); err != nil {
				reason := rejectionText(err)
				r.recordRejection(ctx, models.OrderRejection{
					TradeID: trade.ID, Phase: "exit", Leg: "short", Side: "BUY",
					Attempt: rej.Attempts + 1, BrokerOrderRef: shortRef,
					Reason: reason, SessionDate: sessionDate,
				})
				_ = r.store.SetTradeRejection(ctx, trade.ID, reason)
				return fmt.Errorf("buy back short leg: %w", err)
			}
			// Persist the partial-exit state immediately: if the long-leg sell
			// (or this process) fails from here, the retry must know the short
			// is already covered.
			if nErr := r.store.UpdateTradeNote(ctx, trade.ID,
				fmt.Sprintf("%s%.4f", shortCoveredMarker, exitShort)); nErr != nil {
				r.log.Error("persist short-covered marker failed; a retried exit may double-buy the short leg",
					"trade", trade.ID, "err", nErr)
			}
		}
		var longRef string
		if exitLong, longRef, err = r.brk.Sell(ctx, trade.Underlying, longLeg); err != nil {
			// Short leg already covered; a leftover long call is benign (limited
			// risk). Surface for reconciliation rather than forcing more orders.
			reason := rejectionText(err)
			r.recordRejection(ctx, models.OrderRejection{
				TradeID: trade.ID, Phase: "exit", Leg: "long", Side: "SELL",
				Attempt: rej.Attempts + 1, BrokerOrderRef: longRef,
				Reason: reason, SessionDate: sessionDate,
			})
			_ = r.store.SetTradeRejection(ctx, trade.ID, reason)
			return fmt.Errorf("sell long leg (short leg already covered): %w", err)
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

// abandonExit stops retrying a rejected exit after the budget is spent. The
// trade is flagged 'error' (dropping it out of GetOpenTrade, which halts the
// per-tick retries) with the broker's last rejection reason and an explicit
// statement of which legs remain open at the broker. No P&L is booked and
// equity is not touched: the position is unreconciled until handled manually.
func (r *Runner) abandonExit(ctx context.Context, trade *models.Trade, rej db.RejectionState) error {
	legState := "both legs still open at broker (hedged spread intact; defined-risk debit spread)"
	if p, ok := shortCoveredPrice(trade.Note); ok {
		legState = fmt.Sprintf("short K2=%d covered at %.4f; long K1=%d still open (limited-risk long call)",
			trade.K2, p, trade.K1)
	}
	note := fmt.Sprintf("exit abandoned after %d broker rejections (last: %s); %s — reconcile manually",
		rej.Attempts, rej.LastReason, legState)

	if err := r.store.SetTradeRejection(ctx, trade.ID, rej.LastReason); err != nil {
		r.log.Warn("persist trade rejection reason", "err", err)
	}
	if err := r.store.MarkTradeError(ctx, trade.ID, note); err != nil {
		return fmt.Errorf("mark trade error: %w", err)
	}
	st, _ := r.store.GetState(ctx)
	_ = r.store.UpdateStateMessage(ctx, st.Equity, "EXIT ABANDONED: "+note)
	r.log.Error("exit abandoned; manual intervention required", "trade", trade.ID, "note", note)
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
	capPerLot := 0.0
	if trade.Lots > 0 {
		capPerLot = trade.MarginUsed / float64(trade.Lots)
	}
	return strategy.EntryPlan{
		K1:            trade.K1,
		K2:            trade.K2,
		DTE:           daysToExpiry(trade),
		SigmaATM:      trade.SigmaATM,
		EntryDebit:    trade.EntryDebit,
		DebitPerLot:   trade.EntryDebit * float64(trade.LotSize),
		CapitalPerLot: capPerLot,
		Lots:          trade.Lots,
	}
}
