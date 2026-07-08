<script setup lang="ts">
import { Timer } from 'lucide-vue-next'

// Displays the strategy's total runtime, ticking live when running.
const props = defineProps<{
  baseSeconds: number
  running: boolean
  startedAt?: string
}>()

const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  timer = setInterval(() => { now.value = Date.now() }, 1000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })

const elapsed = computed(() => {
  if (!props.running || !props.startedAt) return props.baseSeconds
  const since = Math.max(0, Math.floor((now.value - Date.parse(props.startedAt)) / 1000))
  return props.baseSeconds + since
})

const formatted = computed(() => {
  let s = elapsed.value
  const d = Math.floor(s / 86400); s -= d * 86400
  const h = Math.floor(s / 3600); s -= h * 3600
  const m = Math.floor(s / 60); s -= m * 60
  const pad = (n: number) => n.toString().padStart(2, '0')
  return `${d}d ${pad(h)}:${pad(m)}:${pad(s)}`
})
</script>

<template>
  <div class="flex items-center gap-2">
    <Timer class="h-4 w-4 text-primary" />
    <span class="text-xs text-muted-foreground">Total runtime</span>
    <span class="font-mono text-sm tabular-nums">{{ formatted }}</span>
  </div>
</template>
