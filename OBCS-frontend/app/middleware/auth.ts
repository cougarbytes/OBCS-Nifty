// Route guard: redirect unauthenticated users to /login. Complements the
// @nuxtjs/supabase module redirect for defence in depth.
export default defineNuxtRouteMiddleware((to) => {
  const user = useSupabaseUser()
  if (!user.value && to.path !== '/login') {
    return navigateTo('/login')
  }
})
