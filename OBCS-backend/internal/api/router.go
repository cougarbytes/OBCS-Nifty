package api

import (
	"embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed docs/openapi.yaml docs/index.html
var docsFS embed.FS

// Router builds the gin engine with middleware and routes.
func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// Do not trust X-Forwarded-* by default: derive ClientIP from RemoteAddr so
	// the rate limiter and logs cannot be spoofed. Set explicit trusted proxy
	// CIDRs here if deploying behind a known load balancer.
	_ = r.SetTrustedProxies(nil)
	r.Use(gin.Recovery())
	r.Use(requestLogger())
	r.Use(corsMiddleware(s.cfg.AllowedOrigins))

	// Public endpoints.
	r.GET("/health", securityHeaders(), s.health)

	// OpenAPI docs (relaxed CSP so the Swagger UI CDN assets load).
	r.GET("/openapi.yaml", securityHeaders(), s.serveOpenAPI)
	r.GET("/docs", s.serveDocs)

	// Protected API: auth (bound to the seeded user) + strict security headers +
	// a moderate global limiter, with a stricter limiter on state-changing writes.
	readRL := newRateLimiter(10, 20) // 10 req/s, burst 20 per IP
	writeRL := newRateLimiter(2, 5)  // 2 req/s, burst 5 per IP
	api := r.Group("/api")
	api.Use(securityHeaders(),
		authMiddleware(s.cfg.SupabaseJWTSecret, s.allowedSub),
		readRL.middleware())
	{
		api.GET("/strategy/state", s.getState)
		api.GET("/strategy/preview", s.preview)
		api.POST("/strategy/start", writeRL.middleware(), s.startStrategy)
		api.POST("/strategy/stop", writeRL.middleware(), s.stopStrategy)
		api.POST("/strategy/enter", writeRL.middleware(), s.enterManual)
		api.POST("/strategy/exit", writeRL.middleware(), s.exitManual)

		api.GET("/trades", s.listTrades)
		api.GET("/trades/:id/options", s.getTradeOptions)
		api.GET("/trades/:id/computed", s.getTradeComputed)

		api.GET("/pnl/daily", s.getDailyPnL)
		api.GET("/holidays", s.getHolidays)
	}
	return r
}

func (s *Server) serveOpenAPI(c *gin.Context) {
	b, err := docsFS.ReadFile("docs/openapi.yaml")
	if err != nil {
		c.String(http.StatusInternalServerError, "openapi spec unavailable")
		return
	}
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", b)
}

func (s *Server) serveDocs(c *gin.Context) {
	b, err := docsFS.ReadFile("docs/index.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "docs unavailable")
		return
	}
	// Relaxed CSP scoped to this route only.
	c.Header("Content-Security-Policy",
		"default-src 'self'; script-src 'self' https://unpkg.com 'unsafe-inline'; "+
			"style-src 'self' https://unpkg.com 'unsafe-inline'; img-src 'self' data:; "+
			"connect-src 'self'")
	c.Data(http.StatusOK, "text/html; charset=utf-8", b)
}
