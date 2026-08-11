-- 0002 — order rejection tracking.
--
-- Motivation: when the broker rejects one leg of the spread (e.g. low
-- liquidity / price band on the selected contract expiry), the runner used to
-- re-attempt on every 30s tick of the 10-minute window. On entry each attempt
-- buys the long leg and unwinds it after the short-leg rejection — crossing the
-- bid-ask spread and paying brokerage/fees twice per tick. Rejections are now
-- persisted (with the broker's reason text) and double as a durable retry
-- counter that survives process restarts:
--   * entry attempts are scoped by IST session_date (one entry window per day);
--   * exit attempts are scoped by trade_id.

-- Terminal broker rejection reason for a trade whose exit had to be abandoned.
ALTER TABLE public.trades ADD COLUMN IF NOT EXISTS rejection_reason text;

CREATE TABLE IF NOT EXISTS public.order_rejections (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    trade_id         uuid        REFERENCES public.trades (id) ON DELETE CASCADE,
    phase            text        NOT NULL CHECK (phase IN ('entry', 'exit')),
    leg              text        NOT NULL CHECK (leg IN ('long', 'short')),
    side             text        NOT NULL CHECK (side IN ('BUY', 'SELL')),
    attempt          integer     NOT NULL,
    broker_order_ref text,
    reason           text        NOT NULL,   -- broker's verdict text (or error)
    fatal            boolean     NOT NULL DEFAULT false, -- unwind failed: naked leg left at broker
    session_date     date        NOT NULL,   -- IST trading day (entry retry scope)
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_order_rejections_session
    ON public.order_rejections (phase, session_date);
CREATE INDEX IF NOT EXISTS idx_order_rejections_trade
    ON public.order_rejections (trade_id);

-- Same posture as the rest of the schema: RLS on, UI reads only, backend
-- (service_role) is the single writer.
ALTER TABLE public.order_rejections ENABLE ROW LEVEL SECURITY;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE tablename = 'order_rejections' AND policyname = 'read_authenticated') THEN
        CREATE POLICY read_authenticated ON public.order_rejections FOR SELECT TO authenticated USING (true);
    END IF;
END $$;
GRANT SELECT ON public.order_rejections TO authenticated;
