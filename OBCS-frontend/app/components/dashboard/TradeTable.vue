<script setup lang="ts">
import { ChevronDown, ChevronRight, Scale } from 'lucide-vue-next'
import type { Trade, OptionData, SpreadGreeks } from '~/types'

const props = defineProps<{ trades: Trade[]; loading?: boolean }>()
const api = useApi()

const expanded = ref<Set<string>>(new Set())
const computedGreeks = ref<Record<string, SpreadGreeks>>({})
const loadingComputed = ref<Set<string>>(new Set())

function toggle(id: string) {
  const s = new Set(expanded.value)
  s.has(id) ? s.delete(id) : s.add(id)
  expanded.value = s
}

// Computed Greeks are fetched on demand for comparison and never persisted.
async function loadComputed(id: string) {
  loadingComputed.value = new Set(loadingComputed.value).add(id)
  try {
    const res = await api.get<{ greeks: SpreadGreeks }>(`/api/trades/${id}/computed`)
    computedGreeks.value = { ...computedGreeks.value, [id]: res.greeks }
  } catch {
    // surfaced inline; ignore transient failures
  } finally {
    const s = new Set(loadingComputed.value); s.delete(id); loadingComputed.value = s
  }
}

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
  return '₹' + v.toLocaleString('en-IN', { maximumFractionDigits: 0 })
}
function netLive(t: Trade, leg: 'long' | 'short' | 'net', phase: 'entry' | 'exit'): OptionData | undefined {
  return t.option_data?.find((o) => o.leg === leg && o.phase === phase)
}
function num(v?: number, d = 3): string {
  return v === undefined ? '—' : v.toFixed(d)
}
</script>

<template>
  <UiCard title="Trade History">
    <UiTable>
      <UiTableHeader>
        <UiTableRow>
          <UiTableHead></UiTableHead>
          <UiTableHead>Entry</UiTableHead>
          <UiTableHead>Exit</UiTableHead>
          <UiTableHead>Expiry</UiTableHead>
          <UiTableHead>K1 / K2</UiTableHead>
          <UiTableHead>Margin</UiTableHead>
          <UiTableHead>P/NL</UiTableHead>
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
            <UiTableCell class="whitespace-nowrap font-mono text-xs">{{ fmtDateTime(t.entry_time) }}</UiTableCell>
            <UiTableCell class="whitespace-nowrap font-mono text-xs">{{ fmtDateTime(t.exit_time) }}</UiTableCell>
            <UiTableCell class="whitespace-nowrap text-xs">{{ fmtDate(t.contract_expiry) }}</UiTableCell>
            <UiTableCell class="font-mono text-xs">{{ t.k1 }} / {{ t.k2 }}</UiTableCell>
            <UiTableCell class="font-mono text-xs">{{ money(t.margin_used) }}</UiTableCell>
            <UiTableCell>
              <span :class="cn('font-mono text-xs font-semibold',
                (t.net_pnl ?? 0) >= 0 ? 'text-gain' : 'text-loss')">
                {{ t.net_pnl === undefined || t.net_pnl === null ? '—' : money(t.net_pnl) }}
              </span>
            </UiTableCell>
            <UiTableCell>
              <UiBadge :variant="t.status === 'error' ? 'loss' : t.status === 'open' ? 'default' : 'secondary'">
                {{ t.status }}
              </UiBadge>
            </UiTableCell>
          </UiTableRow>

          <!-- Expanded option-data panel -->
          <tr v-if="expanded.has(t.id)" :id="`trade-${t.id}-details`">
            <td colspan="8" class="bg-muted/30 px-4 py-3">
              <div class="flex items-center justify-between">
                <h4 class="text-xs font-semibold uppercase text-muted-foreground">Option Data</h4>
                <UiButton size="sm" variant="outline" :disabled="loadingComputed.has(t.id)"
                  @click.stop="loadComputed(t.id)">
                  <Scale class="h-3.5 w-3.5" />
                  {{ loadingComputed.has(t.id) ? 'Computing…' : 'Compare Computed' }}
                </UiButton>
              </div>

              <div class="mt-2 overflow-x-auto">
                <table class="w-full text-xs">
                  <thead class="text-muted-foreground">
                    <tr>
                      <th class="px-2 py-1 text-left">Type</th>
                      <th class="px-2 py-1 text-left">Leg</th>
                      <th class="px-2 py-1 text-right">Price</th>
                      <th class="px-2 py-1 text-right">Δ</th>
                      <th class="px-2 py-1 text-right">Γ</th>
                      <th class="px-2 py-1 text-right">Θ</th>
                      <th class="px-2 py-1 text-right">V</th>
                      <th class="px-2 py-1 text-right">IV</th>
                    </tr>
                  </thead>
                  <tbody class="font-mono">
                    <tr v-for="leg in (['long','short','net'] as const)" :key="'live-'+leg">
                      <td class="px-2 py-1"><UiBadge variant="gain">live</UiBadge></td>
                      <td class="px-2 py-1">{{ leg }} (entry)</td>
                      <td class="px-2 py-1 text-right">{{ num(netLive(t, leg, 'entry')?.price, 2) }}</td>
                      <td class="px-2 py-1 text-right">{{ num(netLive(t, leg, 'entry')?.delta) }}</td>
                      <td class="px-2 py-1 text-right">{{ num(netLive(t, leg, 'entry')?.gamma, 5) }}</td>
                      <td class="px-2 py-1 text-right">{{ num(netLive(t, leg, 'entry')?.theta, 2) }}</td>
                      <td class="px-2 py-1 text-right">{{ num(netLive(t, leg, 'entry')?.vega, 2) }}</td>
                      <td class="px-2 py-1 text-right">{{ num(netLive(t, leg, 'entry')?.iv) }}</td>
                    </tr>
                    <template v-if="computedGreeks[t.id]">
                      <tr v-for="leg in (['long','short','net'] as const)" :key="'comp-'+leg" class="text-primary">
                        <td class="px-2 py-1"><UiBadge variant="secondary">computed</UiBadge></td>
                        <td class="px-2 py-1">{{ leg }} (now)</td>
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
              <p class="mt-1 text-[10px] text-muted-foreground">
                Live data is captured at execution and stored. Computed values are model Greeks at the current spot, shown only for comparison (not persisted).
              </p>
            </td>
          </tr>
        </template>

        <UiTableRow v-if="loading">
          <UiTableCell colspan="8" class="py-8 text-center text-sm text-muted-foreground animate-pulse">
            <span class="inline-block h-4 w-1/3 rounded bg-muted"></span>
          </UiTableCell>
        </UiTableRow>
        <UiTableRow v-else-if="!props.trades.length">
          <UiTableCell colspan="8" class="py-8 text-center text-sm text-muted-foreground">
            No trades recorded yet.
          </UiTableCell>
        </UiTableRow>
      </UiTableBody>
    </UiTable>
  </UiCard>
</template>
