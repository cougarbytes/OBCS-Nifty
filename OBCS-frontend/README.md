# OBCS-Nifty — Frontend

Dashboard for the **Overnight Bull Call Spread** options strategy on the NIFTY
index. Shows live strategy state, PnL, and trade history.

- **Framework:** Nuxt 4 (Vue 3, `<script setup>`)
- **Auth / realtime:** Supabase (`@nuxtjs/supabase`) — bearer token forwarded to
  the Go backend via `useApi()`
- **Styling:** Tailwind (`@nuxtjs/tailwindcss`) with shadcn-vue design tokens
- **Theme:** dark-first, light supported (`@nuxtjs/color-mode`, `dark`/`light` class)
- **Icons:** `lucide-vue-next`

## Design language

Futuristic, dark-first, OKX-inspired. Material spacing/elevation, generous
negative space. Trading semantics: **teal = gain**, **red = loss**.

All colors are driven by CSS variables in `app/assets/css/tailwind.css` and
mapped in `tailwind.config.ts`. Use the semantic utilities (`text-gain`,
`text-loss`, `bg-gain/15`, `border-border`, `bg-card`, `text-muted-foreground`,
…) rather than hardcoded hex/hsl so every element respects the active theme.

## Structure

| Path | Purpose |
| --- | --- |
| `app/layouts/default.vue` | App shell: sticky header, theme toggle, sign-out |
| `app/pages/index.vue` | Dashboard: summary cards + widget grid |
| `app/components/ui/` | shadcn-style primitives (Button, Card, Badge, Table…) |
| `app/components/dashboard/` | StrategyControls, RuntimeClock, PnlChart, PnlHeatmap, TradeTable |
| `app/composables/useApi.ts` | Authenticated `$fetch` wrapper to the backend |
| `app/types.ts` | API contract types (mirror the Go models) |

## Configuration

| Env var | Default | Notes |
| --- | --- | --- |
| `NUXT_PUBLIC_API_BASE` | `http://localhost:8080` | Go backend base URL |
| `SUPABASE_URL` | `http://localhost:8000` | Public Supabase gateway |
| `SUPABASE_KEY` | — | Supabase anon key |

## Develop

```bash
bun install
bun run dev      # http://localhost:3000
bun run build    # production build
bun run preview
```
