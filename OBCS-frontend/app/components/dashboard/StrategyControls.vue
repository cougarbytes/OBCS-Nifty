<script setup lang="ts">
import { Play, Square, PlusCircle, MinusCircle, Activity } from 'lucide-vue-next'
import type { StrategyState } from '~/types'

const props = defineProps<{ state: StrategyState | null }>()
const emit = defineEmits<{ (e: 'changed'): void }>()

const api = useApi()
const busy = ref(false)
const error = ref('')

const isRunning = computed(() => props.state?.status === 'running')
const isPaper = computed(() => props.state?.trading_mode === 'paper')

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
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <Activity :class="cn('h-4 w-4', isRunning ? 'text-gain animate-pulse motion-reduce:animate-none' : 'text-muted-foreground')" />
          <span class="text-lg font-semibold">
            {{ isRunning ? 'Running' : 'Stopped' }}
          </span>
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

      <div v-if="isPaper" class="grid grid-cols-2 gap-2">
        <UiButton variant="outline" size="sm" :disabled="busy" @click="act('/api/strategy/enter')">
          <PlusCircle class="h-4 w-4" /> Paper Enter
        </UiButton>
        <UiButton variant="outline" size="sm" :disabled="busy" @click="act('/api/strategy/exit')">
          <MinusCircle class="h-4 w-4" /> Paper Exit
        </UiButton>
      </div>

      <p v-if="props.state?.last_message" class="text-xs text-muted-foreground">
        {{ props.state.last_message }}
      </p>
      <p v-if="error" class="text-xs text-loss">⚠ {{ error }}</p>
    </div>
  </UiCard>
</template>
