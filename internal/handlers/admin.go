package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/jobs"
)

func (a *API) ListAPIAuditLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	items, total, err := a.Audit.ListAPIAuditLogs(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list audit logs"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

func (a *API) MonitoringSummary(c *gin.Context) {
	summary, err := a.Audit.MonitoringSummary(c.Request.Context(), a.Bus != nil && a.Bus.Enabled())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "monitoring failed"})
		return
	}
	c.JSON(http.StatusOK, summary)
}

func (a *API) MonitoringActivity(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	items, err := a.Audit.APIMonitoringActivity(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "activity failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) AdminConfig(c *gin.Context) {
	upstreams := map[string]bool{
		"warehouse": a.Cfg.UpstreamWarehouse != "",
		"qc":        a.Cfg.UpstreamQC != "",
		"erp":       a.Cfg.UpstreamERP != "",
		"scm":       a.Cfg.UpstreamSCM != "",
	}
	if a.Bridge != nil {
		upstreams = a.Bridge.Status()
	}
	c.JSON(http.StatusOK, gin.H{
		"service":     a.Cfg.ServiceName,
		"environment": a.Cfg.Environment,
		"auth_mode":   a.Cfg.AuthMode,
		"integrations": gin.H{
			"enabled":                        a.Cfg.IntegrationsEnabled,
			"auto_warehouse_on_run_complete": a.Cfg.AutoWarehouseOnRunComplete,
			"auto_qc_on_run_complete":        a.Cfg.AutoQCOnRunComplete,
			"auto_validate_batch_scm":        a.Cfg.AutoValidateBatchWithSCM,
			"upstreams":                      upstreams,
		},
		"kafka": gin.H{
			"enabled":      a.Bus != nil && a.Bus.Enabled(),
			"brokers_set":  len(a.Cfg.KafkaBrokers) > 0,
			"production":   a.Cfg.KafkaProductionTopic,
			"operations":   a.Cfg.KafkaOperationsTopic,
			"supply_chain": a.Cfg.KafkaSupplyChainTopic,
			"quality":      a.Cfg.KafkaQualityTopic,
			"consumer_group": a.Cfg.KafkaConsumerGroup,
		},
		"gateway_api_prefix": a.Cfg.GatewayAPIPrefix,
	})
}

func (a *API) AdminRunJob(c *gin.Context) {
	ctx := c.Request.Context()
	job := strings.ToLower(strings.TrimSpace(c.Param("job")))
	plant := c.DefaultQuery("plant", "kampala")

	var (
		result any
		err    error
	)

	switch job {
	case "ai", "ai-recommendations":
		n, e := jobs.GenerateAIRecommendations(ctx, a.Store)
		result = gin.H{"created": n}
		err = e
	case "energy", "energy-insights":
		n, e := jobs.GenerateEnergyInsights(ctx, a.Store, plant)
		result = gin.H{"created": n, "plant": plant}
		err = e
	case "kpi", "kpi-rollup":
		n, e := jobs.RollupKPIs(ctx, a.Store, plant)
		result = gin.H{"snapshots": n, "plant": plant}
		err = e
	case "alerts", "telemetry-alerts":
		n, e := jobs.EvaluateTelemetryAlerts(ctx, a.Store)
		result = gin.H{"created": n}
		err = e
	case "preventive-maintenance", "preventive-maintenance-sync":
		created, overdue, e := jobs.SyncPreventiveMaintenance(ctx, a.Store)
		result = gin.H{"work_orders_created": created, "schedules_marked_overdue": overdue}
		err = e
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "unknown job",
			"allowed": []string{
				"ai-recommendations", "energy-insights",
				"kpi-rollup", "telemetry-alerts", "preventive-maintenance-sync",
			},
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": job, "result": result})
}
