<script setup lang="ts">
// shadcn-style Card with optional title/description via named slots.
defineProps<{ title?: string }>()
</script>

<template>
  <div :class="cn('rounded-xl border bg-card text-card-foreground shadow-sm')">
    <div v-if="$slots.header || title" class="flex flex-col space-y-1.5 p-5 pb-3">
      <slot name="header">
        <h3 class="text-sm font-semibold tracking-tight text-muted-foreground uppercase">
          {{ title }}
        </h3>
      </slot>
    </div>
    <div class="p-5 pt-0">
      <slot />
    </div>
  </div>
</template>
