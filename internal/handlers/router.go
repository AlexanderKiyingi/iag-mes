package handlers

import (
	"net/http"
	"strings"

	"github.com/alvor-technologies/iag-platform-go/middleware"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"iag-mes/backend/internal/auditlog"
	appmw "iag-mes/backend/internal/middleware"
)

type RouterDeps struct {
	API          *API
	Audit        *auditlog.Store
	PlatformAuth *appmw.PlatformAuth
	CORSOrigins  []string
	StrictRBAC   bool
}

func NewRouter(deps RouterDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(otelgin.Middleware(deps.API.Cfg.ServiceName))
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(securityHeaders())
	r.Use(corsMiddleware(deps.CORSOrigins))

	api := deps.API
	if deps.PlatformAuth != nil {
		r.Use(deps.PlatformAuth.AttachPrincipal())
	}
	r.Use(appmw.RequestAudit(deps.Audit))

	r.GET("/health", api.Health)
	r.GET("/healthz", api.Health)
	r.GET("/ready", api.Ready)

	v1 := r.Group("/api/v1")
	if deps.PlatformAuth != nil {
		v1.Use(deps.PlatformAuth.RequireAuth())
	}
	if deps.StrictRBAC {
		v1.Use(appmw.StrictRBAC())
	}
	{
		v1.GET("/platform/status", appmw.RequireStaff(), api.PlatformStatus)
		v1.GET("/bootstrap", appmw.RequirePermission("mes.view_overview"), api.Bootstrap)

		v1.GET("/plants", appmw.RequirePermission("mes.view_plant"), api.ListPlants)
		v1.POST("/plants", appmw.RequirePermission("mes.change_plant"), api.CreatePlant)
		v1.GET("/plants/:code", appmw.RequirePermission("mes.view_plant"), api.GetPlant)
		v1.GET("/sections", appmw.RequirePermission("mes.view_plant"), api.ListSections)
		v1.POST("/plants/:code/sections", appmw.RequirePermission("mes.change_plant"), api.CreateSection)
		v1.GET("/plants/:code/shifts", appmw.RequirePermission("mes.view_shift"), api.ListShiftDefinitions)

		v1.GET("/assets", appmw.RequirePermission("mes.view_asset"), api.ListAssets)
		v1.POST("/assets", appmw.RequirePermission("mes.change_asset"), api.CreateAsset)
		v1.GET("/assets/:tag", appmw.RequirePermission("mes.view_asset"), api.GetAsset)
		v1.PATCH("/assets/:tag", appmw.RequirePermission("mes.change_asset"), api.PatchAsset)
		v1.GET("/assets/:tag/telemetry", appmw.RequirePermission("mes.view_telemetry"), api.ListTelemetry)
		v1.GET("/telemetry/history", appmw.RequirePermission("mes.view_telemetry"), api.ListTelemetryHistory)
		v1.POST("/telemetry/ingest", appmw.RequirePermission("mes.change_asset"), api.IngestTelemetryBatch)
		v1.POST("/assets/:tag/telemetry", appmw.RequirePermission("mes.change_asset"), api.UpsertTelemetry)

		v1.GET("/work-orders", appmw.RequirePermission("mes.view_work_order"), api.ListWorkOrders)
		v1.POST("/work-orders", appmw.RequirePermission("mes.add_work_order"), api.CreateWorkOrder)
		v1.GET("/work-orders/:num", appmw.RequirePermission("mes.view_work_order"), api.GetWorkOrder)
		v1.PATCH("/work-orders/:num", appmw.RequirePermission("mes.change_work_order"), api.PatchWorkOrder)
		v1.POST("/work-orders/:num/start", appmw.RequirePermission("mes.change_work_order"), api.StartWorkOrder)
		v1.POST("/work-orders/:num/complete", appmw.RequirePermission("mes.complete_work_order"), api.CompleteWorkOrder)

		v1.GET("/downtime-events", appmw.RequirePermission("mes.view_downtime"), api.ListDowntimeEvents)
		v1.POST("/downtime-events", appmw.RequirePermission("mes.add_downtime"), api.CreateDowntimeEvent)
		v1.POST("/downtime-events/:id/end", appmw.RequirePermission("mes.add_downtime"), api.EndDowntimeEvent)

		v1.GET("/pm-templates", appmw.RequirePermission("mes.view_work_order"), api.ListPMTemplates)
		v1.POST("/pm-templates", appmw.RequirePermission("mes.add_work_order"), api.CreatePMTemplate)
		v1.GET("/pm-schedules", appmw.RequirePermission("mes.view_work_order"), api.ListPMSchedules)
		v1.POST("/pm-schedules", appmw.RequirePermission("mes.add_work_order"), api.CreatePMSchedule)
		v1.GET("/maintenance/calendar", appmw.RequirePermission("mes.view_work_order"), api.MaintenanceCalendar)

		v1.GET("/kpis/definitions", appmw.RequirePermission("mes.view_kpi"), api.ListKPIDefinitions)
		v1.GET("/kpis/snapshots", appmw.RequirePermission("mes.view_kpi"), api.ListKPISnapshots)
		v1.GET("/alert-rules", appmw.RequirePermission("mes.view_alert"), api.ListAlertRules)
		v1.GET("/alerts", appmw.RequirePermission("mes.view_alert"), api.ListAlerts)
		v1.POST("/alerts/:id/ack", appmw.RequirePermission("mes.ack_alert"), api.AckAlert)
		v1.POST("/alerts/:id/resolve", appmw.RequirePermission("mes.ack_alert"), api.ResolveAlert)
		v1.GET("/reports/summary", appmw.RequirePermission("mes.view_kpi"), api.ReportsSummary)
		v1.GET("/reports/library", appmw.RequirePermission("mes.view_kpi"), api.ReportsLibrary)
		v1.GET("/reports/daily-production", appmw.RequirePermission("mes.view_kpi"), api.DailyProductionReport)
		v1.GET("/reliability/summary", appmw.RequirePermission("mes.view_kpi"), api.ReliabilitySummary)
		v1.GET("/shift-analysis", appmw.RequirePermission("mes.view_kpi"), api.ShiftAnalysis)

		v1.GET("/ai/recommendations", appmw.RequirePermission("mes.view_ai"), api.ListAIRecommendations)
		v1.POST("/ai/recommendations/:id/accept", appmw.RequirePermission("mes.change_ai"), api.AcceptAIRecommendation)
		v1.POST("/ai/recommendations/:id/dismiss", appmw.RequirePermission("mes.change_ai"), api.DismissAIRecommendation)

		v1.GET("/energy/summary", appmw.RequirePermission("mes.view_energy"), api.EnergySummary)
		v1.POST("/energy/readings", appmw.RequirePermission("mes.add_energy"), api.RecordEnergyReading)

		v1.GET("/integrations/status", appmw.RequirePermission("mes.view_overview"), api.IntegrationStatus)

		admin := v1.Group("/admin")
		{
			adminRead := admin.Group("")
			adminRead.Use(appmw.RequirePermission("mes.admin.read"))
			{
				adminRead.GET("/audit-logs", api.ListAPIAuditLogs)
				adminRead.GET("/monitoring/summary", api.MonitoringSummary)
				adminRead.GET("/monitoring/activity", api.MonitoringActivity)
				adminRead.GET("/config", api.AdminConfig)
				adminRead.GET("/integrations/calls", api.ListIntegrationCalls)
			}

			adminWrite := admin.Group("")
			adminWrite.Use(appmw.RequireAnyPermission("mes.admin.write", "mes.sync_integrations"))
			{
				adminWrite.POST("/integrations/warehouse/consume", api.WarehouseConsume)
				adminWrite.POST("/integrations/warehouse/output", api.WarehouseOutput)
				adminWrite.POST("/integrations/qc/sample", api.SubmitQCSample)
				adminWrite.POST("/jobs/:job", api.AdminRunJob)
			}
		}
	}

	return r
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}

func corsMiddleware(origins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		allowed[strings.TrimSpace(o)] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if _, ok := allowed["*"]; ok {
				c.Header("Access-Control-Allow-Origin", "*")
			} else if _, ok := allowed[origin]; ok {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Vary", "Origin")
			}
		}
		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", "GET,POST,PATCH,PUT,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-Id")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
