// Package db provides the Postgres/Supabase data-access layer. The backend is
// the single writer; all queries are parameterized (defence against SQL
// injection, OWASP A03) via pgx.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/obcs-nifty/backend/internal/models"
)

// Store wraps a pgx connection pool with typed data operations.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a connection pool to the given DATABASE_URL and pings it.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 8
	cfg.MaxConnLifetime = time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Pool exposes the underlying pool (used by the migrator).
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// ── strategy_state ───────────────────────────────────────────────────────────

// GetState returns the singleton strategy state with derived runtime.
func (s *Store) GetState(ctx context.Context) (models.StrategyState, error) {
	var st models.StrategyState
	var status, mode string
	err := s.pool.QueryRow(ctx, `
		SELECT status, trading_mode, started_at, stopped_at,
		       accumulated_runtime_seconds, equity, COALESCE(last_message,''), updated_at
		FROM public.strategy_state WHERE id = 1`).
		Scan(&status, &mode, &st.StartedAt, &st.StoppedAt,
			&st.AccumulatedRuntimeSeconds, &st.Equity, &st.LastMessage, &st.UpdatedAt)
	if err != nil {
		return st, err
	}
	st.Status = models.StrategyStatus(status)
	st.TradingMode = mode
	st.RuntimeSeconds = st.AccumulatedRuntimeSeconds
	if st.Status == models.StatusRunning && st.StartedAt != nil {
		st.RuntimeSeconds += int64(time.Since(*st.StartedAt).Seconds())
	}
	return st, nil
}

// StartStrategy marks the runner running from now, if not already.
func (s *Store) StartStrategy(ctx context.Context, mode string, equity float64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE public.strategy_state
		SET status = 'running',
		    trading_mode = $1,
		    started_at = now(),
		    stopped_at = NULL,
		    equity = $2,
		    last_message = 'strategy started',
		    updated_at = now()
		WHERE id = 1 AND status <> 'running'`, mode, equity)
	return err
}

// StopStrategy accumulates the elapsed session runtime and marks it stopped.
func (s *Store) StopStrategy(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE public.strategy_state
		SET status = 'stopped',
		    stopped_at = now(),
		    accumulated_runtime_seconds = accumulated_runtime_seconds
		        + CASE WHEN started_at IS NOT NULL
		               THEN GREATEST(0, EXTRACT(EPOCH FROM (now() - started_at))::bigint)
		               ELSE 0 END,
		    started_at = NULL,
		    last_message = 'strategy stopped',
		    updated_at = now()
		WHERE id = 1 AND status = 'running'`)
	return err
}

// UpdateStateMessage records equity and a status message without changing the
// running/stopped lifecycle.
func (s *Store) UpdateStateMessage(ctx context.Context, equity float64, msg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE public.strategy_state
		SET equity = $1, last_message = $2, updated_at = now()
		WHERE id = 1`, equity, msg)
	return err
}

// UpdateEquity records the current equity without touching the status message.
// Used by the periodic broker-funds sync so the last meaningful message (e.g.
// the most recent entry/exit) is preserved.
func (s *Store) UpdateEquity(ctx context.Context, equity float64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE public.strategy_state
		SET equity = $1, updated_at = now()
		WHERE id = 1`, equity)
	return err
}

// ── trades ───────────────────────────────────────────────────────────────────

// InsertTrade creates a new open trade and returns its generated id.
func (s *Store) InsertTrade(ctx context.Context, t models.Trade) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO public.trades
		    (status, trading_mode, underlying, entry_time, contract_expiry,
		     k1, k2, lots, lot_size, entry_spot, sigma_atm, entry_debit,
		     margin_used, kelly_f, broker_order_ref, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING id`,
		t.Status, t.TradingMode, t.Underlying, t.EntryTime, t.ContractExpiry,
		t.K1, t.K2, t.Lots, t.LotSize, t.EntrySpot, t.SigmaATM, t.EntryDebit,
		t.MarginUsed, t.KellyF, nullStr(t.BrokerOrderRef), nullStr(t.Note)).Scan(&id)
	return id, err
}

// CloseTrade fills the exit fields and marks the trade closed.
func (s *Store) CloseTrade(ctx context.Context, id string, exitTime time.Time,
	exitSpot, exitValue, gross, costs, net float64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE public.trades
		SET status = 'closed', exit_time = $2, exit_spot = $3, exit_value = $4,
		    gross_pnl = $5, costs = $6, net_pnl = $7, updated_at = now()
		WHERE id = $1`, id, exitTime, exitSpot, exitValue, gross, costs, net)
	return err
}

// MarkTradeError flags a trade that failed mid-lifecycle.
func (s *Store) MarkTradeError(ctx context.Context, id, note string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE public.trades SET status = 'error', note = $2, updated_at = now()
		WHERE id = $1`, id, note)
	return err
}

// ListTrades returns trades most-recent first, capped by limit.
func (s *Store) ListTrades(ctx context.Context, limit int) ([]models.Trade, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, status, trading_mode, underlying, entry_time, exit_time,
		       contract_expiry, k1, k2, lots, lot_size, entry_spot, exit_spot,
		       sigma_atm, entry_debit, exit_value, margin_used, gross_pnl, costs,
		       net_pnl, kelly_f, COALESCE(broker_order_ref,''), COALESCE(note,''),
		       created_at, updated_at
		FROM public.trades
		ORDER BY entry_time DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrades(rows)
}

// GetTradeByID returns a single trade or (nil,nil) when not found.
func (s *Store) GetTradeByID(ctx context.Context, id string) (*models.Trade, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, status, trading_mode, underlying, entry_time, exit_time,
		       contract_expiry, k1, k2, lots, lot_size, entry_spot, exit_spot,
		       sigma_atm, entry_debit, exit_value, margin_used, gross_pnl, costs,
		       net_pnl, kelly_f, COALESCE(broker_order_ref,''), COALESCE(note,''),
		       created_at, updated_at
		FROM public.trades WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ts, err := scanTrades(rows)
	if err != nil {
		return nil, err
	}
	if len(ts) == 0 {
		return nil, nil
	}
	return &ts[0], nil
}

// GetOpenTrade returns the most recent open trade, or (nil,nil) if none.
func (s *Store) GetOpenTrade(ctx context.Context) (*models.Trade, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, status, trading_mode, underlying, entry_time, exit_time,
		       contract_expiry, k1, k2, lots, lot_size, entry_spot, exit_spot,
		       sigma_atm, entry_debit, exit_value, margin_used, gross_pnl, costs,
		       net_pnl, kelly_f, COALESCE(broker_order_ref,''), COALESCE(note,''),
		       created_at, updated_at
		FROM public.trades
		WHERE status = 'open'
		ORDER BY entry_time DESC
		LIMIT 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ts, err := scanTrades(rows)
	if err != nil {
		return nil, err
	}
	if len(ts) == 0 {
		return nil, nil
	}
	return &ts[0], nil
}

// RecentReturns returns the last n closed-trade return-on-capital values
// (oldest first) for Kelly sizing, scoped to the given trading mode.
//
// The return is normalized by margin_used — the capital actually blocked for
// the trade (broker SPAN+exposure hedged margin in live mode, premium debit in
// paper mode) — so the Kelly window shares one base with the sizing
// denominator (strategy.EntryPlan.CapitalPerLot): KellyFraction estimates the
// edge per rupee blocked and LotsFromFraction divides equity by rupees blocked
// per lot. Normalizing here by premium while sizing divides by margin would
// inflate live lot counts by the margin/premium ratio. The premium expression
// remains only as a fallback for rows without a stored margin. Filtering by
// mode keeps the two capital bases (paper premium vs live margin) from
// blending in one window.
func (s *Store) RecentReturns(ctx context.Context, n int, mode string) ([]float64, error) {
	if n <= 0 {
		n = 30
	}
	rows, err := s.pool.Query(ctx, `
		SELECT CASE WHEN margin_used > 0 THEN net_pnl / margin_used
		            WHEN entry_debit > 0 AND lot_size > 0 AND lots > 0
		            THEN net_pnl / (entry_debit * lot_size * lots) ELSE 0 END AS r
		FROM public.trades
		WHERE status = 'closed' AND net_pnl IS NOT NULL AND trading_mode = $2
		ORDER BY exit_time DESC
		LIMIT $1`, n, mode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []float64
	for rows.Next() {
		var r float64
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	// reverse to oldest-first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

func scanTrades(rows pgx.Rows) ([]models.Trade, error) {
	var out []models.Trade
	for rows.Next() {
		var t models.Trade
		var status, mode string
		if err := rows.Scan(
			&t.ID, &status, &mode, &t.Underlying, &t.EntryTime, &t.ExitTime,
			&t.ContractExpiry, &t.K1, &t.K2, &t.Lots, &t.LotSize, &t.EntrySpot,
			&t.ExitSpot, &t.SigmaATM, &t.EntryDebit, &t.ExitValue, &t.MarginUsed,
			&t.GrossPnL, &t.Costs, &t.NetPnL, &t.KellyF, &t.BrokerOrderRef,
			&t.Note, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Status = models.TradeStatus(status)
		t.TradingMode = mode
		out = append(out, t)
	}
	return out, rows.Err()
}

// ── trade_option_data (LIVE snapshots only) ─────────────────────────────────

// InsertOptionData persists a live option Greek snapshot for a trade leg.
func (s *Store) InsertOptionData(ctx context.Context, d models.OptionData) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO public.trade_option_data
		    (trade_id, leg, phase, data_type, price, delta, gamma, theta, vega, iv)
		VALUES ($1,$2,$3,'live',$4,$5,$6,$7,$8,$9)`,
		d.TradeID, d.Leg, d.Phase, d.Price, d.Delta, d.Gamma, d.Theta, d.Vega, d.IV)
	return err
}

// OptionDataForTrade returns all live snapshots for a trade.
func (s *Store) OptionDataForTrade(ctx context.Context, tradeID string) ([]models.OptionData, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, trade_id, leg, phase, data_type, price, delta, gamma, theta,
		       vega, iv, captured_at
		FROM public.trade_option_data
		WHERE trade_id = $1
		ORDER BY captured_at`, tradeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.OptionData
	for rows.Next() {
		var d models.OptionData
		if err := rows.Scan(&d.ID, &d.TradeID, &d.Leg, &d.Phase, &d.DataType,
			&d.Price, &d.Delta, &d.Gamma, &d.Theta, &d.Vega, &d.IV, &d.CapturedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ── daily pnl view ──────────────────────────────────────────────────────────

// DailyPnL returns realized P&L aggregated by day between the given dates
// (inclusive), oldest first.
func (s *Store) DailyPnL(ctx context.Context, from, to time.Time) ([]models.DailyPnL, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT to_char(day,'YYYY-MM-DD'), net_pnl, trades, wins
		FROM public.v_daily_pnl
		WHERE day >= $1 AND day <= $2
		ORDER BY day`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.DailyPnL
	for rows.Next() {
		var d models.DailyPnL
		if err := rows.Scan(&d.Day, &d.NetPnL, &d.Trades, &d.Wins); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ── holidays ─────────────────────────────────────────────────────────────────

// UpsertHolidays inserts holidays, ignoring duplicates.
func (s *Store) UpsertHolidays(ctx context.Context, hs []models.Holiday) error {
	batch := &pgx.Batch{}
	for _, h := range hs {
		batch.Queue(`
			INSERT INTO public.holidays (holiday_date, description, year)
			VALUES ($1,$2,$3)
			ON CONFLICT (holiday_date) DO UPDATE SET description = EXCLUDED.description`,
			h.Date, h.Description, h.Year)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range hs {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// HolidaysForYear returns all holidays for a given year.
func (s *Store) HolidaysForYear(ctx context.Context, year int) ([]models.Holiday, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT to_char(holiday_date,'YYYY-MM-DD'), description, year
		FROM public.holidays WHERE year = $1 ORDER BY holiday_date`, year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Holiday
	for rows.Next() {
		var h models.Holiday
		if err := rows.Scan(&h.Date, &h.Description, &h.Year); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// CountHolidays returns the number of stored holidays for a year.
func (s *Store) CountHolidays(ctx context.Context, year int) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM public.holidays WHERE year = $1`, year).Scan(&n)
	return n, err
}

// IsHoliday reports whether a date is a stored NSE holiday.
func (s *Store) IsHoliday(ctx context.Context, day time.Time) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM public.holidays WHERE holiday_date = $1)`,
		day.Format("2006-01-02")).Scan(&exists)
	return exists, err
}

// ── bootstrap ────────────────────────────────────────────────────────────────

// IsSeeded reports whether the single app user has already been provisioned.
func (s *Store) IsSeeded(ctx context.Context) (bool, error) {
	var seeded bool
	err := s.pool.QueryRow(ctx,
		`SELECT seeded FROM public.app_bootstrap WHERE id = 1`).Scan(&seeded)
	return seeded, err
}

// MarkSeeded records that the app user was provisioned (stores the username and
// Supabase subject id, never the password).
func (s *Store) MarkSeeded(ctx context.Context, username, userID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE public.app_bootstrap
		SET seeded = true, username = $1, user_id = $2::uuid, seeded_at = now()
		WHERE id = 1`, username, nullStr(userID))
	return err
}

// SeededUserID returns the provisioned app user's Supabase subject id, or ""
// when unavailable. Used to bind API access to that single identity.
func (s *Store) SeededUserID(ctx context.Context) (string, error) {
	var id *string
	err := s.pool.QueryRow(ctx,
		`SELECT user_id::text FROM public.app_bootstrap WHERE id = 1`).Scan(&id)
	if err != nil || id == nil {
		return "", err
	}
	return *id, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
