<script setup lang="ts">
import { Play, Square, PlusCircle, MinusCircle } from 'lucide-vue-next'
import type { StrategyState } from '~/types'

const props = defineProps<{ state: StrategyState | null }>()
const emit = defineEmits<{ (e: 'changed'): void }>()

const api = useApi()
const busy = ref(false)
const error = ref('')

const isRunning = computed(() => props.state?.status === 'running')
const isPaper = computed(() => props.state?.trading_mode === 'paper')

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
      <div class="flex items-center justify-between rounded-lg border border-border bg-elevated px-3 py-2.5">
        <div class="flex items-center gap-2.5">
          <span class="relative flex h-2.5 w-2.5">
            <span v-if="isRunning" class="absolute inline-flex h-full w-full animate-ping rounded-full bg-gain opacity-75 motion-reduce:hidden" />
            <span :class="cn('relative inline-flex h-2.5 w-2.5 rounded-full', isRunning ? 'bg-gain' : 'bg-muted-foreground')" />
          </span>
          <div class="leading-tight">
            <p class="text-sm font-semibold">{{ isRunning ? 'Running' : 'Stopped' }}</p>
            <p v-if="since" class="text-[11px] text-muted-foreground">since {{ since }}</p>
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
        <UiButton :disabled="busy || isRunning" @click="act('/api/strategy/start')">
          <Play class="h-4 w-4" /> Start
        </UiButton>
        <UiButton variant="destructive" :disabled="busy || !isRunning" @click="act('/api/strategy/stop')">
          <Square class="h-4 w-4" /> Stop
        </UiButton>
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
