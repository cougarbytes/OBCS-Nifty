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
    // The session lives in an `sb-*-auth-token` cookie. Over HTTPS it MUST be
    // `Secure` (the default). Browsers drop `Secure` cookies over plain HTTP on a
    // public host (localhost is exempt), which causes a login → /login redirect
    // loop — so only for a plain-HTTP deployment on a trusted network, build with
    // NUXT_SUPABASE_COOKIE_SECURE=false. This is baked at build time (SPA); see
    // OBCS-frontend/README.md → "Secure cookies in production".
    cookieOptions: {
      sameSite: 'lax',
      secure: process.env.NUXT_SUPABASE_COOKIE_SECURE !== 'false',
    },
  },

  robots: {
    disallow: '/',
    blockAiBots: true
  },
})
