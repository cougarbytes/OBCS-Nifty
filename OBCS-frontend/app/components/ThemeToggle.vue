<script setup lang="ts">
import { Moon, Sun } from 'lucide-vue-next'
// @nuxtjs/color-mode exposes useColorMode() (auto-imported).
const colorMode = useColorMode()
function toggle() {
  colorMode.preference = colorMode.value === 'dark' ? 'light' : 'dark'
}
</script>

<template>
  <UiButton variant="ghost" size="icon" aria-label="Toggle theme" @click="toggle">
    <ClientOnly>
      <Moon v-if="colorMode.value === 'dark'" class="h-5 w-5" />
      <Sun v-else class="h-5 w-5" />
      <template #fallback><span class="h-5 w-5" /></template>
    </ClientOnly>
  </UiButton>
</template>
