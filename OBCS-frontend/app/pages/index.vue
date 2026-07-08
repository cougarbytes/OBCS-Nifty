<script setup lang="ts">
import { Wallet, TrendingUp, Percent } from 'lucide-vue-next'
import type { StrategyState, Trade, DailyPnL, Holiday } from '~/types'

definePageMeta({ middleware: 'auth' })

const api = useApi()
const supabase = useSupabaseClient()
const year = new Date().getFullYear()

const state = ref<StrategyState | null>(null)
const trades = ref<Trade[]>([])
const daily = ref<DailyPnL[]>([])
const holidays = ref<Holiday[]>([])

const loading = ref(true)
const loadError = ref('')

async function refreshState() {
  try { state.value = await api.get<StrategyState>('/api/strategy/state') }
  catch (e: any) { loadError.value = e?.data?.error || e?.message || 'Failed to load state' }
}
async function refreshTrades() {
  try {
    const res = await api.get<{ trades: Trade[] }>('/api/trades?limit=200')
    trades.value = res.trades || []
  } catch (e: any) { loadError.value = e?.data?.error || e?.message || 'Failed to load trades' }
}
async function refreshDaily() {
  try {
    const res = await api.get<{ daily: DailyPnL[] }>('/api/pnl/daily')
    daily.value = res.daily || []
  } catch (e: any) { loadError.value = e?.data?.error || e?.message || 'Failed to load P&L' }
}
async function refreshHolidays() {
  try {
    const res = await api.get<{ holidays: Holiday[] }>(`/api/holidays?year=${year}`)
    holidays.value = res.holidays || []
  } catch (e: any) { loadError.value = e?.data?.error || e?.message || 'Failed to load holidays' }
}
async function refreshAll() {
  await Promise.all([refreshState(), refreshTrades(), refreshDaily()])
}

// Derived summary metrics.
const closed = computed(() => trades.value.filter((t) => t.status === 'closed'))
const netPnl = computed(() => closed.value.reduce((s, t) => s + (t.net_pnl ?? 0), 0))
const winRate = computed(() => {
  if (!closed.value.length) return 0
  const wins = closed.value.filter((t) => (t.net_pnl ?? 0) > 0).length
  return (wins / closed.value.length) * 100
})

let channel: ReturnType<typeof supabase.channel> | null = null

onMounted(async () => {
  await Promise.all([refreshAll(), refreshHolidays()])
  loading.value = false

  // Supabase Realtime: refresh when the backend writes trades or state.
  channel = supabase
    .channel('obcs-dashboard')
    .on('postgres_changes', { event: '*', schema: 'public', table: 'trades' }, () => {
      refreshTrades(); refreshDaily()
    })
    .on('postgres_changes', { event: '*', schema: 'public', table: 'strategy_state' }, () => {
      refreshState()
    })
    .subscribe()
})

onUnmounted(() => {
  if (channel) supabase.removeChannel(channel)
})

function money(v: number): string {
  return '₹' + v.toLocaleString('en-IN', { maximumFractionDigits: 0 })
}

useHead({ title: 'OBCS·Nifty — Dashboard' })
</script>

<template>
  <div class="flex flex-col gap-6">
    <h1 class="sr-only">OBCS-Nifty dashboard</h1>
    <div v-if="loadError" role="alert" class="flex items-center gap-2 rounded-lg border border-loss/40 bg-loss/10 px-4 py-2 text-sm text-loss">
      ⚠ {{ loadError }}
    </div>

    <!-- Summary stat cards -->
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
      <UiCard title="Account Equity">
        <div class="flex items-center gap-3">
          <Wallet class="h-8 w-8 text-primary" />
          <span class="font-mono text-2xl font-bold">{{ money(state?.equity ?? 0) }}</span>
        </div>
      </UiCard>
      <UiCard title="Realized Net P&L">
        <div class="flex items-center gap-3">
          <TrendingUp class="h-8 w-8" :class="netPnl >= 0 ? 'text-gain' : 'text-loss'" />
          <span :class="cn('font-mono text-2xl font-bold', netPnl >= 0 ? 'text-gain' : 'text-loss')">
            {{ money(netPnl) }}
          </span>
        </div>
      </UiCard>
      <UiCard title="Win Rate">
        <div class="flex items-center gap-3">
          <Percent class="h-8 w-8 text-primary" />
          <span class="font-mono text-2xl font-bold">{{ winRate.toFixed(1) }}%</span>
          <span class="text-xs text-muted-foreground">({{ closed.length }} closed)</span>
        </div>
      </UiCard>
    </div>

    <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
      <div class="lg:col-span-1">
        <DashboardStrategyControls :state="state" @changed="refreshAll" />
      </div>
      <div class="lg:col-span-2">
        <DashboardPnlChart :daily="daily" :loading="loading" />
      </div>
    </div>

    <DashboardPnlHeatmap :daily="daily" :holidays="holidays" :year="year" />

    <DashboardTradeTable :trades="trades" :loading="loading" />
  </div>
</template>
