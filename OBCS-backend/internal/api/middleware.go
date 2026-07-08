package api

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// authMiddleware verifies the Supabase-issued access token (HS256, signed with
// SUPABASE_JWT_SECRET). It enforces signature, expiry and the "authenticated"
// audience/role so only logged-in UI users reach protected routes
// (OWASP A01/A07). When allowedSub is non-empty the token's subject must match
// the single provisioned app user, so a self-registered Supabase account cannot
// control the strategy even if it obtains an authenticated token.
func authMiddleware(jwtSecret, allowedSub string) gin.HandlerFunc {
	secret := []byte(jwtSecret)
	return func(c *gin.Context) {
		authz := c.GetHeader("Authorization")
		if !strings.HasPrefix(authz, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		tokenStr := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))

		claims := jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return secret, nil
		}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		if !hasAudience(claims["aud"], "authenticated") {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient audience"})
			return
		}
		sub, _ := claims["sub"].(string)
		if allowedSub != "" && sub != allowedSub {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not the authorized application user"})
			return
		}
		if sub != "" {
			c.Set("user_id", sub)
		}
		c.Next()
	}
}

// hasAudience checks the aud claim, which Supabase may encode as a string or an
// array of strings.
func hasAudience(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		for _, a := range v {
			if s, ok := a.(string); ok && s == want {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if s == want {
				return true
			}
		}
	}
	return false
}

// securityHeaders applies conservative response headers (OWASP A05).
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		// HSTS assumes TLS termination in front of the service.
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		c.Next()
	}
}

// corsMiddleware allows only the configured origins and the methods/headers the
// SPA needs. Credentials are allowed for cookie/session flows.
func corsMiddleware(allowed []string) gin.HandlerFunc {
	allowSet := make(map[string]bool, len(allowed))
	for _, o := range allowed {
		allowSet[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && allowSet[origin] {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization,Content-Type")
			// Auth uses bearer tokens (not cookies), so credentialed CORS is
			// unnecessary and intentionally omitted.
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// rateLimiter is a simple per-IP token bucket guarding against abuse of the
// state-changing endpoints (OWASP A04). It is intentionally lightweight; a
// production deployment should front this with a gateway/WAF.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   float64
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(ratePerSec, burst float64) *rateLimiter {
	rl := &rateLimiter{buckets: map[string]*bucket{}, rate: ratePerSec, burst: burst}
	// Evict idle buckets so the map cannot grow unbounded (A04/DoS hardening).
	go rl.sweep()
	return rl
}

func (rl *rateLimiter) sweep() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		rl.mu.Lock()
		for k, b := range rl.buckets {
			if b.last.Before(cutoff) {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		rl.buckets[key] = &bucket{tokens: rl.burst - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() * rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (rl *rateLimiter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.allow(c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}
