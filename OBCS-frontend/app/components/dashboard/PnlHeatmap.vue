<script setup lang="ts">
import type { DailyPnL, Holiday } from '~/types'

// GitHub-style calendar heatmap of daily realized P&L for the current year, with
// NSE public-holiday tiles overlaid. Pure CSS grid, theme-aware. Weeks are real
// columns so the month header and weekday labels stay aligned at any width.
const props = defineProps<{ daily: DailyPnL[]; holidays: Holiday[]; year: number }>()

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

interface Cell {
  date: string
  pnl: number | null
  holiday?: string
  weekday: number
  weekend: boolean
}

const pnlByDay = computed(() => {
  const m = new Map<string, number>()
  for (const d of props.daily) m.set(d.day, d.net_pnl)
  return m
})
const holidayByDay = computed(() => {
  const m = new Map<string, string>()
  for (const h of props.holidays) m.set(h.date, h.description)
  return m
})

const cells = computed<Cell[]>(() => {
  const out: Cell[] = []
  const start = new Date(Date.UTC(props.year, 0, 1))
  const end = new Date(Date.UTC(props.year, 11, 31))
  // Pad the first week so weekday alignment (Sun=0 at top) is correct.
  const leading = start.getUTCDay()
  for (let i = 0; i < leading; i++) out.push({ date: '', pnl: null, weekday: i, weekend: false })
  for (let d = new Date(start); d <= end; d.setUTCDate(d.getUTCDate() + 1)) {
    const iso = d.toISOString().slice(0, 10)
    const wd = d.getUTCDay()
    out.push({
      date: iso,
      pnl: pnlByDay.value.has(iso) ? pnlByDay.value.get(iso)! : null,
      holiday: holidayByDay.value.get(iso),
      weekday: wd,
      weekend: wd === 0 || wd === 6,
    })
  }
  return out
})

const weeks = computed<Cell[][]>(() => {
  const out: Cell[][] = []
  for (let i = 0; i < cells.value.length; i += 7) out.push(cells.value.slice(i, i + 7))
  return out
})

// One month label per week-column, at the column where the month first appears.
const monthHeader = computed(() => {
  let prev = -1
  return weeks.value.map((w) => {
    const dated = w.find((c) => c.date)
    if (!dated) return ''
    const m = new Date(dated.date + 'T00:00:00Z').getUTCMonth()
    if (m !== prev) { prev = m; return MONTHS[m] }
    return ''
  })
})

const maxAbs = computed(() => {
  let m = 1
  for (const d of props.daily) m = Math.max(m, Math.abs(d.net_pnl))
  return m
})

const stats = computed(() => {
  let total = 0, days = 0, winDays = 0
  for (const d of props.daily) { total += d.net_pnl; days++; if (d.net_pnl > 0) winDays++ }
  return { total, days, winDays }
})

function cellStyle(c: Cell): Record<string, string> {
  if (!c.date) return { background: 'transparent' }
  if (c.holiday) return { background: 'hsl(var(--holiday) / 0.35)', boxShadow: 'inset 0 0 0 1px hsl(var(--holiday) / 0.65)' }
  if (c.pnl === null) return { background: c.weekend ? 'hsl(var(--muted) / 0.35)' : 'hsl(var(--muted) / 0.8)' }
  const intensity = Math.min(1, Math.abs(c.pnl) / maxAbs.value)
  const color = c.pnl >= 0 ? 'var(--gain)' : 'var(--loss)'
  return { background: `hsl(${color} / ${(0.22 + 0.78 * intensity).toFixed(2)})` }
}

function money(v: number): string {
  return (v < 0 ? '-₹' : '₹') + Math.abs(v).toLocaleString('en-IN', { maximumFractionDigits: 0 })
}
function tooltip(c: Cell): string {
  if (!c.date) return ''
  const d = new Date(c.date + 'T00:00:00Z')
  const parts = [`${WEEKDAYS[c.weekday]} ${c.date}`]
  if (c.holiday) parts.push(`🏦 ${c.holiday}`)
  if (c.pnl !== null) parts.push(money(c.pnl))
  else if (!c.holiday && !c.weekend) parts.push('no trades')
  return parts.join(' · ')
}
// Legend swatches: faint → strong on the gain ramp.
const legendSteps = [0.22, 0.4, 0.6, 0.8, 1]
</script>

<template>
  <UiCard>
    <template #header>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <h3 class="text-sm font-semibold uppercase tracking-tight text-muted-foreground">🗓️ P&amp;L Calendar {{ year }}</h3>
          <span :class="cn('font-mono text-sm font-bold tabular-nums', stats.total >= 0 ? 'text-gain' : 'text-loss')">{{ money(stats.total) }}</span>
        </div>
        <p class="text-xs text-muted-foreground">
          {{ stats.days }} trading days · <span class="text-gain">{{ stats.winDays }} up</span> / <span class="text-loss">{{ stats.days - stats.winDays }} down</span>
        </p>
      </div>
    </template>

    <div class="overflow-x-auto scrollbar-thin pb-1" :style="{ '--cell': '13px' }">
      <div class="flex gap-1">
        <!-- weekday labels (fixed) -->
        <div class="flex flex-col gap-1 pr-1 text-[10px] leading-none text-muted-foreground">
          <div class="mb-1 h-4" aria-hidden="true" />
          <div v-for="(lbl, i) in WEEKDAYS" :key="i" class="flex h-[var(--cell)] items-center">{{ i % 2 === 1 ? lbl : '' }}</div>
        </div>

        <!-- month header + week columns (scroll together) -->
        <div>
          <div class="mb-1 flex h-4 gap-1">
            <div v-for="(m, wi) in monthHeader" :key="wi" class="w-[var(--cell)] shrink-0 whitespace-nowrap text-[10px] leading-4 text-muted-foreground">{{ m }}</div>
          </div>
          <div class="flex gap-1">
            <div v-for="(w, wi) in weeks" :key="wi" class="flex flex-col gap-1">
              <div
                v-for="(c, ci) in w" :key="ci"
                class="h-[var(--cell)] w-[var(--cell)] rounded-[3px] transition-shadow hover:ring-2 hover:ring-ring"
                :style="cellStyle(c)"
                :title="tooltip(c)" />
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- legend -->
    <div class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-[11px] text-muted-foreground">
      <div class="flex items-center gap-1.5">
        <span>Less</span>
        <span v-for="s in legendSteps" :key="s" class="h-3 w-3 rounded-[3px]" :style="{ background: `hsl(var(--gain) / ${s})` }" />
        <span>More</span>
      </div>
      <span class="flex items-center gap-1.5"><span class="h-3 w-3 rounded-[3px]" :style="{ background: 'hsl(var(--loss) / 0.8)' }" /> loss</span>
      <span class="flex items-center gap-1.5"><span class="h-3 w-3 rounded-[3px]" :style="{ background: 'hsl(var(--holiday) / 0.35)', boxShadow: 'inset 0 0 0 1px hsl(var(--holiday) / 0.65)' }" /> holiday</span>
      <span class="flex items-center gap-1.5"><span class="h-3 w-3 rounded-[3px]" :style="{ background: 'hsl(var(--muted) / 0.8)' }" /> no trades</span>
    </div>
  </UiCard>
</template>
