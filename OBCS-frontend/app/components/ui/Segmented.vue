<script setup lang="ts">
// Accessible segmented control (shadcn "tabs"-style pill group). Drives a
// v-model string; used e.g. to switch the P&L chart between Daily / Cumulative.
interface Option { label: string; value: string }
defineProps<{ options: Option[] }>()
const model = defineModel<string>({ required: true })
</script>

<template>
  <div
    role="tablist"
    class="inline-flex items-center gap-0.5 rounded-lg border border-border bg-muted/40 p-0.5">
    <button
      v-for="opt in options"
      :key="opt.value"
      type="button"
      role="tab"
      :aria-selected="model === opt.value"
      :tabindex="model === opt.value ? 0 : -1"
      @click="model = opt.value"
      :class="cn(
        'rounded-md px-2.5 py-1 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
        model === opt.value
          ? 'bg-background text-foreground shadow-sm'
          : 'text-muted-foreground hover:text-foreground',
      )">
      {{ opt.label }}
    </button>
  </div>
</template>
