<script setup lang="ts">
import { ChevronDown, ChevronRight, Scale, X, AlertTriangle, AlertOctagon } from 'lucide-vue-next'
import type { Trade, OptionData, SpreadGreeks } from '~/types'

const props = defineProps<{ trades: Trade[]; loading?: boolean }>()
const api = useApi()

const expanded = ref<Set<string>>(new Set())
const computedGreeks = ref<Record<string, SpreadGreeks>>({})
const loadingComputed = ref<Set<string>>(new Set())
const computedError = ref<Record<string, string>>({})

const COLSPAN = 9

function toggle(id: string) {
  const s = new Set(expanded.value)
  s.has(id) ? s.delete(id) : s.add(id)
  expanded.value = s
}

// Computed Greeks are fetched on demand for comparison and never persisted.
async function loadComputed(id: string) {
  loadingComputed.value = new Set(loadingComputed.value).add(id)
  computedError.value = { ...computedError.value, [id]: '' }
  try {
    const res = await api.get<{ greeks: SpreadGreeks }>(`/api/trades/${id}/computed`)
    computedGreeks.value = { ...computedGreeks.value, [id]: res.greeks }
  } catch (e: unknown) {
    const err = e as { data?: { error?: string }; message?: string }
    computedError.value = { ...computedError.value, [id]: err?.data?.error || err?.message || 'compare failed' }
  } finally {
    const s = new Set(loadingComputed.value); s.delete(id); loadingComputed.value = s
  }
}
function clearComputed(id: string) {
  const next = { ...computedGreeks.value }; delete next[id]; computedGreeks.value = next
}

// Live snapshots ordered entry→exit, long→short→net for a stable ledger.
const phaseOrder: Record<string, number> = { entry: 0, exit: 1 }
const legOrder: Record<string, number> = { long: 0, short: 1, net: 2 }
function liveRows(t: Trade): OptionData[] {
  return (t.option_data ?? []).slice().sort(
    (a, b) => (phaseOrder[a.phase]! - phaseOrder[b.phase]!) || (legOrder[a.leg]! - legOrder[b.leg]!),
  )
}

// Summary footer.
const summary = computed(() => {
  let open = 0, closed = 0, errored = 0, net = 0
  for (const t of props.trades) {
    if (t.status === 'open') open++
    else if (t.status === 'closed') { closed++; net += t.net_pnl ?? 0 }
    else errored++
  }
  return { open, closed, errored, net }
})

function fmtDateTime(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('en-IN', {
    timeZone: 'Asia/Kolkata', day: '2-digit', month: 'short',
    hour: '2-digit', minute: '2-digit',
  })
}
function fmtDate(iso?: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString('en-IN', {
    timeZone: 'Asia/Kolkata', day: '2-digit', month: 'short', year: 'numeric',
  })
}
function money(v?: number): string {
  if (v === undefined || v === null) return '—'
  return (v < 0 ? '-₹' : '₹') + Math.abs(v).toLocaleString('en-IN', { maximumFractionDigits: 0 })
}
function num(v?: number, d = 3): string {
  return v === undefined || v === null ? '—' : v.toFixed(d)
}
</script>

<template>
  <UiCard>
    <template #header>
      <div class="flex items-center justify-between">
        <h3 class="text-sm font-semibold uppercase tracking-tight text-muted-foreground">Trade History</h3>
        <span class="text-xs text-muted-foreground">{{ props.trades.length }} record{{ props.trades.length === 1 ? '' : 's' }}</span>
      </div>
    </template>

    <UiTable>
      <UiTableHeader>
        <UiTableRow>
          <UiTableHead><span class="sr-only">Expand</span></UiTableHead>
          <UiTableHead>Entry</UiTableHead>
          <UiTableHead>Exit</UiTableHead>
          <UiTableHead>Expiry</UiTableHead>
          <UiTableHead>Strikes</UiTableHead>
          <UiTableHead>Margin</UiTableHead>
          <UiTableHead>P/NL</UiTableHead>
          <UiTableHead>Mode</UiTableHead>
          <UiTableHead>Status</UiTableHead>
        </UiTableRow>
      </UiTableHeader>
      <UiTableBody>
        <template v-for="t in props.trades" :key="t.id">
          <UiTableRow :class="cn('transition-colors', expanded.has(t.id) && 'bg-muted/20')">
            <UiTableCell>
              <button
                type="button"
                class="flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                :aria-expanded="expanded.has(t.id)"
                :aria-controls="`trade-${t.id}-details`"
                :aria-label="`${expanded.has(t.id) ? 'Collapse' : 'Expand'} trade ${t.k1}/${t.k2}`"
                @click="toggle(t.id)">
                <ChevronDown v-if="expanded.has(t.id)" class="h-4 w-4" />
                <ChevronRight v-else class="h-4 w-4" />
              </button>
            </UiTableCell>
            <UiTableCell class="whitespace-nowrap font-mono text-xs tabular-nums">{{ fmtDateTime(t.entry_time) }}</UiTableCell>
            <UiTableCell class="whitespace-nowrap font-mono text-xs tabular-nums">{{ fmtDateTime(t.exit_time) }}</UiTableCell>
            <UiTableCell class="whitespace-nowrap text-xs">{{ fmtDate(t.contract_expiry) }}</UiTableCell>
            <UiTableCell class="whitespace-nowrap font-mono text-xs tabular-nums">
              {{ t.k1 }} / {{ t.k2 }}
              <span class="text-muted-foreground">×{{ t.lots }}</span>
            </UiTableCell>
            <UiTableCell class="font-mono text-xs tabular-nums">{{ money(t.margin_used) }}</UiTableCell>
            <UiTableCell>
              <span :class="cn('font-mono text-xs font-semibold tabular-nums',
                (t.net_pnl ?? 0) >= 0 ? 'text-gain' : 'text-loss')">
                {{ t.net_pnl === undefined || t.net_pnl === null ? '—' : money(t.net_pnl) }}
              </span>
            </UiTableCell>
            <UiTableCell>
              <UiBadge :variant="t.trading_mode === 'live' ? 'warning' : 'secondary'">{{ t.trading_mode }}</UiBadge>
            </UiTableCell>
            <UiTableCell>
              <UiBadge :variant="t.status === 'error' ? 'loss' : t.status === 'open' ? 'default' : 'secondary'">
                <span v-if="t.status === 'error'" class="flex items-center gap-1"><AlertTriangle class="h-3 w-3" /> Rejected</span>
                <span v-else>{{ t.status }}</span>
              </UiBadge>
            </UiTableCell>
          </UiTableRow>

          <!-- Expanded option-data panel -->
          <tr v-if="expanded.has(t.id)" :id="`trade-${t.id}-details`">
            <td :colspan="COLSPAN" class="bg-muted/30 px-4 py-3">
              <div v-if="t.status === 'error'" class="mb-4 bg-loss/10 p-4 border border-loss/20 rounded-md flex flex-col gap-3">
                <div class="flex items-center justify-between border-b border-loss/20 pb-2">
                  <div class="flex items-center gap-2 text-loss font-semibold text-xs">
                    <AlertOctagon class="h-4 w-4" />
                    <span>Broker Execution Failure Breakdown</span>
                  </div>
                  <!-- Position Health Indicator -->
                  <UiBadge :variant="t.note?.includes('both legs still open') ? 'secondary' : 'loss'">
                    Position Health: {{ t.note?.includes('both legs still open') ? '✅ FLAT (Hedged Spread)' : t.note?.includes('still open') ? '⚠️ UNHEDGED RISK' : 'UNKNOWN' }}
                  </UiBadge>
                </div>

                <div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-xs font-mono">
                  <div class="rounded bg-background/80 p-2.5 border border-border">
                    <span class="text-muted-foreground block text-[10px] uppercase font-sans font-semibold mb-1">Broker API Response</span>
                    <p class="text-loss font-semibold">{{ t.rejection_reason || 'Broker returned low liquidity / RMS restriction error' }}</p>
                  </div>

                  <div class="rounded bg-background/80 p-2.5 border border-border">
                    <span class="text-muted-foreground block text-[10px] uppercase font-sans font-semibold mb-1">Retry Limit Status</span>
                    <p class="text-foreground">Attempts: <span class="font-bold text-loss">3/3 Max Retries Reached</span></p>
                    <p class="text-[11px] text-muted-foreground font-sans mt-0.5">Execution loop auto-stopped to prevent tick-by-tick brokerage & slippage charges.</p>
                  </div>
                </div>
                <p class="text-[11px] text-loss/80 font-sans mt-1">Note: {{ t.note }}</p>
              </div>

              <div class="flex flex-wrap items-center justify-between gap-2">
                <h4 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Option Greeks</h4>
                <div class="flex items-center gap-2">
                  <UiButton v-if="computedGreeks[t.id]" size="sm" variant="ghost" @click.stop="clearComputed(t.id)">
                    <X class="h-3.5 w-3.5" /> Hide computed
                  </UiButton>
                  <UiButton size="sm" variant="outline" :disabled="loadingComputed.has(t.id)" @click.stop="loadComputed(t.id)">
                    <Scale class="h-3.5 w-3.5" />
                    {{ loadingComputed.has(t.id) ? 'Computing…' : computedGreeks[t.id] ? 'Recompute' : 'Compare computed' }}
                  </UiButton>
                </div>
              </div>

              <div class="mt-2 overflow-x-auto scrollbar-thin">
                <table class="w-full min-w-[36rem] text-xs">
                  <thead class="text-muted-foreground">
                    <tr class="border-b border-border/60">
                      <th class="px-2 py-1 text-left font-medium">Source</th>
                      <th class="px-2 py-1 text-left font-medium">Leg · Phase</th>
                      <th class="px-2 py-1 text-right font-medium">Price</th>
                      <th class="px-2 py-1 text-right font-medium" title="Delta">Δ</th>
                      <th class="px-2 py-1 text-right font-medium" title="Gamma">Γ</th>
                      <th class="px-2 py-1 text-right font-medium" title="Theta">Θ</th>
                      <th class="px-2 py-1 text-right font-medium" title="Vega">ν</th>
                      <th class="px-2 py-1 text-right font-medium" title="Implied volatility">IV</th>
                    </tr>
                  </thead>
                  <tbody class="font-mono tabular-nums">
                    <!-- Persisted live snapshots -->
                    <tr v-for="od in liveRows(t)" :key="od.id" class="border-b border-border/30">
                      <td class="px-2 py-1"><UiBadge variant="gain">live</UiBadge></td>
                      <td class="px-2 py-1 font-sans capitalize">{{ od.leg }} · {{ od.phase }}</td>
                      <td class="px-2 py-1 text-right">{{ num(od.price, 2) }}</td>
                      <td class="px-2 py-1 text-right">{{ num(od.delta) }}</td>
                      <td class="px-2 py-1 text-right">{{ num(od.gamma, 5) }}</td>
                      <td class="px-2 py-1 text-right">{{ num(od.theta, 2) }}</td>
                      <td class="px-2 py-1 text-right">{{ num(od.vega, 2) }}</td>
                      <td class="px-2 py-1 text-right">{{ num(od.iv) }}</td>
                    </tr>
                    <tr v-if="!liveRows(t).length">
                      <td :colspan="8" class="px-2 py-2 text-center font-sans text-muted-foreground">No live snapshot stored for this trade.</td>
                    </tr>
                    <!-- On-demand computed (comparison only, not persisted) -->
                    <template v-if="computedGreeks[t.id]">
                      <tr v-for="leg in (['long','short','net'] as const)" :key="'comp-' + leg" class="border-b border-border/30 text-primary">
                        <td class="px-2 py-1"><UiBadge variant="secondary">computed</UiBadge></td>
                        <td class="px-2 py-1 font-sans capitalize">{{ leg }} · now</td>
                        <td class="px-2 py-1 text-right">{{ num(computedGreeks[t.id]![leg].price, 2) }}</td>
                        <td class="px-2 py-1 text-right">{{ num(computedGreeks[t.id]![leg].delta) }}</td>
                        <td class="px-2 py-1 text-right">{{ num(computedGreeks[t.id]![leg].gamma, 5) }}</td>
                        <td class="px-2 py-1 text-right">{{ num(computedGreeks[t.id]![leg].theta, 2) }}</td>
                        <td class="px-2 py-1 text-right">{{ num(computedGreeks[t.id]![leg].vega, 2) }}</td>
                        <td class="px-2 py-1 text-right">{{ num(computedGreeks[t.id]![leg].iv) }}</td>
                      </tr>
                    </template>
                  </tbody>
                </table>
              </div>
              <p v-if="computedError[t.id]" class="mt-1 text-[11px] text-loss">⚠ {{ computedError[t.id] }}</p>
              <p class="mt-1.5 text-[10px] leading-relaxed text-muted-foreground">
                <span class="font-semibold text-gain">Live</span> data is captured at execution and stored.
                <span class="font-semibold text-primary">Computed</span> values are model Greeks at the current spot — shown only for comparison, never persisted.
              </p>
            </td>
          </tr>
        </template>

        <!-- loading skeleton -->
        <template v-if="loading">
          <UiTableRow v-for="i in 4" :key="'sk' + i">
            <UiTableCell :colspan="COLSPAN" class="py-2">
              <UiSkeleton class="h-5 w-full" />
            </UiTableCell>
          </UiTableRow>
        </template>
        <UiTableRow v-else-if="!props.trades.length">
          <UiTableCell :colspan="COLSPAN" class="py-10 text-center text-sm text-muted-foreground">
            <span class="mb-1 block text-2xl">🧾</span>
            No trades recorded yet.
          </UiTableCell>
        </UiTableRow>
      </UiTableBody>

      <tfoot v-if="!loading && props.trades.length">
        <tr class="border-t border-border text-xs">
          <td :colspan="6" class="px-3 py-2.5 text-muted-foreground">
            {{ summary.closed }} closed · {{ summary.open }} open<span v-if="summary.errored"> · {{ summary.errored }} error</span>
          </td>
          <td class="px-3 py-2.5">
            <span :class="cn('font-mono font-semibold tabular-nums', summary.net >= 0 ? 'text-gain' : 'text-loss')">{{ money(summary.net) }}</span>
          </td>
          <td :colspan="2" class="px-3 py-2.5 text-right text-muted-foreground">realized</td>
        </tr>
      </tfoot>
    </UiTable>
  </UiCard>
</template>
