package middleware

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/auditlog"
)

func RequestAudit(store *auditlog.MemoryStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if store == nil {
			return
		}
		path := c.Request.URL.Path
		if path == "/health" || path == "/healthz" || path == "/ready" {
			return
		}
		_ = store.LogAPIRequest(
			c.Request.Context(),
			c.Request.Method,
			path,
			c.Writer.Status(),
			ActorLabel(c),
			int(time.Since(start).Milliseconds()),
			c.ClientIP(),
		)
	}
}

func ActorLabel(c *gin.Context) string {
	if v := strings.TrimSpace(c.GetHeader("X-User-Email")); v != "" {
		return v
	}
	if strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ") {
		return "authenticated"
	}
	return "anonymous"
}
