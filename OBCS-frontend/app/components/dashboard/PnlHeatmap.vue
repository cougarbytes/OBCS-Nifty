<script setup lang="ts">
import type { DailyPnL, Holiday } from '~/types'

// GitHub-style calendar heatmap of daily realized P&L for the current year,
// with NSE public-holiday tiles overlaid. Pure CSS grid, theme-aware.
const props = defineProps<{ daily: DailyPnL[]; holidays: Holiday[]; year: number }>()

interface Cell {
  date: string
  pnl: number | null
  holiday?: string
  weekday: number
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
  // Pad the first week so weekday alignment (Sun=0) is correct.
  const leading = start.getUTCDay()
  for (let i = 0; i < leading; i++) out.push({ date: '', pnl: null, weekday: i })
  for (let d = new Date(start); d <= end; d.setUTCDate(d.getUTCDate() + 1)) {
    const iso = d.toISOString().slice(0, 10)
    out.push({
      date: iso,
      pnl: pnlByDay.value.has(iso) ? pnlByDay.value.get(iso)! : null,
      holiday: holidayByDay.value.get(iso),
      weekday: d.getUTCDay(),
    })
  }
  return out
})

const maxAbs = computed(() => {
  let m = 1
  for (const d of props.daily) m = Math.max(m, Math.abs(d.net_pnl))
  return m
})

const months = computed(() => {
  const out: { label: string; width: number }[] = []
  let currentMonth = -1
  let width = 0
  
  for (let i = 0; i < cells.value.length; i += 7) {
    const cell = cells.value.find((c, idx) => idx >= i && idx < i + 7 && c.date)
    if (!cell) {
      width++
      continue
    }
    const m = new Date(cell.date).getUTCMonth()
    if (m !== currentMonth) {
      if (currentMonth !== -1) {
        out.push({ label: new Date(Date.UTC(2000, currentMonth, 1)).toLocaleString('en-US', { month: 'short' }), width })
      }
      currentMonth = m
      width = 1
    } else {
      width++
    }
  }
  if (currentMonth !== -1) {
    out.push({ label: new Date(Date.UTC(2000, currentMonth, 1)).toLocaleString('en-US', { month: 'short' }), width })
  }
  return out
})

function cellStyle(c: Cell): Record<string, string> {
  if (!c.date) return { background: 'transparent' }
  if (c.holiday && c.pnl === null) {
    return { background: 'hsl(var(--holiday) / 0.35)', border: '1px solid hsl(var(--holiday) / 0.6)' }
  }
  if (c.pnl === null) return { background: 'hsl(var(--muted))' }
  const intensity = Math.min(1, Math.abs(c.pnl) / maxAbs.value)
  const color = c.pnl >= 0 ? 'var(--gain)' : 'var(--loss)'
  return { background: `hsl(${color} / ${(0.2 + 0.8 * intensity).toFixed(2)})` }
}

function tooltip(c: Cell): string {
  if (!c.date) return ''
  const parts = [c.date]
  if (c.holiday) parts.push(`🏦 ${c.holiday}`)
  if (c.pnl !== null) parts.push(`₹${c.pnl.toLocaleString('en-IN', { maximumFractionDigits: 0 })}`)
  return parts.join(' · ')
}
</script>

<template>
  <UiCard>
    <template #header>
      <div class="flex items-center justify-between">
        <h3 class="text-sm font-semibold uppercase tracking-tight text-muted-foreground">
          🗓️ P&amp;L Calendar {{ year }}
        </h3>
        <div class="flex items-center gap-3 text-xs text-muted-foreground">
          <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-sm bg-gain/80" /> gain</span>
          <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-sm bg-loss/80" /> loss</span>
          <span class="flex items-center gap-1"><span class="inline-block h-3 w-3 rounded-sm bg-holiday/50 border border-holiday/60" /> holiday</span>
        </div>
      </div>
    </template>

    <div class="overflow-x-auto">
      <div class="flex gap-1">
        <div class="grid grid-rows-7 gap-1 pr-1 pt-[18px] text-[10px] text-muted-foreground">
          <span></span><span>Mon</span><span></span><span>Wed</span><span></span><span>Fri</span><span></span>
        </div>
        <div>
          <div class="mb-1 flex text-[10px] text-muted-foreground">
            <span v-for="(m, i) in months" :key="i" :style="{ width: `calc(${m.width} * 16px)` }">
              {{ m.label }}
            </span>
          </div>
          <div class="grid grid-flow-col grid-rows-7 gap-1" style="width: max-content;">
            <div
              v-for="(c, i) in cells" :key="i"
              class="h-3 w-3 rounded-sm"
              :style="cellStyle(c)"
              :title="tooltip(c)" />
          </div>
        </div>
      </div>
    </div>
  </UiCard>
</template>
