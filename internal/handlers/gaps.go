package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/store"
)

func querySince(c *gin.Context, defaultDays int) time.Time {
	since := time.Now().UTC().AddDate(0, 0, -defaultDays)
	if v := c.Query("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	if days := c.Query("days"); days != "" {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			return time.Now().UTC().AddDate(0, 0, -n)
		}
	}
	return since
}

func (a *API) PatchWorkOrder(c *gin.Context) {
	var patch store.WorkOrderPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.PatchWorkOrder(c.Request.Context(), c.Param("num"), patch)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) StartWorkOrder(c *gin.Context) {
	item, err := a.Store.StartWorkOrder(c.Request.Context(), c.Param("num"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) CreatePMTemplate(c *gin.Context) {
	var body store.PMTemplate
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.CreatePMTemplate(c.Request.Context(), body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) CreatePMSchedule(c *gin.Context) {
	var body store.PMSchedule
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.NextDueAt.IsZero() {
		body.NextDueAt = time.Now().UTC()
	}
	item, err := a.Store.CreatePMSchedule(c.Request.Context(), body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) ReliabilitySummary(c *gin.Context) {
	plant := c.DefaultQuery("plant", "kampala")
	since := querySince(c, 90)
	items, err := a.Store.ReliabilityByPlant(c.Request.Context(), plant, since)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	pareto, _ := a.Store.DowntimePareto(c.Request.Context(), since, 15)
	losses, _ := a.Store.SixBigLosses(c.Request.Context(), since)
	c.JSON(http.StatusOK, gin.H{
		"plant":          plant,
		"since":          since,
		"assets":         items,
		"downtime_pareto": pareto,
		"six_big_losses": losses,
	})
}

func (a *API) ShiftAnalysis(c *gin.Context) {
	plant := c.DefaultQuery("plant", "kampala")
	since := querySince(c, 14)
	items, err := a.Store.ShiftAnalysis(c.Request.Context(), plant, since)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"plant": plant, "since": since, "shifts": items})
}

func (a *API) DailyProductionReport(c *gin.Context) {
	plant := c.DefaultQuery("plant", "kampala")
	day := time.Now().UTC()
	if v := c.Query("date"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			day = t
		}
	}
	summary, err := a.Store.DailyProductionSummary(c.Request.Context(), plant, day)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, summary)
}

func (a *API) ReportsLibrary(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"templates": []gin.H{
			{"id": "daily-production", "name": "Daily production summary", "endpoint": "/api/v1/reports/daily-production"},
			{"id": "reliability", "name": "Asset reliability (MTBF/MTTR)", "endpoint": "/api/v1/reliability/summary"},
			{"id": "shift-analysis", "name": "Shift comparison", "endpoint": "/api/v1/shift-analysis"},
			{"id": "kpi-summary", "name": "KPI rollup snapshot", "endpoint": "/api/v1/reports/summary"},
			{"id": "quality-summary", "name": "Recent batch quality (MES read-model)", "endpoint": "/api/v1/quality/summary"},
		},
	})
}

func (a *API) QualitySummary(c *gin.Context) {
	since := querySince(c, 30)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	summary, err := a.Store.QualitySummaryFromRuns(c.Request.Context(), since, limit)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, summary)
}
