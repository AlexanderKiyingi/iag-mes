package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/auditlog"
	"iag-mes/backend/internal/config"
	"iag-mes/backend/internal/db"
	"iag-mes/backend/internal/events"
	"iag-mes/backend/internal/middleware"

	"github.com/jackc/pgx/v5/pgxpool"
)

type RouterDeps struct {
	Cfg   config.Config
	Pool  *pgxpool.Pool
	Pub   *events.Publisher
	Audit *auditlog.Store
}

func NewRouter(deps RouterDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestAudit(deps.Audit))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": deps.Cfg.ServiceName})
	})
	r.GET("/ready", func(c *gin.Context) {
		if err := db.Ping(c.Request.Context(), deps.Pool); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "database": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready", "database": true})
	})

	prod := &Production{Pub: deps.Pub}
	admin := &Admin{Cfg: deps.Cfg, Audit: deps.Audit}

	v1 := r.Group("/api/v1")
	{
		v1.POST("/production-orders", prod.PostProductionOrder)

		adm := v1.Group("/admin", middleware.RequireBearer())
		{
			adm.GET("/audit-logs", admin.ListAPIAuditLogs)
			adm.GET("/monitoring/summary", admin.MonitoringSummary)
			adm.GET("/monitoring/activity", admin.MonitoringActivity)
		}
	}
	return r
}
