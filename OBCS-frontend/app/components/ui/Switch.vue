<script setup lang="ts">
// Minimal shadcn-style toggle switch (accessible, no external primitive dep).
const model = defineModel<boolean>({ default: false })
defineProps<{ disabled?: boolean }>()
</script>

<template>
  <button
    type="button"
    role="switch"
    :aria-checked="model"
    :disabled="disabled"
    @click="model = !model"
    :class="cn(
      'peer inline-flex h-6 w-11 shrink-0 items-center rounded-full border-2 border-transparent transition-colors motion-reduce:transition-none focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
      model ? 'bg-primary' : 'bg-input',
    )">
    <span :class="cn(
      'pointer-events-none block h-5 w-5 rounded-full bg-background shadow-lg ring-0 transition-transform motion-reduce:transition-none',
      model ? 'translate-x-5' : 'translate-x-0',
    )" />
  </button>
</template>
