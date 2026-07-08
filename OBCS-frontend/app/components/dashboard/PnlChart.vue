<script setup lang="ts">
import type { DailyPnL } from '~/types'

// Lightweight SVG area chart of cumulative daily P&L. No chart library, so it
// is fully themeable via CSS variables and adds no CSP/bundle weight.
const props = defineProps<{ daily: DailyPnL[]; loading?: boolean }>()

const W = 640
const H = 220
const PAD = 28

const series = computed(() => {
  let cum = 0
  return props.daily.map((d) => {
    cum += d.net_pnl
    return { day: d.day, cum, pnl: d.net_pnl }
  })
})

const bounds = computed(() => {
  const vals = series.value.map((p) => p.cum)
  const min = Math.min(0, ...vals)
  const max = Math.max(0, ...vals)
  return { min, max: max === min ? min + 1 : max }
})

function x(i: number): number {
  const n = Math.max(series.value.length - 1, 1)
  return PAD + (i / n) * (W - 2 * PAD)
}
function y(v: number): number {
  const { min, max } = bounds.value
  return H - PAD - ((v - min) / (max - min)) * (H - 2 * PAD)
}

const linePath = computed(() =>
  series.value.map((p, i) => `${i === 0 ? 'M' : 'L'} ${x(i).toFixed(1)} ${y(p.cum).toFixed(1)}`).join(' '),
)
const areaPath = computed(() => {
  if (!series.value.length) return ''
  const top = series.value.map((p, i) => `${i === 0 ? 'M' : 'L'} ${x(i).toFixed(1)} ${y(p.cum).toFixed(1)}`).join(' ')
  return `${top} L ${x(series.value.length - 1).toFixed(1)} ${y(0).toFixed(1)} L ${x(0).toFixed(1)} ${y(0).toFixed(1)} Z`
})

const last = computed(() => series.value.at(-1)?.cum ?? 0)
const positive = computed(() => last.value >= 0)
</script>

<template>
  <UiCard>
    <template #header>
      <div class="flex items-center justify-between">
        <h3 class="text-sm font-semibold uppercase tracking-tight text-muted-foreground">
          📈 Daily P&amp;L (cumulative)
        </h3>
        <span :class="cn('font-mono text-sm font-semibold', positive ? 'text-gain' : 'text-loss')">
          ₹{{ last.toLocaleString('en-IN', { maximumFractionDigits: 0 }) }}
        </span>
      </div>
    </template>

    <div v-if="loading" class="h-[220px] animate-pulse rounded-lg bg-muted/40" aria-hidden="true" />
    <div v-else-if="!series.length" class="flex h-[220px] items-center justify-center text-sm text-muted-foreground">
      No trades yet — start the strategy to see live P&amp;L.
    </div>
    <svg v-else :viewBox="`0 0 ${W} ${H}`" class="w-full" role="img" aria-label="Cumulative daily P&L">
      <line :x1="PAD" :y1="y(0)" :x2="W - PAD" :y2="y(0)"
        stroke="hsl(var(--border))" stroke-dasharray="3 3" />
      <path :d="areaPath" :fill="positive ? 'hsl(var(--gain))' : 'hsl(var(--loss))'" fill-opacity="0.12" />
      <path :d="linePath" fill="none"
        :stroke="positive ? 'hsl(var(--gain))' : 'hsl(var(--loss))'" stroke-width="2" />
      <g v-for="(p, i) in series" :key="i">
        <circle :cx="x(i)" :cy="y(p.cum)" r="2.5"
          :fill="positive ? 'hsl(var(--gain))' : 'hsl(var(--loss))'">
          <title>{{ p.day }}: ₹{{ p.cum.toLocaleString('en-IN', { maximumFractionDigits: 0 }) }}</title>
        </circle>
      </g>
    </svg>
  </UiCard>
</template>
