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
  <div class="min-h-screen bg-background">
    <header class="sticky top-0 z-20 border-b border-border bg-background/80 backdrop-blur">
      <div class="mx-auto flex max-w-7xl items-center justify-between px-4 py-3">
        <div class="flex items-center gap-2">
          <TrendingUp class="h-6 w-6 text-primary" />
          <div class="leading-tight">
            <p class="text-sm font-bold tracking-tight">OBCS<span class="text-primary">·</span>Nifty</p>
            <p class="text-[10px] uppercase text-muted-foreground">Overnight Bull Call Spread</p>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <ThemeToggle />
          <UiButton v-if="user" variant="ghost" size="icon" aria-label="Sign out" @click="logout">
            <LogOut class="h-5 w-5" />
          </UiButton>
        </div>
      </div>
    </header>
    <main class="mx-auto max-w-7xl px-4 py-6">
      <slot />
    </main>
  </div>
</template>
