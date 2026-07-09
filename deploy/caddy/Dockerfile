# Caddy with the Cloudflare DNS provider compiled in, so certificates are issued
# via the ACME DNS-01 challenge (Let's Encrypt). DNS-01 needs no inbound :80 and
# works for a Cloudflare-proxied (orange-cloud) origin, unlike the HTTP-01 flow.
FROM caddy:2.8-builder AS builder
RUN xcaddy build \
    --with github.com/caddy-dns/cloudflare

FROM caddy:2.8-alpine
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
