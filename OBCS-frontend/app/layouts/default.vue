<script setup lang="ts">
import { TrendingUp, LogOut } from 'lucide-vue-next'

const supabase = useSupabaseClient()
const user = useSupabaseUser()

async function logout() {
  await supabase.auth.signOut()
  await navigateTo('/login')
}
</script>

<template>
  <div class="relative min-h-screen bg-background">
    <!-- Signature: a single quiet teal glow anchored to the top of the terminal. -->
    <div
      aria-hidden="true"
      class="pointer-events-none fixed inset-x-0 top-0 h-64 bg-[radial-gradient(ellipse_60%_100%_at_50%_0%,hsl(var(--primary)/0.10),transparent_70%)]" />

    <header class="sticky top-0 z-20 border-b border-border bg-background/80 backdrop-blur">
      <div class="mx-auto flex max-w-7xl items-center justify-between px-4 py-3">
        <NuxtLink to="/" class="flex items-center gap-2.5 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <TrendingUp class="h-5 w-5" />
          </span>
          <div class="leading-tight">
            <p class="text-sm font-bold tracking-tight">OBCS<span class="text-primary">·</span>Nifty</p>
          </div>
        </NuxtLink>
        <div class="flex items-center gap-1">
          <ThemeToggle />
          <UiButton v-if="user" variant="ghost" size="icon" aria-label="Sign out" @click="logout">
            <LogOut class="h-5 w-5" />
          </UiButton>
        </div>
      </div>
    </header>

    <main class="relative mx-auto max-w-7xl px-4 py-6">
      <slot />
    </main>
  </div>
</template>
