// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',
  devtools: { enabled: true },

  modules: ['@nuxtjs/supabase', '@nuxtjs/tailwindcss', '@nuxtjs/color-mode', '@nuxt/fonts'],

  css: ['~/assets/css/tailwind.css'],

  colorMode: {
    classSuffix: '', // use `dark` / `light` classes (shadcn convention)
    preference: 'dark',
    fallback: 'dark',
  },

  // API base is overridable at runtime (NUXT_PUBLIC_API_BASE). Supabase URL/key
  // are public values baked at build time (see Dockerfile build args).
  runtimeConfig: {
    public: {
      apiBase: process.env.NUXT_PUBLIC_API_BASE || 'http://localhost:8080',
    },
  },

  supabase: {
    // The @nuxtjs/supabase module reads SUPABASE_URL / SUPABASE_KEY. These are
    // the browser-facing (public) Supabase gateway URL and anon key.
    url: process.env.SUPABASE_URL || 'http://localhost:8000',
    key: process.env.SUPABASE_KEY || '',
    redirectOptions: {
      login: '/login',
      callback: '/confirm',
      include: undefined,
      exclude: ['/login', '/confirm'],
    },
  },
})
