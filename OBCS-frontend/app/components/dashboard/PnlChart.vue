<script setup lang="ts">
import type { DailyPnL } from '~/types'

// Hand-rolled SVG P&L chart — no chart library, so it is fully themeable via CSS
// variables and adds zero CSP/bundle weight. Two views over the same ₹ axis
// (never a dual axis): "Daily" shows per-day realized P&L as gain/loss bars
// anchored to a zero baseline; "Cumulative" shows the running equity curve.
// A crosshair + tooltip reads out each day on hover.
const props = defineProps<{ daily: DailyPnL[]; loading?: boolean; running?: boolean }>()

// String-typed so it binds cleanly to the Segmented v-model; compared as literals.
const view = ref('daily')
const views = [
  { label: 'Daily', value: 'daily' },
  { label: 'Cumulative', value: 'cumulative' },
]

// Plot geometry (internal SVG coords; the SVG itself is width-responsive).
const W = 720
const H = 280
const PAD_L = 52
const PAD_R = 14
const PAD_T = 14
const PAD_B = 26
const plotW = W - PAD_L - PAD_R
const plotH = H - PAD_T - PAD_B

interface Point { day: string; pnl: number; cum: number; trades: number; wins: number }

const series = computed<Point[]>(() => {
  let cum = 0
  return props.daily.map((d) => {
    cum += d.net_pnl
    return { day: d.day, pnl: d.net_pnl, cum, trades: d.trades, wins: d.wins }
  })
})

const values = computed(() =>
  series.value.map((p) => (view.value === 'daily' ? p.pnl : p.cum)),
)

// "Nice" axis ticks (always spanning zero) so gridlines land on round numbers.
function niceNum(range: number, round: boolean): number {
  if (range <= 0) return 1
  const exp = Math.floor(Math.log10(range))
  const frac = range / 10 ** exp
  let nf: number
  if (round) nf = frac < 1.5 ? 1 : frac < 3 ? 2 : frac < 7 ? 5 : 10
  else nf = frac <= 1 ? 1 : frac <= 2 ? 2 : frac <= 5 ? 5 : 10
  return nf * 10 ** exp
}

const axis = computed(() => {
  let min = Math.min(0, ...values.value)
  let max = Math.max(0, ...values.value)
  if (min === max) { min -= 1; max += 1 }
  const step = niceNum(niceNum(max - min, false) / 3, true)
  const niceMin = Math.floor(min / step) * step
  const niceMax = Math.ceil(max / step) * step
  // Keep exact tick values (used for positioning + :key); round only for labels.
  const ticks: number[] = []
  for (let v = niceMin; v <= niceMax + step * 0.5; v += step) ticks.push(v)
  return { min: niceMin, max: niceMax, ticks }
})

const n = computed(() => series.value.length)
function xBand(i: number): number {
  return PAD_L + ((i + 0.5) / Math.max(n.value, 1)) * plotW
}
function y(v: number): number {
  const { min, max } = axis.value
  return PAD_T + (1 - (v - min) / (max - min)) * plotH
}
const barW = computed(() => Math.min(26, (plotW / Math.max(n.value, 1)) * 0.66))

const linePath = computed(() =>
  series.value.map((p, i) => `${i === 0 ? 'M' : 'L'} ${xBand(i).toFixed(1)} ${y(p.cum).toFixed(1)}`).join(' '),
)
const areaPath = computed(() => {
  if (!n.value) return ''
  const top = series.value.map((p, i) => `${i === 0 ? 'M' : 'L'} ${xBand(i).toFixed(1)} ${y(p.cum).toFixed(1)}`).join(' ')
  return `${top} L ${xBand(n.value - 1).toFixed(1)} ${y(0).toFixed(1)} L ${xBand(0).toFixed(1)} ${y(0).toFixed(1)} Z`
})

const last = computed(() => series.value.at(-1)?.cum ?? 0)
const positive = computed(() => last.value >= 0)

// Thin x-axis date labels so they never collide (~ one per ~90px of width).
const xLabels = computed(() => {
  if (!n.value) return [] as { i: number; text: string }[]
  const maxLabels = Math.max(2, Math.floor(plotW / 96))
  const stride = Math.max(1, Math.ceil(n.value / maxLabels))
  const out: { i: number; text: string }[] = []
  for (let i = 0; i < n.value; i += stride) {
    out.push({ i, text: shortDate(series.value[i]!.day) })
  }
  return out
})

function shortDate(iso: string): string {
  const d = new Date(iso + 'T00:00:00')
  return d.toLocaleDateString('en-IN', { day: '2-digit', month: 'short' })
}
function fmtAxis(v: number): string {
  const abs = Math.abs(v)
  const sign = v < 0 ? '-' : ''
  if (abs >= 1000) return `${sign}₹${(abs / 1000).toFixed(abs >= 10000 ? 0 : 1)}k`
  return `${sign}₹${Math.round(abs)}`
}
function money(v: number): string {
  return (v < 0 ? '-₹' : '₹') + Math.abs(v).toLocaleString('en-IN', { maximumFractionDigits: 0 })
}

// --- Interaction: crosshair + tooltip ---------------------------------------
const svgRef = ref<SVGSVGElement | null>(null)
const boxRef = ref<HTMLElement | null>(null)
const hover = ref<number | null>(null)
const boxW = ref(720)
let ro: ResizeObserver | null = null

onMounted(() => {
  if (boxRef.value) {
    boxW.value = boxRef.value.clientWidth
    ro = new ResizeObserver((entries) => { boxW.value = entries[0]!.contentRect.width })
    ro.observe(boxRef.value)
  }
})
onUnmounted(() => ro?.disconnect())

function onMove(e: PointerEvent) {
  const svg = svgRef.value
  if (!svg || !n.value) return
  const ctm = svg.getScreenCTM()
  if (!ctm) return
  const pt = svg.createSVGPoint()
  pt.x = e.clientX; pt.y = e.clientY
  const loc = pt.matrixTransform(ctm.inverse())
  const rel = (loc.x - PAD_L) / plotW
  hover.value = Math.max(0, Math.min(n.value - 1, Math.round(rel * n.value - 0.5)))
}
function onLeave() { hover.value = null }

const hoverPoint = computed(() => (hover.value == null ? null : series.value[hover.value] ?? null))
// Tooltip anchor in wrapper pixels (snaps to the hovered band centre).
const tipLeft = computed(() => (hover.value == null ? 0 : (xBand(hover.value) / W) * boxW.value))
const tipFlip = computed(() => tipLeft.value > boxW.value * 0.62)

const summary = computed(() => {
  if (!n.value) return 'No P&L data yet.'
  const noun = view.value === 'daily' ? 'daily realized P&L' : 'cumulative P&L'
  return `${noun} across ${n.value} trading day${n.value === 1 ? '' : 's'}; latest cumulative ${money(last.value)}.`
})
</script>

<template>
  <UiCard>
    <template #header>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-2">
          <h3 class="text-sm font-semibold uppercase tracking-tight text-muted-foreground">Realized P&amp;L</h3>
          <span v-if="running"
            class="inline-flex items-center gap-1 rounded-full bg-gain/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-gain">
            <span class="relative flex h-1.5 w-1.5">
              <span class="absolute inline-flex h-full w-full animate-ping rounded-full bg-gain opacity-75 motion-reduce:hidden" />
              <span class="relative inline-flex h-1.5 w-1.5 rounded-full bg-gain" />
            </span>
            Live
          </span>
        </div>
        <div class="flex items-center gap-3">
          <span :class="cn('font-mono text-sm font-bold tabular-nums', positive ? 'text-gain' : 'text-loss')">
            {{ money(last) }}
          </span>
          <UiSegmented v-model="view" :options="views" />
        </div>
      </div>
    </template>

    <UiSkeleton v-if="loading" class="h-[260px] w-full" />
    <div v-else-if="!n" class="flex h-[260px] flex-col items-center justify-center gap-1 text-center text-sm text-muted-foreground">
      <span class="text-2xl">📈</span>
      No trades closed yet — start the strategy to plot P&amp;L.
    </div>

    <div v-else ref="boxRef" class="relative select-none">
      <svg
        ref="svgRef"
        :viewBox="`0 0 ${W} ${H}`"
        class="w-full touch-none"
        role="img"
        :aria-label="summary"
        @pointermove="onMove"
        @pointerleave="onLeave">
        <!-- horizontal gridlines + y labels -->
        <g>
          <template v-for="t in axis.ticks" :key="t">
            <line
              :x1="PAD_L" :x2="W - PAD_R" :y1="y(t)" :y2="y(t)"
              :stroke="t === 0 ? 'hsl(var(--border))' : 'hsl(var(--chart-grid))'"
              :stroke-width="t === 0 ? 1.25 : 1" />
            <text
              :x="PAD_L - 8" :y="y(t) + 3" text-anchor="end"
              class="fill-muted-foreground font-mono text-[10px] tabular-nums">{{ fmtAxis(t) }}</text>
          </template>
        </g>

        <!-- x labels -->
        <text
          v-for="l in xLabels" :key="'x' + l.i"
          :x="xBand(l.i)" :y="H - 8" text-anchor="middle"
          class="fill-muted-foreground text-[10px]">{{ l.text }}</text>

        <!-- cumulative area + line -->
        <template v-if="view === 'cumulative'">
          <path :d="areaPath" :fill="positive ? 'hsl(var(--gain))' : 'hsl(var(--loss))'" fill-opacity="0.12" />
          <path :d="linePath" fill="none" :stroke="positive ? 'hsl(var(--gain))' : 'hsl(var(--loss))'" stroke-width="2" stroke-linejoin="round" />
        </template>

        <!-- daily bars -->
        <template v-else>
          <rect
            v-for="(p, i) in series" :key="'b' + i"
            :x="xBand(i) - barW / 2"
            :y="p.pnl >= 0 ? y(p.pnl) : y(0)"
            :width="barW"
            :height="Math.max(1, Math.abs(y(p.pnl) - y(0)))"
            rx="2"
            :fill="p.pnl >= 0 ? 'hsl(var(--gain))' : 'hsl(var(--loss))'"
            :fill-opacity="hover === null || hover === i ? 0.9 : 0.45" />
        </template>

        <!-- crosshair -->
        <g v-if="hoverPoint">
          <line
            :x1="xBand(hover!)" :x2="xBand(hover!)" :y1="PAD_T" :y2="H - PAD_B"
            stroke="hsl(var(--foreground))" stroke-opacity="0.25" stroke-dasharray="3 3" />
          <circle
            :cx="xBand(hover!)"
            :cy="y(view === 'daily' ? hoverPoint.pnl : hoverPoint.cum)"
            r="4"
            :fill="(view === 'daily' ? hoverPoint.pnl : hoverPoint.cum) >= 0 ? 'hsl(var(--gain))' : 'hsl(var(--loss))'"
            stroke="hsl(var(--card))" stroke-width="2" />
        </g>
      </svg>

      <!-- floating tooltip -->
      <div
        v-if="hoverPoint"
        class="pointer-events-none absolute top-2 z-10 min-w-[9rem] rounded-lg border border-border bg-card/95 p-2.5 text-xs shadow-lg backdrop-blur"
        :style="{ left: tipLeft + 'px', transform: tipFlip ? 'translateX(-100%) translateX(-12px)' : 'translateX(12px)' }">
        <p class="font-mono text-[11px] font-medium text-muted-foreground">{{ shortDate(hoverPoint.day) }}</p>
        <div class="mt-1 flex items-center justify-between gap-4">
          <span class="text-muted-foreground">Daily</span>
          <span :class="cn('font-mono font-semibold tabular-nums', hoverPoint.pnl >= 0 ? 'text-gain' : 'text-loss')">{{ money(hoverPoint.pnl) }}</span>
        </div>
        <div class="flex items-center justify-between gap-4">
          <span class="text-muted-foreground">Cumulative</span>
          <span :class="cn('font-mono font-semibold tabular-nums', hoverPoint.cum >= 0 ? 'text-gain' : 'text-loss')">{{ money(hoverPoint.cum) }}</span>
        </div>
        <div class="mt-1 flex items-center justify-between gap-4 border-t border-border/60 pt-1 text-[11px] text-muted-foreground">
          <span>{{ hoverPoint.trades }} trade{{ hoverPoint.trades === 1 ? '' : 's' }}</span>
          <span>{{ hoverPoint.wins }}/{{ hoverPoint.trades }} won</span>
        </div>
      </div>

      <!-- accessible data table (screen-reader / no-color fallback) -->
      <table class="sr-only">
        <caption>{{ summary }}</caption>
        <thead><tr><th>Date</th><th>Daily P&amp;L</th><th>Cumulative P&amp;L</th></tr></thead>
        <tbody>
          <tr v-for="p in series" :key="p.day">
            <td>{{ p.day }}</td><td>{{ money(p.pnl) }}</td><td>{{ money(p.cum) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </UiCard>
</template>
