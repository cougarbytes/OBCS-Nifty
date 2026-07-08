<script setup lang="ts">
import { LogIn } from 'lucide-vue-next'

// Public login page. @nuxtjs/supabase redirects unauthenticated users here.
definePageMeta({ layout: 'default' })

const supabase = useSupabaseClient()
const user = useSupabaseUser()

const email = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)

watchEffect(() => {
  if (user.value) navigateTo('/')
})

async function signIn() {
  busy.value = true
  error.value = ''
  const { error: e } = await supabase.auth.signInWithPassword({
    email: email.value.trim(),
    password: password.value,
  })
  if (e) error.value = e.message
  busy.value = false
}
</script>

<template>
  <div class="flex min-h-[70vh] items-center justify-center">
    <UiCard class="w-full max-w-sm">
      <template #header>
        <h1 class="text-lg font-bold">Sign in</h1>
        <p class="text-xs text-muted-foreground">
          Use the generated credentials printed by the backend on first boot.
        </p>
      </template>
      <form class="flex flex-col gap-3" @submit.prevent="signIn">
        <div class="flex flex-col gap-1">
          <label class="text-xs text-muted-foreground" for="email">Email</label>
          <input id="email" v-model="email" type="email" autocomplete="username"
            class="h-9 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            placeholder="silver-tiger@obcs.local" required />
        </div>
        <div class="flex flex-col gap-1">
          <label class="text-xs text-muted-foreground" for="password">Password</label>
          <input id="password" v-model="password" type="password" autocomplete="current-password"
            class="h-9 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            required />
        </div>
        <UiButton type="submit" :disabled="busy">
          <LogIn class="h-4 w-4" /> {{ busy ? 'Signing in…' : 'Sign in' }}
        </UiButton>
        <p v-if="error" class="text-xs text-loss">⚠ {{ error }}</p>
      </form>
    </UiCard>
  </div>
</template>
