#!/usr/bin/env python3
"""
ODBSC_v6.py — Overnight Bull Call Spread Backtest Studio v6
(review-fix release)

Changes from v5, mapped to the audit findings:

SECURITY
  S1  Credentials removed from source. Angel One creds come from env vars
      (AO_API_KEY, AO_CLIENT_CODE, AO_MPIN, AO_TOTP_SECRET) or the UI;
      nothing is written to disk.
  S2  launch() guarded by __main__ and share=False by default.
  S3  _is_configured now means "credentials present", not "differ from
      defaults", and load_nifty returns the data source actually used, so
      the status bar cannot mislabel it.

BACKTEST LOGIC
  L1  Affordability gate: lots are capped at equity // (debit·lot_size);
      a bar that cannot fund one lot is skipped and counted.
  L2  The account halts permanently the first time equity <= 0 — no more
      post-blowout "zombie" trading.
  L3  EMA state machine is seeded one bar BEFORE the first tradeable bar,
      so a cross on the first bar registers.
  L4  Fractional day count: an overnight hold is charged
      calendar_days − 6h15m/24h of decay (0.740 d weekday, 2.740 d over a
      weekend), not whole calendar days.
  L5  Optional expiry calendar: entries use the listed weekly expiry
      nearest the DTE target instead of a synthetic constant-DTE contract.
      "Auto (NSE)" applies Thursday expiries before 2025-09-01 and Tuesday
      after (SEBI/NSE regime change). Exchange holidays are NOT modelled;
      expiries that fall on a holiday actually settle a day earlier.
  L6  round_to_step no longer uses banker's rounding.
  L7  optimize_path uses the data's end date for the projection horizon,
      not the wall clock.

PRICING / COSTS
  P1  IV model: sigma = HV·iv_mult + iv_add (variance-risk-premium proxy,
      default 1.10x) plus a linear skew adjustment per leg (vol points per
      1% moneyness). Entry and exit still share one sigma, so vega P&L is
      structurally zero — see LIMITATIONS.
  P2  Full Indian cost stack per round trip (4 legs): slippage per leg,
      flat brokerage per order, STT on sell-side premium, exchange
      transaction charges on premium turnover, GST, stamp duty on buys.
      Rates are configurable defaults as of mid-2026 — verify every rate
      against your own contract note before trusting a number.

SIZING
  K1  Kelly AGC now uses the continuous-outcome estimator
      f* = E[r]/Var[r] on per-trade return-on-premium (net of costs),
      scaled by the Kelly multiplier and hard-capped. The binary
      (bp − q)/b formula was a category error for non-binary P&L, and its
      30-sample estimate was mostly noise; the cap and the affordability
      gate bound the damage that estimation error can do.

METRICS
  M1  The equity/drawdown curve is seeded with initial capital, so losses
      before the first new high are measured (v5 hid them).
  M2  Added CAGR, Sharpe, Sortino, Calmar, per-trade t-stat, total costs,
      skipped-bar and halt diagnostics.

ANALYTICS
  A1  Panel-6 compounding fixed: exp(cumsum(log r)), not (1+log r).cumprod().
  A2  "Hawking" tab: the decay constant is now SIGNED and fitted on all
      days, so a profitable curve projects growth; the endogenous
      W(equity) regression is replaced by a win-rate-vs-time decay test
      with a p-value. The tab is explicitly a stylized diagnostic.
  A3  Path-to-Profitability no longer minimizes |projection − target|
      (target-seeking). It splits the window in-sample / out-of-sample,
      shortlists by IS Sharpe, selects by OOS Sharpe, and displays the
      IS→OOS degradation. The target is a reference line only.

LIMITATIONS THAT REMAIN (by construction, not oversight)
  - Prices are Black-Scholes values, not traded option quotes. No bid/ask
    ladder, no vega P&L (sigma is frozen within each trade), no intraday
    path, no early-exercise/assignment, no exchange-holiday calendar.
  - Results are therefore an upper bound on realism and are NOT evidence
    for live deployment. Validate against real chain data before sizing.
"""

import warnings; warnings.filterwarnings("ignore")
import base64, hashlib, hmac, json, math, os, struct, time
import datetime as dt

import numpy as np
import pandas as pd
from scipy import stats as _scipy_stats
from itertools import product as _product
from plotly.subplots import make_subplots
import plotly.graph_objects as go
import gradio as gr
import requests

try:
    import yfinance as yf
    _HAS_YF = True
except ImportError:
    _HAS_YF = False

# ═══════════════════════════════════════════════════════════════════════════════
#  Config — NO secrets in source. Set env vars or type them into the UI.        (S1)
# ═══════════════════════════════════════════════════════════════════════════════

DEFAULT_CONFIG = {
    "api_key":     os.environ.get("AO_API_KEY", ""),
    "client_code": os.environ.get("AO_CLIENT_CODE", ""),
    "mpin":        os.environ.get("AO_MPIN", ""),
    "totp_secret": os.environ.get("AO_TOTP_SECRET", ""),
    "base_url":    "https://apiconnect.angelbroking.com",
    "local_ip":    "127.0.0.1",
    "public_ip":   "",
    "mac_addr":    "",
    "underlying":  "NIFTY",
}

# Cost-model defaults (mid-2026 retail discount broker; VERIFY vs contract note)
COST_DEFAULTS = {
    "enable_costs":        True,
    "slippage_pts":        0.10,    # index points paid per leg (4 legs / round trip)
    "brokerage_per_order": 20.0,    # flat Rs per order, 4 orders / round trip
    "stt_pct":             0.10,    # % of sell-side premium
    "exch_pct":            0.035,   # % of total premium turnover (NSE options txn)
    "gst_pct":             18.0,    # % on (brokerage + exchange charges)
    "stamp_pct":           0.003,   # % of buy-side premium
}

NSE_EXPIRY_SWITCH = dt.date(2025, 9, 1)   # Thu weeklies before, Tue after (L5)

# ── Colour palette ─────────────────────────────────────────────────────────────
_G   = "#26a69a"
_R   = "#ef5350"
_Y   = "#ffeb3b"
_B   = "#42a5f5"
_O   = "#ffa726"
_P   = "#ab47bc"
_GR  = "#546e7a"
_BG  = "#0d1117"
_PAN = "#161b22"

_PLOT_LAYOUT = dict(
    template="plotly_dark",
    paper_bgcolor=_BG,
    plot_bgcolor=_PAN,
    font=dict(color="#e6edf3", size=11, family="Inter,sans-serif"),
    legend=dict(bgcolor=_PAN, bordercolor="#30363d", borderwidth=1, font_size=10),
    margin=dict(l=60, r=30, t=50, b=40),
    hoverlabel=dict(bgcolor=_PAN, bordercolor="#30363d"),
)


# ═══════════════════════════════════════════════════════════════════════════════
#  Angel One API helpers
# ═══════════════════════════════════════════════════════════════════════════════

def _totp_now(secret: str, period=30, digits=6) -> str:
    s = secret.strip().replace(" ", "").upper()
    s += "=" * (-len(s) % 8)
    key = base64.b32decode(s)
    counter = struct.pack(">Q", int(time.time()) // period)
    h = hmac.new(key, counter, hashlib.sha1).digest()
    off = h[-1] & 0x0F
    code = (struct.unpack(">I", h[off:off+4])[0] & 0x7FFFFFFF) % (10**digits)
    return str(code).zfill(digits)


def _ao_headers(jwt: str = None, cfg: dict = None) -> dict:
    c = cfg or DEFAULT_CONFIG
    h = {
        "Content-Type": "application/json",
        "Accept": "application/json",
        "X-UserType": "USER",
        "X-SourceID": "WEB",
        "X-ClientLocalIP": c.get("local_ip") or "127.0.0.1",
        "X-ClientPublicIP": c.get("public_ip") or c.get("local_ip") or "127.0.0.1",
        "X-MACAddress": c.get("mac_addr") or "00:00:00:00:00:00",
        "X-PrivateKey": c.get("api_key", ""),
    }
    if jwt:
        h["Authorization"] = f"Bearer {jwt}"
    return h


def _ao_login(cfg: dict) -> str:
    body = {
        "clientcode": cfg["client_code"],
        "password": cfg["mpin"],
        "totp": _totp_now(cfg["totp_secret"]),
    }
    r = requests.post(
        cfg["base_url"] + "/rest/auth/angelbroking/user/v1/loginByPassword",
        headers=_ao_headers(None, cfg), json=body, timeout=15
    ).json()
    if not r.get("status"):
        raise RuntimeError(f"Angel One login failed: {r.get('message', 'unknown')}")
    return r["data"]["jwtToken"]


def _ao_history(jwt: str, cfg: dict, token: str, exchange: str,
                from_date: str, to_date: str) -> list:
    body = {
        "exchange": exchange,
        "symboltoken": token,
        "interval": "ONE_DAY",
        "fromdate": from_date,
        "todate": to_date,
    }
    r = requests.post(
        cfg["base_url"] + "/rest/secure/angelbroking/historical/v1/getCandleData",
        headers=_ao_headers(jwt, cfg), json=body, timeout=30
    ).json()
    if not r.get("status") or r.get("data") is None:
        raise RuntimeError(f"History request failed: {r.get('message', 'no data')}")
    data = r["data"]
    if isinstance(data, dict):
        data = data.get("candleData", [])
    return data  # list of [timestamp, open, high, low, close, volume]


def _load_scrip_master(cfg: dict) -> list:
    cache = "angelone_scripmaster.json"
    if os.path.exists(cache) and time.time() - os.path.getmtime(cache) < 86400:
        with open(cache) as f:
            return json.load(f)
    url = "https://margincalculator.angelbroking.com/OpenAPI_File/files/OpenAPIScripMaster.json"
    data = requests.get(url, timeout=90).json()
    with open(cache, "w") as f:
        json.dump(data, f)
    return data


def fetch_nifty_ao(start: str, end: str, cfg: dict) -> pd.DataFrame:
    """Fetch Nifty daily OHLCV from Angel One historical API."""
    jwt = _ao_login(cfg)
    master = _load_scrip_master(cfg)
    idx = next(
        (r for r in master if r["exch_seg"] == "NSE"
         and r["symbol"].upper().replace(" ", "") in ("NIFTY50", "NIFTY")),
        None
    )
    token = idx["token"] if idx else "99926000"
    raw = _ao_history(jwt, cfg, token, "NSE",
                      f"{start} 09:15", f"{end} 15:30")
    if not raw:
        raise RuntimeError("Angel One returned no historical data.")
    records = []
    for row in raw:
        ts = row[0]
        if isinstance(ts, str):
            ts = ts.replace("T", " ").replace("Z", "")[:19]
            ts = pd.to_datetime(ts)
        records.append({
            "Date": ts,
            "Open": float(row[1]),
            "High": float(row[2]),
            "Low": float(row[3]),
            "Close": float(row[4]),
            "Volume": int(float(row[5])),
        })
    df = pd.DataFrame(records).sort_values("Date").set_index("Date")
    df.index = pd.to_datetime(df.index).tz_localize(None)
    df.dropna(subset=["Open", "Close"], inplace=True)
    df = df[~df.index.duplicated(keep="first")]
    return df


def _ao_is_configured(cfg: dict) -> bool:
    """Credentials PRESENT (S3) — v5 tested 'differs from defaults', which made
    shipped credentials unreachable and partial edits silently fall through."""
    return all(str(cfg.get(k, "")).strip()
               for k in ("api_key", "client_code", "mpin", "totp_secret"))


def load_nifty(start: str, end: str, cfg: dict):
    """Load Nifty data. Returns (df, source) so the UI reports the source
    actually used (S3) instead of inferring it from the api_key field."""
    if _ao_is_configured(cfg):
        try:
            df = fetch_nifty_ao(start, end, cfg)
            if len(df) >= 10:
                return df, "Angel One"
        except Exception as e:
            print(f"Angel One fetch failed ({e}), falling back to yfinance ...")
    if not _HAS_YF:
        raise RuntimeError("yfinance not available and Angel One not configured.")
    raw = yf.download("^NSEI", start=start, end=end, interval="1d",
                      progress=False, auto_adjust=True)
    if raw.empty:
        raise RuntimeError("yfinance returned no data. Check dates / internet.")
    if isinstance(raw.columns, pd.MultiIndex):
        raw.columns = raw.columns.get_level_values(0)
    df = raw[["Open", "High", "Low", "Close", "Volume"]].copy()
    df.index = pd.to_datetime(df.index).tz_localize(None)
    df.dropna(subset=["Open", "Close"], inplace=True)
    return df, "yfinance"


# ═══════════════════════════════════════════════════════════════════════════════
#  Pricing — Black-Scholes with an explicit IV model (P1)
# ═══════════════════════════════════════════════════════════════════════════════

_SQRT2 = math.sqrt(2.0)

def _nd(x: float) -> float:
    return 0.5 * (1.0 + math.erf(x / _SQRT2))

def bs_call(S, K, T, r, q, sigma):
    if T <= 0 or sigma <= 0:
        return max(0.0, S - K)
    srt = sigma * math.sqrt(T)
    d1 = (math.log(S / K) + (r - q + 0.5 * sigma * sigma) * T) / srt
    d2 = d1 - srt
    return S * math.exp(-q * T) * _nd(d1) - K * math.exp(-r * T) * _nd(d2)

def leg_sigma(sigma_atm: float, S: float, K: float, skew_pts: float) -> float:
    """Linear smile: skew_pts = vol POINTS added per +1% moneyness.
    Equity-index call wings typically trade slightly BELOW ATM vol, so a
    mildly negative skew makes the short OTM leg cheaper and the net debit
    higher — the direction reality pushes against a flat-vol model."""
    money_pct = (K / S - 1.0) * 100.0
    return max(0.02, sigma_atm + skew_pts * money_pct / 100.0)

def spread_legs(S, k1, k2, T, r, q, sigma_atm, skew_pts):
    c1 = bs_call(S, k1, T, r, q, leg_sigma(sigma_atm, S, k1, skew_pts))
    c2 = bs_call(S, k2, T, r, q, leg_sigma(sigma_atm, S, k2, skew_pts))
    return c1, c2

def spread_value(S, k1, k2, T, r, q, sigma_atm, skew_pts=0.0):
    c1, c2 = spread_legs(S, k1, k2, T, r, q, sigma_atm, skew_pts)
    return c1 - c2

def compute_hv(close: pd.Series, window: int = 20) -> pd.Series:
    return (np.log(close / close.shift(1))
              .rolling(window).std()
              .mul(np.sqrt(252))
              .fillna(0.15))

def round_to_step(price: float, step: int = 50) -> int:
    # (L6) v5 used int(round(...)) — Python banker's rounding sends x.5 to the
    # nearest EVEN multiple; floor(x + 0.5) is the conventional half-up.
    return int(math.floor(price / step + 0.5) * step)


# ═══════════════════════════════════════════════════════════════════════════════
#  Expiry calendar (L5) and day count (L4)
# ═══════════════════════════════════════════════════════════════════════════════

# Entry at the 15:30 close, exit at the next session's 09:15 open:
# elapsed = k calendar days − 6h15m.  k=1 → 0.740 d, k=3 (weekend) → 2.740 d.
_OVERNIGHT_GAP_DAYS = 6.25 / 24.0

def _nse_expiry_weekday(entry_date: dt.date) -> int:
    """NSE index weeklies: Thursday (3) before 2025-09-01, Tuesday (1) after."""
    return 3 if entry_date < NSE_EXPIRY_SWITCH else 1

def pick_expiry_dte(entry_date, target_dte: int, weekday: str = "Auto (NSE)",
                    min_dte: int = 2, max_weeks: int = 10) -> int:
    """Calendar days from entry to the listed weekly expiry closest to
    target_dte (never below min_dte). Exchange holidays are not modelled —
    a holiday expiry actually settles one session earlier."""
    d0 = entry_date.date() if hasattr(entry_date, "date") else entry_date
    wd_map = {"Monday": 0, "Tuesday": 1, "Wednesday": 2,
              "Thursday": 3, "Friday": 4}
    wd = _nse_expiry_weekday(d0) if weekday not in wd_map else wd_map[weekday]
    first = (wd - d0.weekday()) % 7
    best = None
    for w in range(max_weeks):
        cand = first + 7 * w
        if cand < min_dte:
            continue
        if best is None or abs(cand - target_dte) < abs(best - target_dte):
            best = cand
    return best if best is not None else max(min_dte, int(target_dte))


# ═══════════════════════════════════════════════════════════════════════════════
#  Transaction costs (P2) — cash charges; slippage is applied to leg prices
# ═══════════════════════════════════════════════════════════════════════════════

def trade_cash_costs(c1_in, c2_in, c1_out, c2_out, lot_size, n_lots, cfg) -> float:
    """Round trip = 4 orders: buy K1 / sell K2 at entry, sell K1 / buy K2 at
    exit. Premiums passed in are the SLIPPED (executed) prices."""
    if not cfg.get("enable_costs", True):
        return 0.0
    qty       = lot_size * n_lots
    sell_prem = max(0.0, c2_in + c1_out) * qty
    buy_prem  = max(0.0, c1_in + c2_out) * qty
    turnover  = sell_prem + buy_prem
    brk   = 4.0 * float(cfg.get("brokerage_per_order", 20.0))
    stt   = float(cfg.get("stt_pct", 0.10))   / 100.0 * sell_prem
    exch  = float(cfg.get("exch_pct", 0.035)) / 100.0 * turnover
    gst   = float(cfg.get("gst_pct", 18.0))   / 100.0 * (brk + exch)
    stamp = float(cfg.get("stamp_pct", 0.003))/ 100.0 * buy_prem
    return brk + stt + exch + gst + stamp


# ═══════════════════════════════════════════════════════════════════════════════
#  Kelly AGC (K1) — continuous-outcome estimator with hard caps
# ═══════════════════════════════════════════════════════════════════════════════

_KELLY_F_CAP = 0.60   # absolute ceiling on the capital fraction at risk

def kelly_fraction(returns_on_premium, kelly_mult: float) -> float:
    """f* ≈ E[r]/Var[r] for continuous outcomes, r = net P&L per rupee of
    premium at risk. Scaled by kelly_mult and capped. Returns 0 when the
    estimated edge is non-positive. NOTE: on a 30-trade window this
    estimator is still noisy — the cap and the affordability gate are what
    keep that noise from being able to sink the account."""
    r = np.asarray(returns_on_premium, dtype=float)
    if len(r) < 5:
        return 0.0
    mu  = float(r.mean())
    var = float(r.var(ddof=1))
    if var <= 1e-8 or mu <= 0:
        return 0.0
    return min(_KELLY_F_CAP, kelly_mult * mu / var)


# ═══════════════════════════════════════════════════════════════════════════════
#  Core backtest
# ═══════════════════════════════════════════════════════════════════════════════

def run_backtest(df: pd.DataFrame, cfg: dict):
    r, q        = cfg["risk_free_rate"], cfg["div_yield"]
    dte_target  = int(cfg["dte_entry"])
    step        = cfg["strike_step"]
    dist        = cfg["strike_dist_pct"] / 100.0
    base_ls     = int(cfg["lot_size"])
    fixed_lots  = int(cfg["lots"])
    dyn, fix    = cfg["use_dynamic_hv"], cfg["fixed_iv"]
    cap         = float(cfg["initial_capital"])
    win         = int(cfg["hv_window"])
    use_ema     = cfg.get("use_ema_filter", False)
    use_agc     = cfg.get("use_agc", False)
    kelly_mult  = float(cfg.get("kelly_mult", 0.5))
    agc_window  = int(cfg.get("agc_window", 30))
    max_lots    = int(cfg.get("max_lots", 10))
    iv_mult     = float(cfg.get("iv_mult", 1.10))
    iv_add      = float(cfg.get("iv_add", 0.0))
    skew_pts    = float(cfg.get("skew_pts", 0.0))
    use_cal     = bool(cfg.get("use_expiry_cal", True))
    exp_wd      = cfg.get("expiry_weekday", "Auto (NSE)")
    slip        = float(cfg.get("slippage_pts", 0.10)) if cfg.get("enable_costs", True) else 0.0
    has_ema     = "EMA" in df.columns

    trades = []
    equity = cap
    skipped_afford = 0
    skipped_ema    = 0
    halted         = False
    halt_date      = None

    # (L3) seed the EMA state machine one bar BEFORE the first tradeable bar,
    # so a cross occurring on bar `win` itself is detected.
    if use_ema and has_ema and win >= 1 and len(df) > win:
        _e0 = df["EMA"].iloc[win - 1]
        _p0 = float(df["Close"].iloc[win - 1])
        ema_prev_above = (not np.isnan(_e0)) and (_p0 >= float(_e0))
        ema_armed      = ema_prev_above
    else:
        ema_armed      = True
        ema_prev_above = True

    for i in range(win, len(df) - 1):
        entry_date = df.index[i]
        exit_date  = df.index[i + 1]
        entry_spot = float(df["Close"].iloc[i])
        exit_spot  = float(df["Open"].iloc[i + 1])

        if use_ema and has_ema:
            ema_val    = float(df["EMA"].iloc[i])
            curr_above = (not np.isnan(ema_val)) and (entry_spot >= ema_val)
            if curr_above and not ema_prev_above:
                ema_armed = True
            elif (not curr_above) and ema_prev_above:
                ema_armed = False
            ema_prev_above = curr_above
            if not ema_armed:
                skipped_ema += 1
                continue

        # ── volatility used for pricing (P1) ──────────────────────────────
        if dyn:
            hv = float(df["HV"].iloc[i - 1])            # lagged, causal
            if hv <= 0 or np.isnan(hv):
                hv = 0.15
            sigma_atm = hv * iv_mult + iv_add
        else:
            sigma_atm = float(fix)
        sigma_atm = min(1.50, max(0.03, sigma_atm))

        k1 = round_to_step(entry_spot, step)
        k2 = max(round_to_step(entry_spot * (1 + dist), step), k1 + step)

        # ── tenor (L5) and day count (L4) ──────────────────────────────────
        dte_eff = pick_expiry_dte(entry_date, dte_target, exp_wd) if use_cal \
                  else dte_target
        cal_days = (exit_date - entry_date).days
        elapsed  = max(0.05, cal_days - _OVERNIGHT_GAP_DAYS)
        t_in  = dte_eff / 365.0
        t_out = max(1e-4, (dte_eff - elapsed) / 365.0)

        # ── leg prices with slippage on all four executions ───────────────
        c1_in,  c2_in  = spread_legs(entry_spot, k1, k2, t_in,  r, q, sigma_atm, skew_pts)
        c1_out, c2_out = spread_legs(exit_spot,  k1, k2, t_out, r, q, sigma_atm, skew_pts)
        c1_in_x,  c2_in_x  = c1_in + slip,  max(0.0, c2_in - slip)   # buy worse / sell worse
        c1_out_x, c2_out_x = max(0.0, c1_out - slip), c2_out + slip  # sell worse / buy worse
        entry_debit = c1_in_x - c2_in_x
        exit_value  = c1_out_x - c2_out_x
        if entry_debit <= 0:
            continue

        # ── affordability gate (L1): premium is funded from equity ────────
        debit_rs_per_lot = entry_debit * base_ls
        affordable = int(equity // debit_rs_per_lot)
        if affordable < 1:
            skipped_afford += 1
            continue

        # ── sizing (K1) ────────────────────────────────────────────────────
        kelly_f = 0.0
        if use_agc and len(trades) >= agc_window:
            recent = [t["ret_risk"] for t in trades[-agc_window:]]
            kelly_f = kelly_fraction(recent, kelly_mult)
            desired = int(kelly_f * equity / debit_rs_per_lot) if kelly_f > 0 else 1
            desired = max(1, desired)
        else:
            desired = fixed_lots
        n_lots = max(1, min(desired, max_lots, affordable))

        cash_costs = trade_cash_costs(c1_in_x, c2_in_x, c1_out_x, c2_out_x,
                                      base_ls, n_lots, cfg)
        gross_rs = (exit_value - entry_debit) * base_ls * n_lots
        pnl_rs   = gross_rs - cash_costs
        pnl_lot  = pnl_rs / n_lots
        ret_risk = pnl_lot / debit_rs_per_lot        # net return per rupee of premium
        equity_before = equity
        equity  += pnl_rs

        trades.append({
            "entry_date"  : entry_date,
            "exit_date"   : exit_date,
            "entry_spot"  : entry_spot,
            "exit_spot"   : exit_spot,
            "k1"          : k1,
            "k2"          : k2,
            "dte_days"    : dte_eff,
            "sigma_pct"   : sigma_atm * 100,
            "entry_debit" : entry_debit,
            "exit_value"  : exit_value,
            "gross_rs"    : gross_rs,
            "costs_rs"    : cash_costs,
            "pnl_rs"      : pnl_rs,
            "ret"         : pnl_rs / equity_before if equity_before > 0 else 0.0,
            "ret_risk"    : ret_risk,
            "lots_used"   : n_lots,
            "kelly_f"     : kelly_f,
            "equity_before": equity_before,
            "equity"      : equity,
            "win"         : pnl_rs > 0,
        })

        # ── hard halt (L2): a dead account stays dead ──────────────────────
        if equity <= 0:
            halted, halt_date = True, exit_date
            break

    attrs = dict(skipped_afford=skipped_afford, skipped_ema=skipped_ema,
                 halted=halted, halt_date=halt_date, initial_capital=cap)

    if not trades:
        et = pd.DataFrame()
        et.index.name = "entry_date"
        et.attrs.update(attrs)
        eq = pd.DataFrame(columns=["equity", "drawdown", "drawdown_pct"])
        eq.attrs.update(attrs)
        return et, eq

    tdf = pd.DataFrame(trades).set_index("entry_date")
    tdf.index = pd.to_datetime(tdf.index)
    tdf.attrs.update(attrs)

    # (M1) seed the equity curve with initial capital so early drawdowns count
    seed_idx = tdf.index[0] - pd.Timedelta(days=1)
    eq_ser = pd.concat([pd.Series([cap], index=[seed_idx]), tdf["equity"]])
    edf = eq_ser.to_frame("equity")
    edf["drawdown"]     = edf["equity"].cummax() - edf["equity"]
    peak = edf["equity"].cummax().clip(lower=1e-9)
    edf["drawdown_pct"] = edf["drawdown"] / peak * 100
    edf.attrs.update(attrs)
    return tdf, edf


# ═══════════════════════════════════════════════════════════════════════════════
#  Performance metrics (M2)
# ═══════════════════════════════════════════════════════════════════════════════

_METRIC_KEYS = [
    "total_trades","win_count","loss_count","win_rate","net_pnl","net_pnl_pct",
    "avg_pnl","gross_profit","gross_loss","profit_factor","max_drawdown",
    "max_drawdown_pct","final_equity","avg_sigma_pct","max_profit_pct",
    "avg_lots","max_lots_used","avg_kelly_f","cagr_pct","sharpe","sortino",
    "calmar","tstat","total_costs","avg_cost","skipped_afford","skipped_ema",
    "halted","blowout_count","avg_dte",
]

def _annualisation(tdf: pd.DataFrame) -> float:
    if len(tdf) < 2:
        return 252.0
    yrs = max((tdf.index[-1] - tdf.index[0]).days, 7) / 365.25
    return max(1.0, len(tdf) / yrs)

def compute_metrics(tdf: pd.DataFrame, edf: pd.DataFrame, cfg: dict) -> dict:
    cap = float(cfg["initial_capital"])
    a = getattr(tdf, "attrs", {}) or {}
    if len(tdf) == 0:
        m = {k: 0 for k in _METRIC_KEYS}
        m.update(final_equity=cap,
                 skipped_afford=a.get("skipped_afford", 0),
                 skipped_ema=a.get("skipped_ema", 0),
                 halted=bool(a.get("halted", False)), blowout_count=0)
        return m
    t  = len(tdf)
    w  = int(tdf["win"].sum())
    gp = tdf.loc[tdf["win"], "pnl_rs"].sum()
    gl = tdf.loc[~tdf["win"], "pnl_rs"].sum()
    pf = (gp / abs(gl)) if gl else float("inf")
    eq = edf["equity"]
    final = float(eq.iloc[-1])
    yrs = max((tdf.index[-1] - tdf.index[0]).days, 7) / 365.25
    tpy = _annualisation(tdf)
    rets = tdf["ret"].values.astype(float)
    rf_pt = float(cfg.get("risk_free_rate", 0.06)) / tpy
    ex = rets - rf_pt
    sd = float(np.std(ex, ddof=1)) if t > 2 else 0.0
    sharpe = float(np.mean(ex) / sd * math.sqrt(tpy)) if sd > 1e-12 else 0.0
    dn = np.minimum(rets, 0.0)
    dsd = float(np.sqrt(np.mean(dn ** 2)))
    sortino = float(np.mean(ex) / dsd * math.sqrt(tpy)) if dsd > 1e-12 else 0.0
    mdd_pct = float(edf["drawdown_pct"].max())
    cagr = ((final / cap) ** (1.0 / yrs) - 1.0) * 100 if final > 0 else -100.0
    calmar = cagr / mdd_pct if mdd_pct > 1e-9 else 0.0
    sd_raw = float(np.std(rets, ddof=1)) if t > 2 else 0.0
    tstat = float(np.mean(rets) / (sd_raw / math.sqrt(t))) if sd_raw > 1e-12 else 0.0
    halted = bool(a.get("halted", False))
    return dict(
        total_trades     = t,
        win_count        = w,
        loss_count       = t - w,
        win_rate         = w / t * 100,
        net_pnl          = tdf["pnl_rs"].sum(),
        net_pnl_pct      = tdf["pnl_rs"].sum() / cap * 100,
        avg_pnl          = tdf["pnl_rs"].mean(),
        gross_profit     = gp,
        gross_loss       = gl,
        profit_factor    = pf,
        max_drawdown     = edf["drawdown"].max(),
        max_drawdown_pct = mdd_pct,
        final_equity     = final,
        avg_sigma_pct    = tdf["sigma_pct"].mean(),
        max_profit_pct   = max(0.0, (eq.max() - cap) / cap * 100),
        avg_lots         = tdf["lots_used"].mean(),
        max_lots_used    = int(tdf["lots_used"].max()),
        avg_kelly_f      = tdf["kelly_f"].mean() * 100,
        cagr_pct         = cagr,
        sharpe           = sharpe,
        sortino          = sortino,
        calmar           = calmar,
        tstat            = tstat,
        total_costs      = tdf["costs_rs"].sum(),
        avg_cost         = tdf["costs_rs"].mean(),
        skipped_afford   = a.get("skipped_afford", 0),
        skipped_ema      = a.get("skipped_ema", 0),
        halted           = halted,
        blowout_count    = int(halted),
        avg_dte          = tdf["dte_days"].mean(),
    )

def _slice_stats(sub: pd.DataFrame, rf_annual: float) -> dict:
    """Sharpe / win rate / PF on a trade slice (used by the optimizer)."""
    if len(sub) < 3:
        return dict(sharpe=0.0, win_rate=0.0, pf=0.0, n=len(sub))
    tpy = _annualisation(sub)
    rets = sub["ret"].values.astype(float)
    ex = rets - rf_annual / tpy
    sd = float(np.std(ex, ddof=1))
    sh = float(np.mean(ex) / sd * math.sqrt(tpy)) if sd > 1e-12 else 0.0
    gp = sub.loc[sub["win"], "pnl_rs"].sum()
    gl = sub.loc[~sub["win"], "pnl_rs"].sum()
    pf = (gp / abs(gl)) if gl else float("inf")
    return dict(sharpe=min(sh, 99.0), win_rate=sub["win"].mean() * 100,
                pf=pf, n=len(sub))


# ═══════════════════════════════════════════════════════════════════════════════
#  "Hawking" diagnostic (A2) — stylized, but no longer rigged
# ═══════════════════════════════════════════════════════════════════════════════

def _fmt_trade_days(t):
    if not np.isfinite(t) or t < 0:
        return "\u221e (no decay detected)"
    d = int(t)
    return f"~{d} trade-days \u2248 {d//21}m {d%21}d"

def compute_hawking_model(tdf: pd.DataFrame, edf: pd.DataFrame, cfg: dict) -> dict:
    cap = float(cfg["initial_capital"])
    if len(tdf) < 15 or len(edf) < 15:
        return {}
    eq_arr = edf["equity"].values.astype(float)
    dates  = edf.index.tolist()
    alpha  = cap
    hawking_T = alpha / np.maximum(eq_arr, 1.0)
    entropy   = (eq_arr / cap) ** 2

    # (A2) SIGNED decay constant fitted on ALL days. v5 fitted C on losing
    # days only, so E(t) = (E0^3 - 3Ct)^(1/3) could only fall — a profitable
    # curve was still "forecast" to evaporate. With the signed median,
    # C > 0 means net decay and C < 0 means net growth under the same ODE.
    diff = np.diff(eq_arr)
    if len(diff) >= 3:
        C_H = -float(np.median(diff * (eq_arr[:-1] ** 2)))
    else:
        C_H = 0.0
    decaying = C_H > 0
    sigma_d  = float(np.std(diff, ddof=1)) if len(diff) > 2 else cap * 0.01
    vp_rate  = abs(C_H) / (np.maximum(eq_arr, 1.0) * alpha)
    E_now    = float(eq_arr[-1])
    E_page   = cap / math.sqrt(2.0)
    avg_debit = float(tdf["entry_debit"].mean())
    E_info    = avg_debit * int(cfg.get("lot_size", 65))

    def _t_to_E(E_target):
        """Trade-days until E(t) reaches E_target — only defined on decay."""
        if not decaying or E_target >= E_now:
            return float("inf")
        return (E_now ** 3 - max(E_target, 0.0) ** 3) / (3.0 * C_H)

    t_page = _t_to_E(E_page)
    t_info = _t_to_E(E_info)
    t_evap = _t_to_E(0.0)
    page_status = ("already \u2264 Page level" if E_now <= E_page
                   else _fmt_trade_days(t_page) if decaying
                   else "n/a \u2014 curve growing")

    # (A2) Edge-decay test replaces the endogenous W(equity) fit: regress the
    # win indicator on TRADE ORDER. Equity is the cumulative sum of the same
    # wins, so W(equity) was a regression of Y on \u222bY; W(trade #) is not.
    n = len(tdf)
    x = np.arange(n, dtype=float)
    y = tdf["win"].values.astype(float)
    sl_w, ic_w, r_w, p_w, se_w = _scipy_stats.linregress(x, y)
    fitted_now = ic_w + sl_w * (n - 1)
    if sl_w < -1e-9 and p_w < 0.10:
        edge_rem = max(0.0, (0.5 - ic_w) / sl_w - (n - 1))
        if fitted_now <= 0.5:
            edge_rem = 0.0
    else:
        edge_rem = float("inf")
    WIN = min(30, max(5, n // 5))
    roll_wr = tdf["win"].rolling(WIN).mean()

    horizon = int(min(max(60, n), 600))
    t_fwd = np.linspace(0.0, horizon, 400)
    cube  = E_now ** 3 - 3.0 * C_H * t_fwd          # signed: grows if C_H < 0
    E_fwd = np.cbrt(np.maximum(cube, 0.0))
    band  = sigma_d * np.sqrt(t_fwd + 1.0)
    W_fwd = np.clip(ic_w + sl_w * ((n - 1) + t_fwd), 0.0, 1.0)

    return dict(
        dates=dates, eq_arr=eq_arr, hawking_T=hawking_T, entropy=entropy,
        vp_rate=vp_rate, C_H=C_H, decaying=decaying, sigma_d=sigma_d,
        alpha=alpha, cap=cap, E_now=E_now, E_page=E_page, E_info=E_info,
        t_page=t_page, t_info=t_info, t_evap=t_evap, page_status=page_status,
        wr_x=x, wr_roll=roll_wr, wr_win=WIN,
        w_slope=sl_w, w_icept=ic_w, w_p=p_w, w_r2=r_w ** 2,
        edge_rem=edge_rem, n_trades=n,
        t_fwd=t_fwd, E_fwd=E_fwd, band=band, W_fwd=W_fwd,
    )


# ═══════════════════════════════════════════════════════════════════════════════
#  Path-to-Profitability (A3) — walk-forward selection, target = reference only
# ═══════════════════════════════════════════════════════════════════════════════

def _lr_on_equity(equity_vals: np.ndarray):
    x = np.arange(len(equity_vals), dtype=float)
    sl, ic, r, _, se = _scipy_stats.linregress(x, equity_vals)
    resid_std = float(np.std(equity_vals - (sl * x + ic)))
    return float(sl), float(ic), float(r ** 2), resid_std

def _trading_days_to_year_end(ref_date: dt.date) -> int:
    # (L7) ref_date is the DATA end date, not the wall clock.
    year_end = dt.date(ref_date.year, 12, 31)
    cal_days = max(0, (year_end - ref_date).days)
    return max(1, int(cal_days * 252 / 365))

def optimize_path(df_full: pd.DataFrame, base_cfg: dict,
                  target_return_pct: float, lookback_days: int = 126) -> dict:
    """Grid search with an in-sample / out-of-sample split.

    v5 minimized |projected \u2212 target| over 700 configs on ~40 trades \u2014
    target-seeking on noise. v6: run each config once over the window, tag
    trades before the 60% split as IS and after as OOS, shortlist the top 20
    by IS Sharpe, then select by OOS Sharpe. The IS\u2192OOS degradation is
    reported so overfitting is visible instead of hidden. The annual target
    only draws a reference line."""
    cap = float(base_cfg["initial_capital"])
    target = cap * (1 + target_return_pct / 100.0)
    end_date = df_full.index[-1].date()
    rem_days = _trading_days_to_year_end(end_date)
    window = df_full.iloc[-min(int(lookback_days), len(df_full)):].copy()
    if len(window) < 60:
        return {}
    split_date = window.index[int(len(window) * 0.6)]
    rf = float(base_cfg.get("risk_free_rate", 0.06))

    otm_grid   = [0.5, 0.75, 1.0, 1.25, 1.5, 2.0, 2.5]
    dte_grid   = [7, 10, 14, 21, 30]
    ema_grid   = [0, 20, 35, 55, 100]
    kelly_grid = [0.0, 0.25, 0.5, 0.75]

    cands = []
    for otm, dte, ema_p, km in _product(otm_grid, dte_grid, ema_grid, kelly_grid):
        cfg2 = {**base_cfg,
                "strike_dist_pct": otm, "dte_entry": dte,
                "use_ema_filter": ema_p > 0, "ema_period": max(10, int(ema_p)),
                "use_agc": km > 0, "kelly_mult": km if km > 0 else 0.5}
        df2 = window.copy()
        df2["HV"] = compute_hv(df2["Close"], cfg2["hv_window"])
        if ema_p > 0:
            df2["EMA"] = df2["Close"].ewm(span=int(ema_p), adjust=False).mean()
        elif "EMA" in df2.columns:
            df2 = df2.drop(columns=["EMA"])
        try:
            t2, e2 = run_backtest(df2, cfg2)
        except Exception:
            continue
        if len(t2) < 15 or t2.attrs.get("halted"):
            continue
        is_t  = t2[t2.index <  split_date]
        oos_t = t2[t2.index >= split_date]
        if len(is_t) < 8 or len(oos_t) < 5:
            continue
        cands.append(dict(
            params=dict(otm=otm, dte=dte, ema_p=int(ema_p), km=km,
                        use_ema=ema_p > 0, use_agc=km > 0),
            s_is=_slice_stats(is_t, rf), s_oos=_slice_stats(oos_t, rf),
            tdf=t2, edf=e2, cfg=cfg2))
    if not cands:
        return {}

    cands.sort(key=lambda c: -c["s_is"]["sharpe"])
    short = cands[:20]
    best = max(short, key=lambda c: (c["s_oos"]["sharpe"],
                                     min(c["s_oos"]["pf"], 99.0)))

    e2 = best["edf"]
    oos_eq = e2["equity"][e2.index >= split_date]
    if len(oos_eq) < 5:
        oos_eq = e2["equity"]
    sl, ic, r2, resid = _lr_on_equity(oos_eq.values.astype(float))
    projected = float(oos_eq.iloc[-1]) + sl * rem_days

    return dict(
        best_params=best["params"],
        is_stats=best["s_is"], oos_stats=best["s_oos"],
        best_metrics=compute_metrics(best["tdf"], best["edf"], best["cfg"]),
        lr_slope=sl, lr_intercept=ic, lr_r2=r2, lr_resid_std=resid,
        projected=projected, target=target, remaining_days=rem_days,
        split_date=split_date, window_equity=e2["equity"], oos_equity=oos_eq,
        n_tested=len(cands), n_short=len(short),
    )


# ═══════════════════════════════════════════════════════════════════════════════
#  Data cache — keyed on range AND credentials; remembers the real source (S3)
# ═══════════════════════════════════════════════════════════════════════════════

_CACHE: dict = {"df": None, "key": None, "source": None}

def _get_df(start, end, hv_win, ema_period, ao_cfg):
    key = (start, end, ao_cfg.get("api_key", ""), ao_cfg.get("client_code", ""))
    if _CACHE["df"] is None or _CACHE["key"] != key:
        df, src = load_nifty(start, end, ao_cfg)
        _CACHE.update(df=df, key=key, source=src)
    df = _CACHE["df"].copy()
    df["HV"]  = compute_hv(df["Close"], int(hv_win))
    df["EMA"] = df["Close"].ewm(span=int(ema_period), adjust=False).mean()
    return df, _CACHE["source"]


# ═══════════════════════════════════════════════════════════════════════════════
#  Plotly chart builders
# ═══════════════════════════════════════════════════════════════════════════════

def _fig_dashboard(df_src, tdf, edf, cfg, m) -> go.Figure:
    use_agc = cfg.get("use_agc", False)
    use_ema = cfg.get("use_ema_filter", False)
    ema_period = cfg.get("ema_period", 55)
    cap = float(cfg["initial_capital"])
    has_trades = len(tdf) > 0
    has_edf = len(edf) > 0

    fig = make_subplots(
        rows=4, cols=2,
        specs=[
            [{"colspan": 2, "secondary_y": True}, None],
            [{"colspan": 2}, None],
            [{}, {}],
            [{"secondary_y": True}, {}],
        ],
        subplot_titles=[
            "Equity Curve vs Buy & Hold (net of costs)",
            "Drawdown (%)  \u2014  measured from initial capital",
            "P&L Distribution (net)",
            "Monthly P&L Heatmap (\u20b9)",
            "Rolling 60-Trade Win Rate  \u00b7  Kelly f",
            "Overnight Drift + Buy & Hold + EMA  (exp\u2218cumsum \u2014 fixed)",
        ],
        row_heights=[0.28, 0.14, 0.30, 0.28],
        vertical_spacing=0.07, horizontal_spacing=0.09,
    )

    # Panel 1 — Equity Curve
    if has_edf:
        dates = list(edf.index)
        equity_vals = list(edf["equity"])
        fig.add_trace(go.Scatter(
            x=dates, y=[max(v, cap) for v in equity_vals],
            fill="tonexty", mode="none",
            fillcolor="rgba(38,166,154,0.18)", showlegend=False, hoverinfo="skip",
        ), row=1, col=1, secondary_y=False)
        fig.add_trace(go.Scatter(
            x=dates, y=[cap]*len(dates),
            mode="none", showlegend=False, hoverinfo="skip",
        ), row=1, col=1, secondary_y=False)
        fig.add_trace(go.Scatter(
            x=dates, y=[min(v, cap) for v in equity_vals],
            fill="tonexty", mode="none",
            fillcolor="rgba(239,83,80,0.18)", showlegend=False, hoverinfo="skip",
        ), row=1, col=1, secondary_y=False)
        fig.add_trace(go.Scatter(
            x=dates, y=[cap]*len(dates),
            mode="none", showlegend=False, hoverinfo="skip",
        ), row=1, col=1, secondary_y=False)
        fig.add_trace(go.Scatter(
            x=dates, y=equity_vals,
            mode="lines", line=dict(color=_G, width=2),
            name="Strategy Equity (net)",
            hovertemplate="<b>%{x|%d %b %Y}</b><br>Equity: \u20b9%{y:,.0f}<extra></extra>",
        ), row=1, col=1, secondary_y=False)
        fig.add_hline(y=cap, line_dash="dot", line_color=_GR, line_width=1, row=1, col=1)
        if has_trades:
            try:
                anchor = tdf.index[0]
                bah = (df_src["Close"].loc[anchor:] /
                       float(df_src["Close"].loc[anchor]) * cap)
                fig.add_trace(go.Scatter(
                    x=list(bah.index), y=list(bah.values),
                    mode="lines", line=dict(color=_B, width=1.5, dash="dash"),
                    name="Buy & Hold",
                    hovertemplate="<b>%{x|%d %b %Y}</b><br>B&H: \u20b9%{y:,.0f}<extra></extra>",
                ), row=1, col=1, secondary_y=False)
            except Exception:
                pass
        if m.get("halted") and has_trades:
            fig.add_trace(go.Scatter(
                x=[tdf.index[-1]], y=[float(tdf["equity"].iloc[-1])],
                mode="markers+text",
                marker=dict(symbol="x", size=14, color=_Y),
                text=["HALTED"], textposition="top center",
                textfont=dict(color=_Y, size=10),
                name="Account halted (equity \u2264 0)",
                hovertemplate="<b>HALTED</b><br>%{x|%d %b %Y}<extra></extra>",
            ), row=1, col=1, secondary_y=False)
        if use_agc and has_trades:
            fig.add_trace(go.Scatter(
                x=list(tdf.index), y=list(tdf["lots_used"]),
                mode="lines", line=dict(color=_P, width=1, dash="dot"),
                name="Lots Used (AGC)", opacity=0.8,
                hovertemplate="<b>%{x|%d %b %Y}</b><br>Lots: %{y}<extra></extra>",
            ), row=1, col=1, secondary_y=True)
            fig.update_yaxes(title_text="Lots Used", secondary_y=True, row=1, col=1, color=_P)
    fig.update_yaxes(title_text="\u20b9 Equity", tickprefix="\u20b9",
                     tickformat=",.0f", secondary_y=False, row=1, col=1)

    # Panel 2 — Drawdown (now includes the capital-seeded start, M1)
    if has_edf:
        dd = -edf["drawdown_pct"]
        fig.add_trace(go.Scatter(
            x=list(edf.index), y=list(dd),
            fill="tozeroy", mode="lines",
            fillcolor="rgba(239,83,80,0.35)", line=dict(color=_R, width=1),
            name="Drawdown %",
            hovertemplate="<b>%{x|%d %b %Y}</b><br>DD: %{y:.2f}%<extra></extra>",
        ), row=2, col=1)
    fig.update_yaxes(title_text="% Drawdown", ticksuffix="%", row=2, col=1)

    # Panel 3 — P&L Distribution
    if has_trades:
        wins_vals   = tdf.loc[tdf["win"], "pnl_rs"].tolist()
        losses_vals = tdf.loc[~tdf["win"], "pnl_rs"].tolist()
        fig.add_trace(go.Histogram(
            x=wins_vals, nbinsx=40, marker_color=_G, opacity=0.75,
            name=f"Wins ({m['win_count']})",
            hovertemplate="\u20b9%{x:,.0f}<br>Count: %{y}<extra></extra>",
        ), row=3, col=1)
        fig.add_trace(go.Histogram(
            x=losses_vals, nbinsx=40, marker_color=_R, opacity=0.75,
            name=f"Losses ({m['loss_count']})",
            hovertemplate="\u20b9%{x:,.0f}<br>Count: %{y}<extra></extra>",
        ), row=3, col=1)
        fig.add_vline(x=0, line_dash="dash", line_color="white", line_width=1, row=3, col=1)
        fig.add_vline(x=m["avg_pnl"], line_dash="dot", line_color=_Y, line_width=1.5,
                       row=3, col=1, annotation_text=f"Avg \u20b9{m['avg_pnl']:,.0f}",
                       annotation_font_color=_Y, annotation_position="top right")
    fig.update_xaxes(title_text="\u20b9 P&L", tickprefix="\u20b9", row=3, col=1)
    fig.update_yaxes(title_text="Count", row=3, col=1)
    fig.update_layout(barmode="overlay")

    # Panel 4 — Monthly Heatmap
    if has_trades:
        try:
            monthly = tdf["pnl_rs"].resample("ME").sum()
            mdf = monthly.to_frame()
            mdf["year"] = mdf.index.year
            mdf["month"] = mdf.index.strftime("%b")
            pivot = mdf.pivot_table("pnl_rs", index="year", columns="month")
            mo = ["Jan","Feb","Mar","Apr","May","Jun",
                  "Jul","Aug","Sep","Oct","Nov","Dec"]
            pivot = pivot.reindex(columns=[c for c in mo if c in pivot.columns])
            zvals = pivot.values
            txt = [[f"\u20b9{int(v):,}" if not np.isnan(v) else "" for v in row] for row in zvals]
            zmax = float(np.nanmax(np.abs(zvals))) if not np.all(np.isnan(zvals)) else 1
            fig.add_trace(go.Heatmap(
                z=zvals, x=pivot.columns.tolist(), y=[str(y) for y in pivot.index.tolist()],
                text=txt, texttemplate="%{text}", textfont={"size": 9},
                colorscale=[[0, _R], [0.5, _Y], [1, _G]], zmid=0, zmin=-zmax, zmax=zmax,
                colorbar=dict(title="\u20b9", thickness=10, len=0.3, y=0.35, yanchor="middle"),
                hovertemplate="Year %{y}, %{x}: %{text}<extra></extra>", name="Monthly P&L",
            ), row=3, col=2)
        except Exception:
            pass

    # Panel 5 — Rolling Win Rate + Kelly f
    if has_trades and len(tdf) >= 60:
        roll_wr = (tdf["win"].rolling(60).mean() * 100).dropna()
        fig.add_trace(go.Scatter(
            x=list(roll_wr.index), y=list(roll_wr.values),
            mode="lines", line=dict(color=_Y, width=1.5),
            name="Rolling 60 Win Rate",
            hovertemplate="<b>%{x|%d %b %Y}</b><br>Win Rate: %{y:.1f}%<extra></extra>",
        ), row=4, col=1, secondary_y=False)
        fig.add_hline(y=50, line_dash="dot", line_color=_GR, line_width=1, row=4, col=1)
        fig.add_hline(y=m["win_rate"], line_dash="dash", line_color=_G, line_width=1,
                       row=4, col=1, annotation_text=f"Overall {m['win_rate']:.1f}%",
                       annotation_font_color=_G)
    if use_agc and has_trades:
        roll_kf = (tdf["kelly_f"] * 100).rolling(20).mean().dropna()
        if len(roll_kf):
            fig.add_trace(go.Scatter(
                x=list(roll_kf.index), y=list(roll_kf.values),
                mode="lines", line=dict(color=_P, width=1.2, dash="dot"),
                name="Kelly f (%)",
                hovertemplate="<b>%{x|%d %b %Y}</b><br>Kelly f: %{y:.2f}%<extra></extra>",
            ), row=4, col=1, secondary_y=True)
            fig.update_yaxes(title_text="Kelly f (%)", secondary_y=True, row=4, col=1, color=_P)
    fig.update_yaxes(title_text="Win Rate %", ticksuffix="%",
                     secondary_y=False, row=4, col=1, range=[20, 80])

    # Panel 6 — Overnight drift decomposition
    # (A1) v5 computed (1 + log_r).cumprod(): mixing log returns with simple
    # compounding biases every curve down by ~r^2/2 per bar. Correct identity:
    # growth = exp(cumsum(log_r)).
    log_oc    = np.log(df_src["Open"] / df_src["Close"].shift(1)).dropna()
    log_cc    = np.log(df_src["Close"] / df_src["Close"].shift(1)).dropna()
    log_intra = (log_cc - log_oc).dropna()
    cum_ov    = np.exp(log_oc.cumsum())
    cum_in    = np.exp(log_intra.cumsum())
    cum_bh    = np.exp(log_cc.cumsum())

    fig.add_trace(go.Scatter(
        x=list(cum_ov.index), y=list(cum_ov.values),
        mode="lines", line=dict(color=_G, width=1.5),
        name=f"Overnight (x{cum_ov.iloc[-1]:.2f})",
        hovertemplate="<b>%{x|%d %b %Y}</b><br> x%{y:.3f}<extra></extra>",
    ), row=4, col=2)
    fig.add_trace(go.Scatter(
        x=list(cum_in.index), y=list(cum_in.values),
        mode="lines", line=dict(color=_R, width=1.5),
        name=f"Intraday (x{cum_in.iloc[-1]:.2f})",
        hovertemplate="<b>%{x|%d %b %Y}</b><br> x%{y:.3f}<extra></extra>",
    ), row=4, col=2)
    fig.add_trace(go.Scatter(
        x=list(cum_bh.index), y=list(cum_bh.values),
        mode="lines", line=dict(color=_B, width=1.5, dash="dash"),
        name=f"Buy & Hold (x{cum_bh.iloc[-1]:.2f})",
        hovertemplate="<b>%{x|%d %b %Y}</b><br> x%{y:.3f}<extra></extra>",
    ), row=4, col=2)
    fig.add_hline(y=1.0, line_dash="dot", line_color=_GR, row=4, col=2)

    if "EMA" in df_src.columns:
        ema_s = df_src["EMA"].dropna()
        ema_n = ema_s / float(ema_s.iloc[0])
        fig.add_trace(go.Scatter(
            x=list(ema_n.index), y=list(ema_n.values),
            mode="lines", line=dict(color=_O, width=1.0, dash="dot"),
            name=f"{ema_period}-EMA (norm.)",
            hovertemplate="<b>%{x|%d %b %Y}</b><br>EMA x%{y:.3f}<extra></extra>",
        ), row=4, col=2)
        below = df_src["Close"] < df_src["EMA"]
        ymin = float(min(cum_ov.min(), cum_in.min(), cum_bh.min()) * 0.92)
        ymax = float(max(cum_ov.max(), cum_in.max(), cum_bh.max()) * 1.08)
        segments_x, segments_y = [], []
        in_seg = False
        seg_start = None
        for date, is_below in zip(df_src.index, below):
            if is_below and not in_seg:
                seg_start = date; in_seg = True
            elif not is_below and in_seg:
                segments_x += [seg_start, seg_start, date, date, None]
                segments_y += [ymin, ymax, ymax, ymin, None]
                in_seg = False
        if in_seg:
            segments_x += [seg_start, seg_start, df_src.index[-1], df_src.index[-1], None]
            segments_y += [ymin, ymax, ymax, ymin, None]
        if segments_x:
            fig.add_trace(go.Scatter(
                x=segments_x, y=segments_y, fill="toself",
                fillcolor="rgba(84,110,122,0.18)", line=dict(width=0),
                mode="lines", showlegend=True,
                name=f"Below {ema_period}-EMA", hoverinfo="skip",
            ), row=4, col=2)
        fig.update_yaxes(range=[ymin, ymax], row=4, col=2)
    fig.update_yaxes(title_text="Growth of Re1", row=4, col=2)

    badges = (f"  |  EMA{ema_period} {'ON' if use_ema else 'OFF'}"
              f"  |  AGC {'ON' if use_agc else 'OFF'}"
              f"  |  Costs {'ON' if cfg.get('enable_costs', True) else 'OFF'}"
              + ("  |  \u26a0 HALTED" if m.get("halted") else ""))
    title = (f"Overnight Bull Call Spread v6  \u00b7  "
             f"{m['total_trades']:,} trades  \u00b7  "
             f"Win {m['win_rate']:.1f}%  \u00b7  "
             f"Net \u20b9{m['net_pnl']:,.0f}  \u00b7  "
             f"Sharpe {m['sharpe']:.2f}  \u00b7  "
             f"PF {m['profit_factor']:.2f}"
             + badges)
    fig.update_layout(**_PLOT_LAYOUT, height=1300,
                      title=dict(text=title, font_size=13, x=0.01),
                      hovermode="x unified")
    return fig


def _fig_otm_sensitivity(sens_df: pd.DataFrame) -> go.Figure:
    fig = make_subplots(rows=1, cols=3,
        subplot_titles=["Net P&L (\u20b9) vs OTM %",
                        "Sharpe vs OTM %",
                        "Max Drawdown (%) vs OTM %"],
        horizontal_spacing=0.1)
    labels = sens_df["OTM %"].astype(str).tolist()
    clrs = [_G if v >= 0 else _R for v in sens_df["Net P&L (\u20b9)"]]
    fig.add_trace(go.Bar(x=labels, y=sens_df["Net P&L (\u20b9)"],
                         marker_color=clrs, opacity=0.85, name="Net P&L",
                         hovertemplate="OTM %{x}%<br>P&L: \u20b9%{y:,.0f}<extra></extra>"),
                  row=1, col=1)
    fig.add_trace(go.Bar(x=labels, y=sens_df["Sharpe"],
                         marker_color=_Y, opacity=0.85, name="Sharpe",
                         hovertemplate="OTM %{x}%<br>Sharpe: %{y:.2f}<extra></extra>"),
                  row=1, col=2)
    fig.add_hline(y=0, line_dash="dot", line_color=_GR, row=1, col=2)
    fig.add_trace(go.Bar(x=labels, y=sens_df["Max DD (%)"],
                         marker_color=_R, opacity=0.85, name="Max DD %",
                         hovertemplate="OTM %{x}%<br>Max DD: %{y:.1f}%<extra></extra>"),
                  row=1, col=3)
    fig.update_yaxes(tickprefix="\u20b9", tickformat=",.0f", row=1, col=1)
    fig.update_yaxes(ticksuffix="%", row=1, col=3)
    fig.update_layout(**_PLOT_LAYOUT, height=400,
                      title="OTM Distance Sensitivity (net of costs)")
    return fig


def _fig_ema_sensitivity(ema_df: pd.DataFrame) -> go.Figure:
    fig = make_subplots(rows=1, cols=2,
        subplot_titles=["Max Profit % / Max DD % Ratio vs EMA Period",
                        "Max Profit % and Max DD % vs EMA Period"],
        horizontal_spacing=0.1)
    x = ema_df["EMA Period"].tolist()
    r = ema_df["P/DD Ratio"].tolist()
    fig.add_trace(go.Scatter(x=x, y=r, mode="lines+markers",
        line=dict(color=_Y, width=2), marker=dict(size=6, color=_Y),
        name="P/DD Ratio",
        hovertemplate="EMA %{x}d<br>Ratio: %{y:.3f}<extra></extra>"), row=1, col=1)
    fig.add_hline(y=1.0, line_dash="dot", line_color=_GR,
                   annotation_text="Ratio = 1.0", annotation_font_color=_GR, row=1, col=1)
    if r:
        best_i = int(np.argmax(r))
        fig.add_annotation(x=x[best_i], y=r[best_i],
            text=f"Best: {x[best_i]}d<br>Ratio={r[best_i]:.2f}",
            showarrow=True, arrowhead=2, arrowcolor=_Y, font_color=_Y,
            bgcolor=_PAN, bordercolor=_Y, row=1, col=1)
    fig.add_trace(go.Scatter(x=x, y=ema_df["Max Profit %"].tolist(),
        mode="lines+markers", line=dict(color=_G, width=1.8),
        marker=dict(size=5, color=_G), name="Max Profit %",
        hovertemplate="EMA %{x}d<br>Max Profit: %{y:.1f}%<extra></extra>"), row=1, col=2)
    fig.add_trace(go.Scatter(x=x, y=ema_df["Max DD %"].tolist(),
        mode="lines+markers", line=dict(color=_R, width=1.8),
        marker=dict(size=5, color=_R), name="Max DD %",
        hovertemplate="EMA %{x}d<br>Max DD: %{y:.1f}%<extra></extra>"), row=1, col=2)
    fig.update_yaxes(ticksuffix="%", row=1, col=1)
    fig.update_yaxes(ticksuffix="%", row=1, col=2)
    fig.update_xaxes(title_text="EMA Period (days)", row=1, col=1)
    fig.update_xaxes(title_text="EMA Period (days)", row=1, col=2)
    fig.update_layout(**_PLOT_LAYOUT, height=430,
                      title="EMA Filter Sensitivity - Profit / Drawdown Trade-off")
    return fig


def _fig_hawking(h: dict) -> go.Figure:
    if not h:
        f = go.Figure()
        f.update_layout(**_PLOT_LAYOUT,
                        title="Need at least 15 trades \u2014 extend the date range")
        return f
    dates = h["dates"]; eq = h["eq_arr"]; cap = h["cap"]
    direction = "DECAYING" if h["decaying"] else "GROWING"

    fig = make_subplots(rows=4, cols=1,
        specs=[[{"secondary_y": True}], [{"secondary_y": True}], [{}],
               [{"secondary_y": True}]],
        subplot_titles=[
            "Event Horizon (Equity) + Hawking Temp T_H = a/E   [stylized]",
            "Bekenstein-Hawking Entropy S ~ E^2 + |C|/(E\u00b7a)   [stylized]",
            "Edge-Decay Test: rolling win rate vs TRADE ORDER (OLS + p-value)",
            f"Signed Evaporation Forecast \u2014 drift-consistent C ({direction})",
        ], row_heights=[0.27, 0.18, 0.28, 0.27], vertical_spacing=0.07)

    # Panel 1
    fig.add_trace(go.Scatter(x=dates, y=list(eq), mode="lines",
        line=dict(color=_G, width=2.2), name="Equity (Mass)",
        fill="tozeroy", fillcolor="rgba(38,166,154,0.07)",
        hovertemplate="<b>%{x|%d %b %Y}</b><br>E = \u20b9%{y:,.0f}<extra></extra>"),
        row=1, col=1, secondary_y=False)
    for E_lv, lbl, clr in [(cap, "Initial capital", _GR),
                           (h["E_page"], "Page level E0/\u221a2", _O),
                           (h["E_info"], "1-lot premium floor", _P)]:
        if E_lv and 0 < E_lv < max(eq) * 1.4:
            fig.add_shape(type="line", x0=0, x1=1, xref="x domain",
                y0=E_lv, y1=E_lv, line=dict(color=clr, dash="dot"), row=1, col=1)
            fig.add_annotation(x=0.99, y=E_lv, xref="x domain",
                text=f"{lbl} \u20b9{E_lv:,.0f}", showarrow=False,
                font=dict(color=clr, size=9), xanchor="right", yanchor="bottom",
                row=1, col=1)
    fig.add_trace(go.Scatter(x=dates, y=list(h["hawking_T"]), mode="lines",
        line=dict(color=_R, width=1.2, dash="dot"), name="T_H = a/E",
        opacity=0.85, hovertemplate="T_H = %{y:.4f}<extra></extra>"),
        row=1, col=1, secondary_y=True)
    fig.update_yaxes(title_text="\u20b9 Equity", tickprefix="\u20b9",
        tickformat=",.0f", secondary_y=False, row=1, col=1)
    fig.update_yaxes(title_text="T_H", secondary_y=True, row=1, col=1,
        tickfont_color=_R, title_font_color=_R)

    # Panel 2
    fig.add_trace(go.Scatter(x=dates, y=list(h["entropy"]), mode="lines",
        line=dict(color=_B, width=1.8), fill="tozeroy",
        fillcolor="rgba(66,165,245,0.10)", name="Entropy S ~ E^2",
        hovertemplate="S = %{y:.4f}<extra></extra>"),
        row=2, col=1, secondary_y=False)
    fig.add_trace(go.Scatter(x=dates, y=list(h["vp_rate"]), mode="lines",
        line=dict(color=_R, width=1.0, dash="dash"), name="|C|/(E\u00b7a)",
        opacity=0.80, hovertemplate="%{y:.6f}<extra></extra>"),
        row=2, col=1, secondary_y=True)
    fig.update_yaxes(title_text="S (norm.)", secondary_y=False, row=2, col=1)
    fig.update_yaxes(title_text="|C|/(E\u00b7a)", secondary_y=True, row=2, col=1,
        tickfont_color=_R, title_font_color=_R)

    # Panel 3 — the honest edge test
    roll = h["wr_roll"].dropna()
    if len(roll):
        fig.add_trace(go.Scatter(
            x=list(np.arange(len(h["wr_x"]))[h["wr_roll"].notna().values]),
            y=list(roll.values),
            mode="lines", line=dict(color=_Y, width=1.5),
            name=f"Rolling {h['wr_win']}-trade win rate",
            hovertemplate="trade %{x}<br>W = %{y:.1%}<extra></extra>"), row=3, col=1)
    xfit = np.array([0, h["n_trades"] - 1], dtype=float)
    yfit = h["w_icept"] + h["w_slope"] * xfit
    fig.add_trace(go.Scatter(x=list(xfit), y=list(np.clip(yfit, 0, 1)),
        mode="lines", line=dict(color=_P, width=2.5),
        name=(f"OLS: slope {h['w_slope']*100:+.3f} pp/trade, "
              f"p={h['w_p']:.3f}"),
        hovertemplate="W(fit) = %{y:.1%}<extra></extra>"), row=3, col=1)
    fig.add_shape(type="line", x0=0, x1=1, xref="x domain",
        y0=0.50, y1=0.50, line=dict(color="white", dash="dash", width=1.4),
        row=3, col=1)
    if np.isfinite(h["edge_rem"]):
        fig.add_annotation(x=0.99, y=0.06, xref="x domain", yref="y",
            text=f"fitted W hits 50% in ~{int(h['edge_rem'])} trades",
            showarrow=False, font=dict(color=_R, size=10), xanchor="right",
            row=3, col=1)
    else:
        fig.add_annotation(x=0.99, y=0.06, xref="x domain", yref="y",
            text="no statistically significant decay (p \u2265 0.10 or slope \u2265 0)",
            showarrow=False, font=dict(color=_G, size=10), xanchor="right",
            row=3, col=1)
    fig.update_xaxes(title_text="Trade # (order)", row=3, col=1)
    fig.update_yaxes(title_text="Win Rate", tickformat=".0%", row=3, col=1,
                     range=[0.0, 1.0])

    # Panel 4 — signed forecast
    t_fwd = h["t_fwd"]; E_fwd = h["E_fwd"]; band = h["band"]
    ci_hi = E_fwd + band
    ci_lo = np.maximum(E_fwd - band, 0.0)
    fig.add_trace(go.Scatter(x=list(t_fwd)+list(t_fwd[::-1]),
        y=list(ci_hi)+list(ci_lo[::-1]), fill="toself", mode="none",
        fillcolor="rgba(38,166,154,0.10)", showlegend=True,
        name="\u00b11\u03c3\u00b7\u221at band", hoverinfo="skip"),
        row=4, col=1, secondary_y=False)
    fig.add_trace(go.Scatter(x=list(t_fwd), y=list(E_fwd), mode="lines",
        line=dict(color=_G if not h["decaying"] else _R, width=2),
        name="E(t) = cbrt(E0\u00b3 \u2212 3Ct), C signed",
        hovertemplate="t+%{x:.0f}d<br>E=\u20b9%{y:,.0f}<extra></extra>"),
        row=4, col=1, secondary_y=False)
    fig.add_trace(go.Scatter(x=list(t_fwd), y=list(h["W_fwd"]), mode="lines",
        line=dict(color=_Y, width=1.5, dash="dot"), name="W(t) from OLS",
        hovertemplate="t+%{x:.0f}d<br>W=%{y:.1%}<extra></extra>"),
        row=4, col=1, secondary_y=True)
    fig.add_trace(go.Scatter(x=[float(t_fwd[0]), float(t_fwd[-1])],
        y=[0.50, 0.50], mode="lines", line=dict(color="white", dash="dash"),
        showlegend=False, hoverinfo="skip"),
        row=4, col=1, secondary_y=True)
    fig.update_yaxes(title_text="Projected \u20b9 Equity", tickprefix="\u20b9",
        tickformat=",.0f", secondary_y=False, row=4, col=1)
    fig.update_yaxes(title_text="Predicted Win Rate", tickformat=".0%",
        secondary_y=True, row=4, col=1, tickfont_color=_Y,
        title_font_color=_Y, range=[0.0, 1.0])
    fig.update_xaxes(title_text="Trade-days from now", row=4, col=1)
    fig.update_layout(**_PLOT_LAYOUT, height=1150,
        title=("Hawking-Style Diagnostics [stylized visual metaphor \u2014 "
               "the edge test in panel 3 is the statistical content]"),
        hovermode="x unified")
    return fig


def _fig_path(full_edf, res: dict, cfg: dict) -> go.Figure:
    if not res:
        fig = go.Figure()
        fig.update_layout(**_PLOT_LAYOUT, title="No optimisation result yet")
        return fig
    cap = float(cfg["initial_capital"])
    target = res["target"]; rem = res["remaining_days"]
    sl = res["lr_slope"]; ic = res["lr_intercept"]
    r2 = res["lr_r2"]; resid = res["lr_resid_std"]
    w_eq = res["window_equity"]; o_eq = res["oos_equity"]
    proj = res["projected"]; split = res["split_date"]
    mp = res["best_params"]; si = res["is_stats"]; so = res["oos_stats"]

    last_date = o_eq.index[-1]
    proj_dates = [last_date + dt.timedelta(days=int(d * 1.4484)) for d in range(1, rem + 1)]
    proj_eq = [float(o_eq.iloc[-1]) + sl * d for d in range(1, rem + 1)]

    fig = make_subplots(rows=3, cols=1,
        subplot_titles=[
            "Window Equity: In-Sample | Out-of-Sample split + OOS projection",
            "OOS Detail + LR Channel (fit is OOS-only)",
            "Rolling 21-Day Equity Slope (window)"],
        row_heights=[0.50, 0.28, 0.22], vertical_spacing=0.08)

    if full_edf is not None and len(full_edf) > 0:
        fig.add_trace(go.Scatter(x=list(full_edf.index), y=list(full_edf["equity"]),
            mode="lines", line=dict(color=_GR, width=1), opacity=0.45,
            name="Full History (base params)",
            hovertemplate="<b>%{x|%d %b %Y}</b><br>\u20b9%{y:,.0f}<extra></extra>"),
            row=1, col=1)
    is_eq = w_eq[w_eq.index < split]
    fig.add_trace(go.Scatter(x=list(is_eq.index), y=list(is_eq.values),
        mode="lines", line=dict(color=_B, width=2), name="In-sample (fit/shortlist)",
        hovertemplate="<b>%{x|%d %b %Y}</b><br>\u20b9%{y:,.0f}<extra></extra>"),
        row=1, col=1)
    fig.add_trace(go.Scatter(x=list(o_eq.index), y=list(o_eq.values),
        mode="lines", line=dict(color=_G, width=2), name="Out-of-sample (selection)",
        hovertemplate="<b>%{x|%d %b %Y}</b><br>\u20b9%{y:,.0f}<extra></extra>"),
        row=1, col=1)
    fig.add_vline(x=split, line_dash="dash", line_color=_O, line_width=1.2,
                  row=1, col=1, annotation_text="IS | OOS split",
                  annotation_font_color=_O)
    lr_y = [ic + sl * d for d in range(len(o_eq))]
    fig.add_trace(go.Scatter(x=list(o_eq.index), y=lr_y,
        mode="lines", line=dict(color=_Y, width=1.5, dash="dash"),
        name=f"OOS LR fit (R\u00b2={r2:.2f})",
        hovertemplate="LR: \u20b9%{y:,.0f}<extra></extra>"), row=1, col=1)
    for mult, alpha in [(2, 0.08), (1, 0.18)]:
        up = [float(o_eq.iloc[-1]) + sl*d + mult*resid*np.sqrt(d+1) for d in range(rem)]
        dn = [float(o_eq.iloc[-1]) + sl*d - mult*resid*np.sqrt(d+1) for d in range(rem)]
        fig.add_trace(go.Scatter(x=proj_dates+proj_dates[::-1],
            y=up+dn[::-1], fill="toself", mode="none",
            fillcolor=f"rgba(66,165,245,{alpha})",
            showlegend=True, name=f"\u00b1{mult}\u03c3\u00b7\u221at", hoverinfo="skip"),
            row=1, col=1)
    fig.add_trace(go.Scatter(x=proj_dates, y=proj_eq,
        mode="lines", line=dict(color=_B, width=2),
        name=f"OOS-slope projection \u2192 \u20b9{proj:,.0f}",
        hovertemplate="<b>%{x|%d %b %Y}</b><br>Proj \u20b9%{y:,.0f}<extra></extra>"),
        row=1, col=1)
    fig.add_hline(y=target, line_dash="dot", line_color=_O,
                  annotation_text=f"Target \u20b9{target:,.0f} (reference only \u2014 not optimised for)",
                  annotation_font_color=_O, annotation_position="bottom right",
                  row=1, col=1)
    fig.add_hline(y=cap, line_dash="dot", line_color=_GR, row=1, col=1)

    fig.add_trace(go.Scatter(x=list(o_eq.index), y=list(o_eq.values),
        mode="lines", line=dict(color=_G, width=2), showlegend=False,
        hovertemplate="<b>%{x|%d %b %Y}</b><br>\u20b9%{y:,.0f}<extra></extra>"),
        row=2, col=1)
    fig.add_trace(go.Scatter(x=list(o_eq.index), y=lr_y,
        mode="lines", line=dict(color=_Y, width=1.5, dash="dash"),
        showlegend=False), row=2, col=1)
    q_xi = np.arange(len(o_eq), dtype=float)
    for mult, alpha in [(2, 0.07), (1, 0.14)]:
        up = [ic + sl*d + mult*resid for d in q_xi]
        dn = [ic + sl*d - mult*resid for d in q_xi]
        fig.add_trace(go.Scatter(x=list(o_eq.index)+list(o_eq.index[::-1]),
            y=up+dn[::-1], fill="toself", mode="none",
            fillcolor=f"rgba(66,165,245,{alpha})",
            showlegend=False, hoverinfo="skip"), row=2, col=1)

    WIN21 = 21
    if len(w_eq) >= WIN21:
        slopes21 = []
        for j in range(WIN21, len(w_eq)+1):
            s21, _, _, _ = _lr_on_equity(w_eq.values[j-WIN21:j])
            slopes21.append(s21)
        slope_idx = w_eq.index[WIN21-1:]
        slope_colors = [_G if s >= 0 else _R for s in slopes21]
        fig.add_trace(go.Bar(x=list(slope_idx), y=slopes21,
            marker_color=slope_colors, opacity=0.8, name="21-day Slope",
            hovertemplate="<b>%{x|%d %b %Y}</b><br>\u20b9%{y:.2f}/day<extra></extra>"),
            row=3, col=1)
        fig.add_hline(y=0, line_dash="dot", line_color=_GR, row=3, col=1)
        fig.add_hline(y=float(sl), line_dash="dash", line_color=_Y,
            line_width=1, row=3, col=1,
            annotation_text=f"OOS slope \u20b9{sl:.2f}/day",
            annotation_font_color=_Y)

    _ema_lbl = "OFF" if not mp["use_ema"] else f"{mp['ema_p']}d"
    _agc_lbl = "OFF" if not mp["use_agc"] else f"Kelly\u00d7{mp['km']}"
    degr = si["sharpe"] - so["sharpe"]
    ann = (f"<b>Selected by OOS Sharpe (walk-forward)</b><br>"
           f"OTM {mp['otm']}%  DTE {mp['dte']}d  EMA {_ema_lbl}  AGC {_agc_lbl}<br>"
           f"IS Sharpe {si['sharpe']:.2f} \u2192 OOS Sharpe {so['sharpe']:.2f}"
           f"  (degradation {degr:+.2f})<br>"
           f"OOS win {so['win_rate']:.1f}%  OOS PF {min(so['pf'],99):.2f}"
           f"  ({so['n']} OOS trades)<br>"
           f"Configs tested {res['n_tested']}  \u00b7  shortlist {res['n_short']}")
    fig.add_annotation(x=0.01, y=0.97, xref="paper", yref="paper",
        text=ann, showarrow=False, align="left",
        bgcolor=_PAN, bordercolor=_Y, borderwidth=1,
        font=dict(size=10, color="#e6edf3"))
    fig.update_yaxes(title_text="\u20b9 Equity", tickprefix="\u20b9",
        tickformat=",.0f", row=1, col=1)
    fig.update_yaxes(title_text="\u20b9 Equity", tickprefix="\u20b9",
        tickformat=",.0f", row=2, col=1)
    fig.update_yaxes(title_text="Slope \u20b9/day", row=3, col=1)
    fig.update_layout(**_PLOT_LAYOUT, height=900,
        title=("Walk-Forward Parameter Selection  \u00b7  "
               "projection = OOS slope, extrapolation is illustrative"),
        hovermode="x unified")
    return fig


# ═══════════════════════════════════════════════════════════════════════════════
#  Gradio callbacks
# ═══════════════════════════════════════════════════════════════════════════════

_WD_CHOICES = ["Auto (NSE)", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]

def _pack_cfg(start, end, dte, otm, rfr, div, hvwin, dyn, fiv,
              lsz, lts, stp, cap, use_ema, ema_period,
              use_agc, kelly_mult, agc_window, max_lots,
              iv_mult, iv_add, skew_pts, use_expiry, expiry_wd,
              enable_costs, slip, brk, stt, exch, gst, stamp):
    return dict(
        start_date=start, end_date=end,
        dte_entry=int(dte), strike_dist_pct=float(otm),
        risk_free_rate=float(rfr), div_yield=float(div),
        hv_window=int(hvwin), use_dynamic_hv=bool(dyn),
        fixed_iv=float(fiv), lot_size=int(lsz),
        lots=int(lts), strike_step=int(stp),
        initial_capital=float(cap), use_ema_filter=bool(use_ema),
        ema_period=int(ema_period),
        use_agc=bool(use_agc), kelly_mult=float(kelly_mult),
        agc_window=int(agc_window), max_lots=int(max_lots),
        iv_mult=float(iv_mult), iv_add=float(iv_add),
        skew_pts=float(skew_pts),
        use_expiry_cal=bool(use_expiry), expiry_weekday=str(expiry_wd),
        enable_costs=bool(enable_costs), slippage_pts=float(slip),
        brokerage_per_order=float(brk), stt_pct=float(stt),
        exch_pct=float(exch), gst_pct=float(gst), stamp_pct=float(stamp),
    )


def _pack_ao_cfg(api_key, client_code, mpin, totp_secret, base_url,
                 local_ip, public_ip, mac_addr):
    cfg = DEFAULT_CONFIG.copy()
    for k, v in (("api_key", api_key), ("client_code", client_code),
                 ("mpin", mpin), ("totp_secret", totp_secret),
                 ("base_url", base_url), ("local_ip", local_ip),
                 ("public_ip", public_ip), ("mac_addr", mac_addr)):
        v = (v or "").strip()
        if v:
            cfg[k] = v
    return cfg


def _metrics_table(m, cfg):
    use_agc = cfg.get("use_agc", False)
    rows = [
        ("Total Trades",              f"{m['total_trades']:,}"),
        ("Win Rate",                  f"{m['win_rate']:.2f} %"),
        ("Net P&L (\u20b9, after costs)",  f"\u20b9 {m['net_pnl']:,.2f}"),
        ("Net P&L (%)",               f"{m['net_pnl_pct']:.2f} %"),
        ("CAGR",                      f"{m['cagr_pct']:.2f} %"),
        ("Sharpe (ann., net)",        f"{m['sharpe']:.3f}"),
        ("Sortino (ann., net)",       f"{m['sortino']:.3f}"),
        ("Calmar",                    f"{m['calmar']:.3f}"),
        ("Per-trade t-stat",          f"{m['tstat']:.2f}"),
        ("Profit Factor",             f"{m['profit_factor']:.3f}"),
        ("Avg P&L / Trade (\u20b9)",       f"\u20b9 {m['avg_pnl']:,.2f}"),
        ("Max Drawdown (from capital)", f"{m['max_drawdown_pct']:.2f} %  (\u20b9 {m['max_drawdown']:,.0f})"),
        ("Peak Profit %",             f"{m['max_profit_pct']:.2f} %"),
        ("Final Equity (\u20b9)",          f"\u20b9 {m['final_equity']:,.2f}"),
        ("Total Costs (\u20b9)",           f"\u20b9 {m['total_costs']:,.2f}"),
        ("Avg Cost / Trade (\u20b9)",      f"\u20b9 {m['avg_cost']:,.2f}"),
        ("Bars Skipped \u2014 unaffordable", str(m['skipped_afford'])),
        ("Bars Skipped \u2014 EMA filter",   str(m['skipped_ema'])),
        ("Account Halted (equity\u22640)",  "YES \u26a0" if m['halted'] else "no"),
        ("Avg DTE Used (days)",       f"{m['avg_dte']:.1f}"),
        ("Avg IV Used (annual)",      f"{m['avg_sigma_pct']:.2f} %"),
        ("AGC \u2014 Avg Lots",            f"{m['avg_lots']:.2f}" if use_agc else "OFF"),
        ("AGC \u2014 Avg Kelly f",         f"{m['avg_kelly_f']:.2f} %" if use_agc else "OFF"),
    ]
    return pd.DataFrame(rows, columns=["Metric", "Value"])


def _cb_run(api_key, client_code, mpin, totp_secret, base_url,
            local_ip, public_ip, mac_addr, *bt_args):
    try:
        ao_cfg = _pack_ao_cfg(api_key, client_code, mpin, totp_secret,
                               base_url, local_ip, public_ip, mac_addr)
        cfg = _pack_cfg(*bt_args)
        df, source = _get_df(cfg["start_date"], cfg["end_date"],
                             cfg["hv_window"], cfg["ema_period"], ao_cfg)
        tdf, edf = run_backtest(df, cfg)
        m = compute_metrics(tdf, edf, cfg)
        metrics_df = _metrics_table(m, cfg)
        fig = _fig_dashboard(df, tdf, edf, cfg, m)

        cols = ["exit_date","entry_spot","exit_spot","k1","k2","dte_days",
                "sigma_pct","lots_used","entry_debit","exit_value",
                "costs_rs","pnl_rs","win"]
        if len(tdf) > 0:
            recent = (tdf[[c for c in cols if c in tdf.columns]]
                      .tail(20).reset_index()
                      .rename(columns={"entry_date": "Entry Date"}))
            recent["Entry Date"] = pd.to_datetime(recent["Entry Date"]).dt.date
            if "exit_date" in recent:
                recent["exit_date"] = pd.to_datetime(recent["exit_date"]).dt.date
            for c in ("entry_spot","exit_spot","entry_debit","exit_value",
                      "costs_rs","pnl_rs","sigma_pct"):
                if c in recent:
                    recent[c] = recent[c].round(2)
        else:
            recent = pd.DataFrame()

        status = (f"[{source}]  {m['total_trades']:,} trades  \u00b7  "
                  f"Win {m['win_rate']:.1f}%  \u00b7  "
                  f"Net \u20b9{m['net_pnl']:,.0f}  \u00b7  "
                  f"Sharpe {m['sharpe']:.2f}  \u00b7  PF {m['profit_factor']:.2f}"
                  f"  |  Costs {'ON' if cfg['enable_costs'] else 'OFF'}"
                  f" (\u20b9{m['total_costs']:,.0f})"
                  f"  |  EMA {'ON' if cfg['use_ema_filter'] else 'OFF'}"
                  f"  |  skipped {m['skipped_afford']}A/{m['skipped_ema']}E"
                  + ("  |  \u26a0 HALTED" if m['halted'] else ""))
        return metrics_df, fig, recent, status
    except Exception:
        import traceback
        return pd.DataFrame(), go.Figure(), pd.DataFrame(), f"Error: {traceback.format_exc()}"


def _cb_hawking(api_key, client_code, mpin, totp_secret, base_url,
                local_ip, public_ip, mac_addr, *bt_args):
    try:
        ao_cfg = _pack_ao_cfg(api_key, client_code, mpin, totp_secret,
                               base_url, local_ip, public_ip, mac_addr)
        cfg = _pack_cfg(*bt_args)
        df, _src = _get_df(cfg["start_date"], cfg["end_date"],
                           cfg["hv_window"], cfg["ema_period"], ao_cfg)
        tdf, edf = run_backtest(df, cfg)
        if len(tdf) < 15:
            f = go.Figure()
            f.update_layout(**_PLOT_LAYOUT,
                            title="Need at least 15 trades \u2014 extend the date range")
            return f, pd.DataFrame(), "Too few trades."
        h = compute_hawking_model(tdf, edf, cfg)
        fig = _fig_hawking(h)
        direction = "DECAYING (C > 0)" if h["decaying"] else "GROWING (C < 0)"
        edge = ("no significant decay (p \u2265 0.10)"
                if not np.isfinite(h["edge_rem"])
                else f"~{int(h['edge_rem'])} trades to fitted W = 50%")
        rows = [
            ("Current Equity",             f"\u20b9{h['E_now']:,.0f}"),
            ("Drift-consistent C (signed)",f"{h['C_H']:.4e}  \u2192 {direction}"),
            ("Hawking Temp T_H = a/E",    f"{h['hawking_T'][-1]:.5f}  [stylized]"),
            ("Daily \u03c3 of equity moves",   f"\u20b9{h['sigma_d']:,.0f}"),
            ("Time \u2192 Page level E0/\u221a2",  h["page_status"]),
            ("Time \u2192 1-lot premium floor",
             _fmt_trade_days(h["t_info"]) if h["decaying"] else "n/a \u2014 curve growing"),
            ("Time \u2192 full evaporation",
             _fmt_trade_days(h["t_evap"]) if h["decaying"] else "n/a \u2014 curve growing"),
            ("Edge-decay slope",           f"{h['w_slope']*10000:+.2f} pp per 100 trades"),
            ("Edge-decay p-value",         f"{h['w_p']:.4f}"),
            ("Edge horizon (win-rate test)", edge),
            ("Rolling window used",        f"{h['wr_win']} trades"),
        ]
        summary_df = pd.DataFrame(rows, columns=["Metric", "Value"])
        return fig, summary_df, f"Diagnostics fitted \u00b7 curve {direction} \u00b7 {edge}"
    except Exception:
        import traceback
        f = go.Figure(); f.update_layout(**_PLOT_LAYOUT)
        return f, pd.DataFrame(), f"Error: {traceback.format_exc()}"


def _cb_otm_sweep(api_key, client_code, mpin, totp_secret, base_url,
                  local_ip, public_ip, mac_addr,
                  start, end, dte, rfr, div, hvwin, dyn, fiv,
                  lsz, lts, stp, cap, use_ema, ema_period,
                  use_agc, kelly_mult, agc_window, max_lots,
                  iv_mult, iv_add, skew_pts, use_expiry, expiry_wd,
                  enable_costs, slip, brk, stt, exch, gst, stamp,
                  otm_min, otm_max, otm_steps):
    try:
        ao_cfg = _pack_ao_cfg(api_key, client_code, mpin, totp_secret,
                               base_url, local_ip, public_ip, mac_addr)
        df, _src = _get_df(start, end, hvwin, ema_period, ao_cfg)
        rows = []
        for pct in np.linspace(float(otm_min), float(otm_max), int(otm_steps)):
            c2 = _pack_cfg(start, end, dte, pct, rfr, div, hvwin, dyn, fiv,
                           lsz, lts, stp, cap, use_ema, ema_period,
                           use_agc, kelly_mult, agc_window, max_lots,
                           iv_mult, iv_add, skew_pts, use_expiry, expiry_wd,
                           enable_costs, slip, brk, stt, exch, gst, stamp)
            t2, e2 = run_backtest(df, c2)
            m2 = compute_metrics(t2, e2, c2)
            rows.append({
                "OTM %": round(pct, 2),
                "Net P&L (\u20b9)": int(round(m2["net_pnl"])),
                "Sharpe": round(m2["sharpe"], 2),
                "Win Rate (%)": round(m2["win_rate"], 1),
                "Profit Factor": round(min(m2["profit_factor"], 99), 3),
                "Max DD (%)": round(m2["max_drawdown_pct"], 1),
                "Costs (\u20b9)": int(round(m2["total_costs"])),
                "Halted": "\u26a0" if m2["halted"] else "",
                "Final Equity": int(round(m2["final_equity"])),
            })
        sdf = pd.DataFrame(rows)
        return sdf, _fig_otm_sensitivity(sdf), (
            f"OTM sweep \u2014 {int(otm_steps)} levels "
            f"({float(otm_min):.1f}%\u2192{float(otm_max):.1f}%), net of costs")
    except Exception:
        import traceback
        return pd.DataFrame(), go.Figure(), f"Error: {traceback.format_exc()}"


def _cb_ema_sweep(api_key, client_code, mpin, totp_secret, base_url,
                  local_ip, public_ip, mac_addr,
                  start, end, dte, otm, rfr, div, hvwin, dyn, fiv,
                  lsz, lts, stp, cap,
                  use_agc, kelly_mult, agc_window, max_lots,
                  iv_mult, iv_add, skew_pts, use_expiry, expiry_wd,
                  enable_costs, slip, brk, stt, exch, gst, stamp,
                  ema_min, ema_max, ema_steps):
    try:
        ao_cfg = _pack_ao_cfg(api_key, client_code, mpin, totp_secret,
                               base_url, local_ip, public_ip, mac_addr)
        rows = []
        for ep in np.linspace(int(ema_min), int(ema_max), int(ema_steps)).astype(int):
            df, _src = _get_df(start, end, hvwin, int(ep), ao_cfg)
            c2 = _pack_cfg(start, end, dte, otm, rfr, div, hvwin, dyn, fiv,
                           lsz, lts, stp, cap, True, int(ep),
                           use_agc, kelly_mult, agc_window, max_lots,
                           iv_mult, iv_add, skew_pts, use_expiry, expiry_wd,
                           enable_costs, slip, brk, stt, exch, gst, stamp)
            t2, e2 = run_backtest(df, c2)
            m2 = compute_metrics(t2, e2, c2)
            ratio = (m2["max_profit_pct"] / m2["max_drawdown_pct"]
                     if m2["max_drawdown_pct"] > 0 else 0.0)
            rows.append({
                "EMA Period": int(ep),
                "Trades": m2["total_trades"],
                "Net P&L (\u20b9)": int(round(m2["net_pnl"])),
                "Sharpe": round(m2["sharpe"], 2),
                "Win Rate (%)": round(m2["win_rate"], 1),
                "Max Profit %": round(m2["max_profit_pct"], 2),
                "Max DD %": round(m2["max_drawdown_pct"], 2),
                "P/DD Ratio": round(ratio, 3),
                "Halted": "\u26a0" if m2["halted"] else "",
            })
        edf2 = pd.DataFrame(rows)
        return edf2, _fig_ema_sensitivity(edf2), (
            f"EMA sweep \u2014 {int(ema_steps)} periods "
            f"({int(ema_min)}d\u2192{int(ema_max)}d), net of costs")
    except Exception:
        import traceback
        return pd.DataFrame(), go.Figure(), f"Error: {traceback.format_exc()}"


def _cb_path_to_profitability(target_return, lookback_days,
                              api_key, client_code, mpin, totp_secret,
                              base_url, local_ip, public_ip, mac_addr,
                              *bt_args):
    try:
        ao_cfg = _pack_ao_cfg(api_key, client_code, mpin, totp_secret,
                               base_url, local_ip, public_ip, mac_addr)
        base_cfg = _pack_cfg(*bt_args)
        df_full, _src = _get_df(base_cfg["start_date"], base_cfg["end_date"],
                                base_cfg["hv_window"], base_cfg["ema_period"],
                                ao_cfg)
        _, full_edf = run_backtest(df_full, base_cfg)
        res = optimize_path(df_full, base_cfg,
                            target_return_pct=float(target_return),
                            lookback_days=int(lookback_days))
        if not res:
            no_fig = go.Figure()
            no_fig.update_layout(**_PLOT_LAYOUT,
                                 title="No config passed the IS/OOS trade-count filters")
            return (gr.update(), gr.update(), gr.update(), gr.update(),
                    gr.update(), gr.update(), no_fig,
                    "No viable parameter set (need enough IS and OOS trades).")
        mp = res["best_params"]; si = res["is_stats"]; so = res["oos_stats"]
        fig = _fig_path(full_edf if len(full_edf) else None, res, base_cfg)
        status = (f"Selected by OOS Sharpe (walk-forward): "
                  f"IS {si['sharpe']:.2f} \u2192 OOS {so['sharpe']:.2f}  \u00b7  "
                  f"OOS win {so['win_rate']:.1f}%  \u00b7  "
                  f"projection \u20b9{res['projected']:,.0f} vs target "
                  f"\u20b9{res['target']:,.0f} (reference only)")
        return (gr.update(value=float(mp["otm"])),
                gr.update(value=int(mp["dte"])),
                gr.update(value=max(10, int(mp["ema_p"]))),
                gr.update(value=bool(mp["use_ema"])),
                gr.update(value=bool(mp["use_agc"])),
                (gr.update(value=float(mp["km"])) if mp["use_agc"] else gr.update()),
                fig, status)
    except Exception:
        import traceback
        empty = go.Figure(); empty.update_layout(**_PLOT_LAYOUT)
        return (gr.update(), gr.update(), gr.update(), gr.update(),
                gr.update(), gr.update(), empty,
                f"Error: {traceback.format_exc()}")


# ═══════════════════════════════════════════════════════════════════════════════
#  Gradio UI
# ═══════════════════════════════════════════════════════════════════════════════

_THEME = gr.themes.Base(
    primary_hue="teal", secondary_hue="slate", neutral_hue="slate",
    font=gr.themes.GoogleFont("Inter"),
).set(
    body_background_fill=_BG,
    body_text_color="#e6edf3",
    block_background_fill=_PAN,
    block_border_color="#30363d",
    block_label_text_color="#8b949e",
    button_primary_background_fill=_G,
    button_primary_background_fill_hover="#2bbbaf",
    button_primary_text_color="#ffffff",
    button_secondary_background_fill="#21262d",
    button_secondary_background_fill_hover="#2d333b",
    button_secondary_text_color="#e6edf3",
    input_background_fill="#21262d",
    input_border_color="#30363d",
    slider_color=_G,
)

with gr.Blocks(theme=_THEME, title="Overnight Bull Call Spread v6") as demo:

    gr.Markdown("""
    # Overnight Bull Call Spread — Backtest Studio v6 (review-fix release)
    **Data:** Angel One SmartAPI (if credentials supplied) / yfinance fallback — status bar shows the source actually used
    **Model:** BS pricing on HV×IV-mult with skew · listed weekly expiries · full Indian cost stack · affordability + halt gates
    **Honesty note:** prices are model values, not option quotes; vega P&L is structurally zero. See the Guide tab before trusting any number.
    """)

    with gr.Accordion("Angel One API (optional — or set AO_API_KEY / AO_CLIENT_CODE / AO_MPIN / AO_TOTP_SECRET env vars)", open=False):
        gr.Markdown("Credentials are used in-memory only and never written to disk or source. "
                    "**If any credentials were ever committed to a file, rotate them.**")
        with gr.Row():
            p_ao_api_key = gr.Textbox(value="", label="API Key", type="password", scale=2)
            p_ao_client  = gr.Textbox(value="", label="Client Code", scale=2)
            p_ao_mpin    = gr.Textbox(value="", label="MPIN", type="password", scale=2)
            p_ao_totp    = gr.Textbox(value="", label="TOTP Secret", type="password", scale=2)
        with gr.Row():
            p_ao_base      = gr.Textbox(value=DEFAULT_CONFIG["base_url"], label="Base URL", scale=3)
            p_ao_local_ip  = gr.Textbox(value=DEFAULT_CONFIG["local_ip"], label="Local IP", scale=1)
            p_ao_public_ip = gr.Textbox(value="", label="Public IP (optional)", scale=1)
            p_ao_mac       = gr.Textbox(value="", label="MAC (optional)", scale=1)

    with gr.Tabs():

        # ═════════════════════════════════════════════════════════════════════
        # TAB 1 — Backtest
        # ═════════════════════════════════════════════════════════════════════
        with gr.TabItem("Backtest"):

            with gr.Row():
                p_status = gr.Textbox(label="Status", interactive=False, lines=1, scale=5)

            with gr.Row(equal_height=False):

                with gr.Column(scale=1, min_width=330):

                    with gr.Group():
                        gr.Markdown("**Date Range**")
                        with gr.Row():
                            p_start = gr.Textbox(value="2024-01-01", label="Start",
                                                 placeholder="YYYY-MM-DD")
                            p_end   = gr.Textbox(value="2026-07-07", label="End",
                                                 placeholder="YYYY-MM-DD")

                    with gr.Group():
                        gr.Markdown("**Strategy**")
                        p_dte = gr.Slider(2, 60, value=14, step=1, label="DTE target")
                        p_otm = gr.Slider(0.1, 5.0, value=1.0, step=0.05, label="OTM Strike Distance (%)")
                        p_rfr = gr.Slider(0.01, 0.15, value=0.06, step=0.005, label="Risk-Free Rate")
                        p_div = gr.Slider(0.0, 0.05, value=0.0125, step=0.001, label="Dividend Yield")

                    with gr.Group():
                        gr.Markdown("**Expiry Calendar**  · Auto = Thu before Sep-2025, Tue after (NSE)")
                        p_use_expcal = gr.Checkbox(value=True, label="Trade listed weekly expiries (recommended)")
                        p_exp_wd = gr.Dropdown(choices=_WD_CHOICES, value="Auto (NSE)",
                                               label="Expiry weekday")

                    with gr.Group():
                        gr.Markdown("**EMA Trend Filter**")
                        p_use_ema    = gr.Checkbox(value=False, label="Enable — trade only when Close > EMA")
                        p_ema_period = gr.Slider(10, 200, value=55, step=5, label="EMA Period (days)")

                    with gr.Group():
                        gr.Markdown("**Volatility / IV model**")
                        p_dyn     = gr.Checkbox(value=True, label="Dynamic HV (rolling)")
                        p_hvwin   = gr.Slider(5, 60, value=20, step=1, label="HV Window (bars)")
                        p_iv_mult = gr.Slider(1.00, 1.50, value=1.10, step=0.01,
                                              label="IV multiplier on HV (variance-risk-premium proxy)")
                        p_iv_add  = gr.Slider(-0.05, 0.05, value=0.0, step=0.005,
                                              label="IV additive (vol pts / 100)")
                        p_skew    = gr.Slider(-1.0, 0.5, value=-0.25, step=0.05,
                                              label="Call skew (vol pts per +1% moneyness; 0 = flat)")
                        p_fiv     = gr.Slider(0.05, 0.5, value=0.155, step=0.005,
                                              label="Fixed IV (when dynamic OFF)")

                    with gr.Group():
                        gr.Markdown("**AGC — Kelly sizing** (f* = E[r]/Var[r], capped at 0.6)")
                        p_use_agc    = gr.Checkbox(value=False, label="Enable Kelly AGC")
                        p_kelly_mult = gr.Slider(0.1, 1.0, value=0.5, step=0.05, label="Kelly Multiplier")
                        p_agc_win    = gr.Slider(10, 100, value=30, step=5, label="AGC Lookback (trades)")
                        p_max_lots   = gr.Number(value=10, label="Max Lots", precision=0, minimum=1)

                    with gr.Group():
                        gr.Markdown("**Costs & Microstructure** — defaults ≈ mid-2026 discount broker; **verify vs your contract note**")
                        p_costs_on = gr.Checkbox(value=True, label="Enable transaction costs + slippage")
                        p_slip     = gr.Slider(0.0, 1.0, value=0.10, step=0.05,
                                               label="Slippage (index pts per leg, 4 legs/round-trip)")
                        with gr.Row():
                            p_brk  = gr.Number(value=20.0,  label="Brokerage \u20b9/order")
                            p_stt  = gr.Number(value=0.10,  label="STT % (sell premium)")
                        with gr.Row():
                            p_exch = gr.Number(value=0.035, label="Exchange % (turnover)")
                            p_gst  = gr.Number(value=18.0,  label="GST %")
                        p_stamp = gr.Number(value=0.003, label="Stamp % (buy premium)")

                    with gr.Group():
                        gr.Markdown("**Position Sizing**")
                        with gr.Row():
                            p_lsz = gr.Number(value=65, label="Lot Size (NIFTY = 65, Jan-2026)",
                                              precision=0, minimum=1)
                            p_lts = gr.Number(value=2, label="Lots (fixed sizing)", precision=0, minimum=1)
                        p_stp = gr.Dropdown(choices=[50, 100], value=50, label="Strike Step")

                    with gr.Group():
                        gr.Markdown("**Capital**  · one 2-lot ATM/1%-OTM spread costs ≈ \u20b914–15k of premium")
                        p_cap = gr.Number(value=200_000, label="Initial Capital", minimum=1_000)

                    btn_run = gr.Button("Run Backtest", variant="primary", size="lg")

                with gr.Column(scale=2):
                    with gr.Tabs():
                        with gr.TabItem("Dashboard"):
                            out_fig = gr.Plot(label="", show_label=False)
                        with gr.TabItem("Metrics"):
                            out_metrics = gr.Dataframe(
                                headers=["Metric", "Value"],
                                col_count=(2, "fixed"), wrap=True, interactive=False)
                        with gr.TabItem("Last 20 Trades"):
                            out_trades = gr.Dataframe(wrap=True, interactive=False)

            _ALL_AO = [p_ao_api_key, p_ao_client, p_ao_mpin, p_ao_totp, p_ao_base,
                       p_ao_local_ip, p_ao_public_ip, p_ao_mac]
            _ALL_BT = [p_start, p_end, p_dte, p_otm, p_rfr, p_div,
                       p_hvwin, p_dyn, p_fiv, p_lsz, p_lts, p_stp, p_cap,
                       p_use_ema, p_ema_period,
                       p_use_agc, p_kelly_mult, p_agc_win, p_max_lots,
                       p_iv_mult, p_iv_add, p_skew, p_use_expcal, p_exp_wd,
                       p_costs_on, p_slip, p_brk, p_stt, p_exch, p_gst, p_stamp]
            _ALL_IN = _ALL_AO + _ALL_BT
            _ALL_OUT = [out_metrics, out_fig, out_trades, p_status]

            btn_run.click(fn=_cb_run, inputs=_ALL_IN, outputs=_ALL_OUT)

        # ═════════════════════════════════════════════════════════════════════
        # TAB 2 — Sensitivity Analysis
        # ═════════════════════════════════════════════════════════════════════
        with gr.TabItem("Sensitivity Analysis"):
            with gr.Tabs():

                with gr.TabItem("OTM Distance Sweep"):
                    with gr.Row():
                        s_otm_min   = gr.Slider(0.1, 3.0, value=0.5, step=0.1, label="OTM Min (%)", scale=2)
                        s_otm_max   = gr.Slider(0.5, 5.0, value=3.0, step=0.1, label="OTM Max (%)", scale=2)
                        s_otm_steps = gr.Slider(2, 20, value=6, step=1, label="Steps", scale=1)
                        btn_otm     = gr.Button("Run OTM Sweep", variant="secondary", scale=1)
                    s_otm_status = gr.Textbox(label="Status", interactive=False, lines=1)
                    with gr.Row():
                        with gr.Column(scale=1):
                            out_otm_tbl = gr.Dataframe(wrap=True, interactive=False)
                        with gr.Column(scale=2):
                            out_otm_fig = gr.Plot()
                    btn_otm.click(fn=_cb_otm_sweep,
                        inputs=_ALL_AO + [p_start, p_end, p_dte, p_rfr, p_div,
                            p_hvwin, p_dyn, p_fiv, p_lsz, p_lts, p_stp, p_cap,
                            p_use_ema, p_ema_period, p_use_agc, p_kelly_mult,
                            p_agc_win, p_max_lots,
                            p_iv_mult, p_iv_add, p_skew, p_use_expcal, p_exp_wd,
                            p_costs_on, p_slip, p_brk, p_stt, p_exch, p_gst, p_stamp,
                            s_otm_min, s_otm_max, s_otm_steps],
                        outputs=[out_otm_tbl, out_otm_fig, s_otm_status])

                with gr.TabItem("EMA Period Sweep"):
                    with gr.Row():
                        s_ema_min   = gr.Slider(10, 150, value=50, step=5, label="EMA Min (days)", scale=2)
                        s_ema_max   = gr.Slider(50, 300, value=200, step=5, label="EMA Max (days)", scale=2)
                        s_ema_steps = gr.Slider(3, 30, value=10, step=1, label="Steps", scale=1)
                        btn_ema     = gr.Button("Run EMA Sweep", variant="secondary", scale=1)
                    s_ema_status = gr.Textbox(label="Status", interactive=False, lines=1)
                    with gr.Row():
                        with gr.Column(scale=1):
                            out_ema_tbl = gr.Dataframe(wrap=True, interactive=False)
                        with gr.Column(scale=2):
                            out_ema_fig = gr.Plot()
                    btn_ema.click(fn=_cb_ema_sweep,
                        inputs=_ALL_AO + [p_start, p_end, p_dte, p_otm, p_rfr, p_div,
                            p_hvwin, p_dyn, p_fiv, p_lsz, p_lts, p_stp, p_cap,
                            p_use_agc, p_kelly_mult, p_agc_win, p_max_lots,
                            p_iv_mult, p_iv_add, p_skew, p_use_expcal, p_exp_wd,
                            p_costs_on, p_slip, p_brk, p_stt, p_exch, p_gst, p_stamp,
                            s_ema_min, s_ema_max, s_ema_steps],
                        outputs=[out_ema_tbl, out_ema_fig, s_ema_status])

        # ═════════════════════════════════════════════════════════════════════
        # TAB 3 — Hawking Diagnostics
        # ═════════════════════════════════════════════════════════════════════
        with gr.TabItem("Hawking Diagnostics"):
            gr.Markdown("""
            ### Strategy-as-Black-Hole — a *stylized* diagnostic (v6, un-rigged)
            The physics mapping is a visual metaphor, not a model. Two things changed so it can no
            longer only predict doom:
            | v5 (rigged) | v6 (fixed) |
            |---|---|
            | C fitted on **losing days only** → forecast could only decay | C is **signed**, fitted on all days → profitable curves project growth |
            | Win rate regressed on **equity** (its own cumulative sum) | Win rate regressed on **trade order**, with a p-value |
            | "∞ (safe)" shown even when already past the level | Status distinguishes *growing / already past / time-to-level* |

            The statistically meaningful output is **panel 3**: is the win rate drifting down over
            time, and is that drift distinguishable from noise?
            """)
            with gr.Row():
                btn_hawking = gr.Button("Run Diagnostics", variant="primary", size="lg", scale=1)
                h_status = gr.Textbox(label="Status", interactive=False, lines=1, scale=4)
            with gr.Row():
                with gr.Column(scale=1):
                    out_h_tbl = gr.Dataframe(headers=["Metric","Value"],
                        col_count=(2, "fixed"), wrap=True, interactive=False)
                with gr.Column(scale=2):
                    out_h_fig = gr.Plot()
            btn_hawking.click(fn=_cb_hawking, inputs=_ALL_IN,
                              outputs=[out_h_fig, out_h_tbl, h_status])

        # ═════════════════════════════════════════════════════════════════════
        # TAB 4 — Walk-Forward Optimizer
        # ═════════════════════════════════════════════════════════════════════
        with gr.TabItem("Walk-Forward Optimizer"):
            gr.Markdown("""
            ### Parameter selection with an in-sample / out-of-sample split
            v5 minimized |projection − target| over 700 configs on ~40 trades — that optimizes for
            *wishful thinking*. v6 instead: **(1)** runs each config over the lookback window,
            **(2)** splits trades 60/40 into IS and OOS, **(3)** shortlists the top 20 by IS Sharpe,
            **(4)** selects by **OOS Sharpe**, and shows the IS→OOS degradation so overfitting is
            visible. Your annual target only draws a reference line. Sliders are auto-filled with the
            selected parameters — treat them as a candidate to re-validate, not a promise.
            """)
            with gr.Row():
                with gr.Column(scale=1):
                    p_target_ret = gr.Slider(5, 200, value=30, step=5,
                                             label="Target Annual Return (%) — reference line only")
                    p_lookback   = gr.Slider(63, 252, value=126, step=1,
                                             label="Lookback Window (trading days)")
                    btn_path = gr.Button("Run Walk-Forward Selection", variant="primary", size="lg")
                    p_path_status = gr.Textbox(label="Status", interactive=False, lines=2)
                with gr.Column(scale=2):
                    out_path_fig = gr.Plot()
            btn_path.click(fn=_cb_path_to_profitability,
                inputs=[p_target_ret, p_lookback] + _ALL_IN,
                outputs=[p_otm, p_dte, p_ema_period, p_use_ema, p_use_agc,
                         p_kelly_mult, out_path_fig, p_path_status])

        # ═════════════════════════════════════════════════════════════════════
        # TAB 5 — Guide
        # ═════════════════════════════════════════════════════════════════════
        with gr.TabItem("Guide"):
            gr.Markdown("""
            ## What v6 simulates — and what it cannot

            Every price in this app is a **Black-Scholes value**, not a traded quote. v6 closes the
            gaps that were *fixable* inside that frame and labels the ones that are not.

            ### Data
            **Angel One SmartAPI** when credentials are present (env vars `AO_API_KEY`,
            `AO_CLIENT_CODE`, `AO_MPIN`, `AO_TOTP_SECRET`, or the panel above); otherwise
            **yfinance** `^NSEI`. The status bar reports the source actually used.

            ### Contract model
            - **Expiry calendar (L5):** entries pick the listed weekly expiry closest to your DTE
              target. "Auto (NSE)" uses **Thursday before 1-Sep-2025 and Tuesday after** (SEBI/NSE
              regime change). Exchange holidays are not modelled — a holiday expiry really settles
              one session earlier.
            - **Day count (L4):** an overnight hold is charged `calendar_days − 6h15m` of decay
              (0.740 d on a weekday, 2.740 d over a weekend), not whole days.
            - **IV model (P1):** `σ = HV × iv_mult + iv_add`, plus a linear skew per leg. The 1.10×
              default is a crude variance-risk-premium proxy; entry and exit share one σ, so
              **vega P&L is structurally zero** — an overnight IV drop or spike is invisible here.

            ### Costs (P2) — verify every rate against your contract note
            Round trip = 4 orders. Slippage (pts/leg) worsens all four fills; cash charges are
            brokerage ×4, **STT on sell-side premium**, exchange charges on premium turnover, GST on
            (brokerage + exchange), stamp duty on buys. With ~\u20b960/lot of model theta per night, these
            frictions are the difference between a paper edge and a real one — the OTM sweep tab
            shows how quickly they eat thin configurations.

            ### Sizing
            - **Affordability gate (L1):** lots ≤ equity // (debit × lot size); an unaffordable bar
              is skipped and counted.
            - **Halt (L2):** the first time equity ≤ 0 the run stops. Dead accounts stay dead.
            - **Kelly AGC (K1):** `f* = E[r]/Var[r]` on per-trade net return-on-premium, ×multiplier,
              capped at 0.6. On a 30-trade window this is still a noisy estimate — the cap and the
              affordability gate bound the damage, they don't create precision.

            ### Reading the numbers
            Sharpe/Sortino are annualized from per-trade net returns at the realized trade
            frequency; drawdown is measured **from initial capital** (v5 hid losses before the first
            new high); the per-trade t-stat tells you whether the mean is distinguishable from zero
            at all.

            ### Remaining limitations (by construction)
            No option-chain data, no bid/ask ladder, no intraday path, no vega P&L, no
            assignment, no holiday calendar. Treat every result as an **upper bound** on realism
            and validate against real chain data before deploying. Nothing here is investment
            advice.
            """)

# ═══════════════════════════════════════════════════════════════════════════════
#  Launch — guarded (S2); share=False by default
# ═══════════════════════════════════════════════════════════════════════════════

if __name__ == "__main__":
    demo.launch(share=False, server_port=4080, show_error=True, quiet=False)
