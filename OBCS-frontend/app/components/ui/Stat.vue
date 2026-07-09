<script setup lang="ts">
// Compact "terminal" stat tile: uppercase micro-label, large tabular value,
// optional icon and sub-line. Tone tints the value + icon for gain/loss.
defineProps<{
  label: string
  tone?: 'default' | 'gain' | 'loss'
  loading?: boolean
}>()

const toneClass: Record<string, string> = {
  default: 'text-foreground',
  gain: 'text-gain',
  loss: 'text-loss',
}
</script>

<template>
  <div class="group relative overflow-hidden rounded-xl border border-border bg-elevated p-4">
    <!-- hairline accent, brightens on hover -->
    <span
      class="pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-border to-transparent transition-opacity group-hover:via-primary/50" />
    <div class="flex items-center justify-between">
      <p class="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">{{ label }}</p>
      <span :class="cn('opacity-70', toneClass[tone ?? 'default'])"><slot name="icon" /></span>
    </div>
    <div class="mt-2 flex items-baseline gap-2">
      <UiSkeleton v-if="loading" class="h-8 w-28" />
      <span v-else :class="cn('font-mono text-2xl font-bold tracking-tight tabular-nums', toneClass[tone ?? 'default'])">
        <slot />
      </span>
      <span class="text-xs text-muted-foreground"><slot name="sub" /></span>
    </div>
  </div>
</template>
