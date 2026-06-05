package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/auditlog"
	"iag-mes/backend/internal/config"
)

type Admin struct {
	Cfg   config.Config
	Audit *auditlog.Store
}

func (a *Admin) ListAPIAuditLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	items, total, err := a.Audit.ListAPIAuditLogs(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list audit logs"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

func (a *Admin) MonitoringSummary(c *gin.Context) {
	summary, err := a.Audit.MonitoringSummary(c.Request.Context(), len(a.Cfg.KafkaBrokers) > 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "monitoring failed"})
		return
	}
	c.JSON(http.StatusOK, summary)
}

func (a *Admin) MonitoringActivity(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	items, err := a.Audit.APIMonitoringActivity(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "activity failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
