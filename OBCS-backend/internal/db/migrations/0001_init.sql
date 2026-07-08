-- OBCS-Nifty schema — KISS: Keep It Simple, Safe & Secure.
--
-- Security posture (OWASP A01: Broken Access Control):
--   * Row Level Security is ON for every table.
--   * The `authenticated` role (Supabase Auth users, i.e. the web UI) may only
--     SELECT. It can NEVER insert/update/delete.
--   * All writes go through the Go backend using the service_role key, which
--     bypasses RLS. The backend is therefore the single, validated writer.
--
-- All timestamps are stored in UTC (timestamptz). Presentation converts to IST.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ─────────────────────────────────────────────────────────────────────────────
-- strategy_state — a single row (id = 1) describing the runner.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS public.strategy_state (
    id                          smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    status                      text        NOT NULL DEFAULT 'stopped'
                                            CHECK (status IN ('running', 'stopped')),
    trading_mode                text        NOT NULL DEFAULT 'paper'
                                            CHECK (trading_mode IN ('paper', 'live')),
    started_at                  timestamptz,
    stopped_at                  timestamptz,
    accumulated_runtime_seconds bigint      NOT NULL DEFAULT 0,
    equity                      numeric(18,2) NOT NULL DEFAULT 0,
    last_message                text,
    updated_at                  timestamptz NOT NULL DEFAULT now()
);

INSERT INTO public.strategy_state (id) VALUES (1)
ON CONFLICT (id) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────────
-- trades — one row per overnight bull call spread round trip.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS public.trades (
    id              uuid          PRIMARY KEY DEFAULT gen_random_uuid(),
    status          text          NOT NULL DEFAULT 'open'
                                  CHECK (status IN ('open', 'closed', 'error')),
    trading_mode    text          NOT NULL CHECK (trading_mode IN ('paper', 'live')),
    underlying      text          NOT NULL,
    entry_time      timestamptz   NOT NULL,
    exit_time       timestamptz,
    contract_expiry date          NOT NULL,
    k1              integer        NOT NULL,   -- long ATM call strike
    k2              integer        NOT NULL,   -- short OTM call strike
    lots            integer        NOT NULL,
    lot_size        integer        NOT NULL,
    entry_spot      numeric(18,2)  NOT NULL,
    exit_spot       numeric(18,2),
    sigma_atm       numeric(10,6),
    entry_debit     numeric(18,4)  NOT NULL,   -- net premium in index points
    exit_value      numeric(18,4),
    margin_used     numeric(18,2)  NOT NULL,   -- premium at risk (rupees)
    gross_pnl       numeric(18,2),
    costs           numeric(18,2),
    net_pnl         numeric(18,2),
    kelly_f         numeric(10,6),
    broker_order_ref text,
    note            text,
    created_at      timestamptz    NOT NULL DEFAULT now(),
    updated_at      timestamptz    NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_trades_entry_time ON public.trades (entry_time DESC);
CREATE INDEX IF NOT EXISTS idx_trades_exit_time  ON public.trades (exit_time);
CREATE INDEX IF NOT EXISTS idx_trades_status      ON public.trades (status);

-- ─────────────────────────────────────────────────────────────────────────────
-- trade_option_data — LIVE option snapshots captured at execution.
-- Per spec: only live data is persisted; computed Greeks are returned on demand
-- by the API for comparison and are NEVER written here.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS public.trade_option_data (
    id          uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    trade_id    uuid         NOT NULL REFERENCES public.trades (id) ON DELETE CASCADE,
    leg         text         NOT NULL CHECK (leg IN ('long', 'short', 'net')),
    phase       text         NOT NULL CHECK (phase IN ('entry', 'exit')),
    data_type   text         NOT NULL DEFAULT 'live' CHECK (data_type = 'live'),
    price       numeric(18,4),
    delta       numeric(12,6),
    gamma       numeric(14,8),
    theta       numeric(14,6),
    vega        numeric(14,6),
    iv          numeric(10,6),
    captured_at timestamptz  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_option_data_trade ON public.trade_option_data (trade_id);

-- ─────────────────────────────────────────────────────────────────────────────
-- holidays — NSE trading holidays for the current year (scraped on init).
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS public.holidays (
    holiday_date date        PRIMARY KEY,
    description  text        NOT NULL,
    year         integer     NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_holidays_year ON public.holidays (year);

-- ─────────────────────────────────────────────────────────────────────────────
-- v_daily_pnl — realized net P&L aggregated by exit day (IST), for the PnL
-- graph and calendar heatmap. Views are not writable, so no RLS is needed here;
-- access is granted explicitly below.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE OR REPLACE VIEW public.v_daily_pnl AS
SELECT
    (exit_time AT TIME ZONE 'Asia/Kolkata')::date AS day,
    SUM(net_pnl)                                   AS net_pnl,
    COUNT(*)                                       AS trades,
    SUM(CASE WHEN net_pnl > 0 THEN 1 ELSE 0 END)   AS wins
FROM public.trades
WHERE status = 'closed' AND exit_time IS NOT NULL AND net_pnl IS NOT NULL
GROUP BY 1
ORDER BY 1;

-- ─────────────────────────────────────────────────────────────────────────────
-- app_bootstrap — one-time initialization marker (single app user seeded once).
-- Backend-only: no RLS policy is defined, so the anon/authenticated roles can
-- never read it; only the service_role (backend) touches this table.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS public.app_bootstrap (
    id        smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    seeded    boolean     NOT NULL DEFAULT false,
    username  text,
    user_id   uuid,        -- Supabase Auth subject (sub) of the single app user
    seeded_at timestamptz
);
INSERT INTO public.app_bootstrap (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
ALTER TABLE public.app_bootstrap ENABLE ROW LEVEL SECURITY;

-- ─────────────────────────────────────────────────────────────────────────────
-- Row Level Security
-- ─────────────────────────────────────────────────────────────────────────────
ALTER TABLE public.strategy_state   ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.trades           ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.trade_option_data ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.holidays         ENABLE ROW LEVEL SECURITY;

-- authenticated users (the UI) get read-only access; nothing else.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE tablename = 'strategy_state' AND policyname = 'read_authenticated') THEN
        CREATE POLICY read_authenticated ON public.strategy_state FOR SELECT TO authenticated USING (true);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE tablename = 'trades' AND policyname = 'read_authenticated') THEN
        CREATE POLICY read_authenticated ON public.trades FOR SELECT TO authenticated USING (true);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE tablename = 'trade_option_data' AND policyname = 'read_authenticated') THEN
        CREATE POLICY read_authenticated ON public.trade_option_data FOR SELECT TO authenticated USING (true);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE tablename = 'holidays' AND policyname = 'read_authenticated') THEN
        CREATE POLICY read_authenticated ON public.holidays FOR SELECT TO authenticated USING (true);
    END IF;
END $$;

-- Grant read access to the roles PostgREST/Realtime use.
GRANT USAGE ON SCHEMA public TO anon, authenticated;
GRANT SELECT ON public.strategy_state, public.trades, public.trade_option_data,
               public.holidays, public.v_daily_pnl TO authenticated;

-- ─────────────────────────────────────────────────────────────────────────────
-- Realtime: publish the tables the UI subscribes to for the live dashboard.
-- ─────────────────────────────────────────────────────────────────────────────
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        ALTER PUBLICATION supabase_realtime ADD TABLE public.trades;
        ALTER PUBLICATION supabase_realtime ADD TABLE public.strategy_state;
    END IF;
EXCEPTION WHEN duplicate_object THEN
    -- tables already in the publication; ignore.
    NULL;
END $$;
