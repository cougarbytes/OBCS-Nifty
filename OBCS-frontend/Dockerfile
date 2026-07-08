# ── build stage ──────────────────────────────────────────────────────────────
FROM oven/bun:1.3 AS build
WORKDIR /app

# Public (browser-facing) config is baked at build time. These are not secrets:
# the Supabase anon key and public gateway URL are safe to ship to the client.
ARG NUXT_PUBLIC_API_BASE=http://localhost:8080
ARG SUPABASE_URL=http://localhost:8000
ARG SUPABASE_KEY=
ENV NUXT_PUBLIC_API_BASE=$NUXT_PUBLIC_API_BASE \
    SUPABASE_URL=$SUPABASE_URL \
    SUPABASE_KEY=$SUPABASE_KEY

COPY package.json bun.lock* ./
RUN bun install --frozen-lockfile || bun install
COPY . .
RUN bun run build

# ── runtime stage ────────────────────────────────────────────────────────────
FROM node:24-alpine
RUN adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=build /app/.output /app/.output
USER appuser
ENV NITRO_HOST=0.0.0.0 NITRO_PORT=3000 PORT=3000
EXPOSE 3000
CMD ["node", ".output/server/index.mjs"]
