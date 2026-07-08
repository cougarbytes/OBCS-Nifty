<script setup lang="ts">
// OAuth/magic-link callback target for @nuxtjs/supabase. For password auth this
// simply forwards to the dashboard once the session is established.
const user = useSupabaseUser()
watchEffect(() => {
  if (user.value) navigateTo('/')
})
</script>

<template>
  <div class="flex min-h-[60vh] items-center justify-center text-sm text-muted-foreground">
    Completing sign-in…
  </div>
</template>
