# ── build stage ──────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Build the static binary.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/backend .

# ── runtime stage ────────────────────────────────────────────────────────────
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=build /out/backend /app/backend

# Run as non-root (OWASP: least privilege).
USER appuser
EXPOSE 8080
ENTRYPOINT ["/app/backend"]
