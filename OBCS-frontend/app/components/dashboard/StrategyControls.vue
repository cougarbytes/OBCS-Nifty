<script setup lang="ts">
import { Play, Square, PlusCircle, MinusCircle, AlertOctagon, RefreshCw, CheckCircle2 } from 'lucide-vue-next'
import type { StrategyState } from '~/types'

const props = defineProps<{ state: StrategyState | null }>()
const emit = defineEmits<{ (e: 'changed'): void }>()

const api = useApi()
const busy = ref(false)
const error = ref('')

const isRunning = computed(() => props.state?.status === 'running')
const isPaper = computed(() => props.state?.trading_mode === 'paper')
const isHaltedError = computed(() => props.state?.last_message?.includes('EXIT ABANDONED') || props.state?.last_message?.includes('suspended'))

const since = computed(() => {
  if (!isRunning.value || !props.state?.started_at) return ''
  return new Date(props.state.started_at).toLocaleString('en-IN', {
    timeZone: 'Asia/Kolkata', day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit',
  })
})

async function act(path: string) {
  busy.value = true
  error.value = ''
  try {
    await api.post(path)
    emit('changed')
  } catch (e: unknown) {
    const err = e as { data?: { error?: string }, message?: string }
    error.value = err?.data?.error || err?.message || 'request failed'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UiCard title="Strategy Control">
    <div class="flex flex-col gap-4">
      <!-- Status header -->
      <div :class="cn('flex items-center justify-between rounded-lg border px-3 py-2.5', isHaltedError ? 'border-loss/50 bg-loss/10 text-loss' : 'border-border bg-elevated')">
        <div class="flex items-center gap-2.5">
          <span class="relative flex h-2.5 w-2.5">
            <span v-if="isRunning" class="absolute inline-flex h-full w-full animate-ping rounded-full bg-gain opacity-75 motion-reduce:hidden" />
            <span :class="cn('relative inline-flex h-2.5 w-2.5 rounded-full', isHaltedError ? 'bg-loss' : isRunning ? 'bg-gain' : 'bg-muted-foreground')" />
          </span>
          <div class="leading-tight">
            <p class="text-sm font-semibold">
              <span v-if="isHaltedError">Halted (Error)</span>
              <span v-else-if="isRunning">Running</span>
              <span v-else>Stopped</span>
            </p>
            <p v-if="since" class="text-[11px] opacity-80">since {{ since }}</p>
          </div>
        </div>
        <UiBadge :variant="isPaper ? 'secondary' : 'warning'">
          {{ props.state?.trading_mode?.toUpperCase() || '—' }}
        </UiBadge>
      </div>

      <DashboardRuntimeClock
        :base-seconds="props.state?.accumulated_runtime_seconds || 0"
        :started-at="props.state?.started_at"
        :running="isRunning" />

      <div class="grid grid-cols-2 gap-2">
        <UiButton :disabled="busy || isRunning || isHaltedError" @click="act('/api/strategy/start')">
          <Play class="h-4 w-4" /> Start
        </UiButton>
        <UiButton variant="destructive" :disabled="busy || !isRunning" @click="act('/api/strategy/stop')">
          <Square class="h-4 w-4" /> Stop
        </UiButton>
      </div>

      <div v-if="isHaltedError" class="rounded-md bg-loss/15 p-2.5 text-xs text-loss border border-loss/30">
        <div class="flex items-start gap-2">
          <AlertOctagon class="h-4 w-4 shrink-0 mt-0.5" />
          <div class="space-y-1">
            <p class="font-semibold">Execution Circuit Broken</p>
            <p class="text-[11px] text-loss/90 leading-tight">
              Strategy auto-stopped after execution failures to protect account equity. Fix the root cause before restarting.
            </p>
          </div>
        </div>
      </div>

      <!-- Paper-only manual controls -->
      <div v-if="isPaper" class="flex flex-col gap-2 border-t border-border/60 pt-3">
        <p class="text-[11px] uppercase tracking-wide text-muted-foreground">Manual (paper only)</p>
        <div class="grid grid-cols-2 gap-2">
          <UiButton variant="outline" size="sm" :disabled="busy" @click="act('/api/strategy/enter')">
            <PlusCircle class="h-4 w-4" /> Enter
          </UiButton>
          <UiButton variant="outline" size="sm" :disabled="busy" @click="act('/api/strategy/exit')">
            <MinusCircle class="h-4 w-4" /> Exit
          </UiButton>
        </div>
      </div>

      <p v-if="props.state?.last_message" class="text-xs text-muted-foreground">{{ props.state.last_message }}</p>
      <p v-if="error" class="text-xs text-loss">⚠ {{ error }}</p>
    </div>
  </UiCard>
</template>
