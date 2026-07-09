<script setup lang="ts">
import { Wallet, TrendingUp, TrendingDown, Target, Layers } from 'lucide-vue-next'
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
const openCount = computed(() => trades.value.filter((t) => t.status === 'open').length)
const netPnl = computed(() => closed.value.reduce((s, t) => s + (t.net_pnl ?? 0), 0))
const winRate = computed(() => {
  if (!closed.value.length) return 0
  const wins = closed.value.filter((t) => (t.net_pnl ?? 0) > 0).length
  return (wins / closed.value.length) * 100
})
const isRunning = computed(() => state.value?.status === 'running')

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
  return (v < 0 ? '-₹' : '₹') + Math.abs(v).toLocaleString('en-IN', { maximumFractionDigits: 0 })
}
const updatedAt = computed(() =>
  state.value?.updated_at
    ? new Date(state.value.updated_at).toLocaleTimeString('en-IN', { timeZone: 'Asia/Kolkata', hour: '2-digit', minute: '2-digit', second: '2-digit' })
    : '—',
)

useHead({ title: 'OBCS·Nifty — Dashboard' })
</script>

<template>
  <div class="flex flex-col gap-6">
    <!-- Page header -->
    <div class="flex flex-wrap items-end justify-between gap-2">
      <div>
        <h1 class="text-xl font-bold tracking-tight">Dashboard</h1>
        <p class="text-xs text-muted-foreground">Overnight Bull Call Spread · NIFTY</p>
      </div>
      <div class="flex items-center gap-2 text-xs text-muted-foreground">
        <span class="relative flex h-2 w-2">
          <span v-if="isRunning" class="absolute inline-flex h-full w-full animate-ping rounded-full bg-gain opacity-75 motion-reduce:hidden" />
          <span :class="cn('relative inline-flex h-2 w-2 rounded-full', isRunning ? 'bg-gain' : 'bg-muted-foreground')" />
        </span>
        <span class="font-mono tabular-nums">updated {{ updatedAt }} IST</span>
      </div>
    </div>

    <div v-if="loadError" role="alert" class="flex items-center gap-2 rounded-lg border border-loss/40 bg-loss/10 px-4 py-2 text-sm text-loss">
      ⚠ {{ loadError }}
    </div>

    <!-- Summary stat tiles -->
    <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
      <UiStat label="Account Equity" :loading="loading">
        <template #icon><Wallet class="h-4 w-4" /></template>
        {{ money(state?.equity ?? 0) }}
      </UiStat>
      <UiStat label="Realized Net P&L" :tone="netPnl >= 0 ? 'gain' : 'loss'" :loading="loading">
        <template #icon>
          <component :is="netPnl >= 0 ? TrendingUp : TrendingDown" class="h-4 w-4" />
        </template>
        {{ money(netPnl) }}
        <template #sub>{{ closed.length }} closed</template>
      </UiStat>
      <UiStat label="Win Rate" :loading="loading">
        <template #icon><Target class="h-4 w-4" /></template>
        {{ winRate.toFixed(1) }}%
        <template #sub>of {{ closed.length }}</template>
      </UiStat>
      <UiStat label="Open Positions" :loading="loading">
        <template #icon><Layers class="h-4 w-4" /></template>
        {{ openCount }}
      </UiStat>
    </div>

    <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
      <div class="lg:col-span-1">
        <DashboardStrategyControls :state="state" @changed="refreshAll" />
      </div>
      <div class="lg:col-span-2">
        <DashboardPnlChart :daily="daily" :loading="loading" :running="isRunning" />
      </div>
    </div>

    <DashboardPnlHeatmap :daily="daily" :holidays="holidays" :year="year" />

    <DashboardTradeTable :trades="trades" :loading="loading" />
  </div>
</template>
