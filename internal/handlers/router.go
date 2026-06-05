package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/auditlog"
	"iag-mes/backend/internal/config"
	"iag-mes/backend/internal/events"
	"iag-mes/backend/internal/middleware"
)

type RouterDeps struct {
	Cfg   config.Config
	Pub   *events.Publisher
	Audit *auditlog.MemoryStore
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
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
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
