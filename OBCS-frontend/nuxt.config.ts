// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  ssr: false, // <--- Add this to fix the Docker NAT loopback issue
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
    url: process.env.SUPABASE_URL || 'http://localhost:8000',
    key: process.env.SUPABASE_KEY || '',
    redirect: false, // <--- Disable the aggressive automatic global redirect guard
    // The session lives in an `sb-*-auth-token` cookie. By default the module
    // marks it `Secure`, which browsers silently drop over plain HTTP on a public
    // host (localhost is exempt) — the session never persists, so login bounces
    // straight back to /login. `secure: false` lets the cookie survive over HTTP.
    // INSECURE: the session token then travels in cleartext — trusted networks
    // only. Restore `secure: true` (the module default) once served over HTTPS.
    cookieOptions: {
      sameSite: 'lax',
      secure: false,
    },
  },
})
