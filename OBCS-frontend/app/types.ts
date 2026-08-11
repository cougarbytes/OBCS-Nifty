// API contract types mirroring the Go backend models (internal/models).

export interface StrategyState {
  status: 'running' | 'stopped'
  trading_mode: 'paper' | 'live'
  started_at?: string
  stopped_at?: string
  accumulated_runtime_seconds: number
  runtime_seconds: number
  equity: number
  last_message?: string
  updated_at: string
}

export interface Greeks {
  price: number
  delta: number
  gamma: number
  theta: number
  vega: number
  iv: number
}

export interface SpreadGreeks {
  long: Greeks
  short: Greeks
  net: Greeks
}

export interface OptionData {
  id: string
  trade_id: string
  leg: 'long' | 'short' | 'net'
  phase: 'entry' | 'exit'
  data_type: 'live'
  price: number
  delta: number
  gamma: number
  theta: number
  vega: number
  iv: number
  captured_at: string
}

export interface Trade {
  id: string
  status: 'open' | 'closed' | 'error'
  trading_mode: 'paper' | 'live'
  underlying: string
  entry_time: string
  exit_time?: string
  contract_expiry: string
  k1: number
  k2: number
  lots: number
  lot_size: number
  entry_spot: number
  exit_spot?: number
  sigma_atm: number
  entry_debit: number
  exit_value?: number
  margin_used: number
  gross_pnl?: number
  costs?: number
  net_pnl?: number
  kelly_f?: number
  broker_order_ref?: string
  note?: string
  /** Broker's last rejection verdict when an exit was abandoned after retries. */
  rejection_reason?: string
  option_data?: OptionData[]
}

export interface DailyPnL {
  day: string
  net_pnl: number
  trades: number
  wins: number
}

export interface Holiday {
  date: string
  description: string
  year: number
}
