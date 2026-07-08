package api

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// requestLogger emits a structured line per request. It deliberately logs only
// method, path, status and latency — never request bodies, tokens or query
// secrets (OWASP A09: logging without leaking sensitive data).
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("http",
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
		)
	}
}
