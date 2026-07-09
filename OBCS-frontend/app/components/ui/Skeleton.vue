<script setup lang="ts">
// Loading placeholder. Pulses unless the user prefers reduced motion. Sizing is
// passed via class (merged onto the root through attribute fallthrough).
</script>

<template>
  <div aria-hidden="true" class="animate-pulse rounded-md bg-muted/60 motion-reduce:animate-none" />
</template>
