package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (a *API) ListKPIDefinitions(c *gin.Context) {
	items, err := a.Store.ListKPIDefinitions(c.Request.Context())
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) ListKPISnapshots(c *gin.Context) {
	items, err := a.Store.ListKPISnapshots(c.Request.Context(), c.Query("kpi"), 100)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) ListAlertRules(c *gin.Context) {
	items, err := a.Store.ListAlertRules(c.Request.Context())
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) ListAlerts(c *gin.Context) {
	items, err := a.Store.ListAlerts(c.Request.Context(), c.Query("status"), 50)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) AckAlert(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	item, err := a.Store.AckAlert(c.Request.Context(), id)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) ResolveAlert(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	item, err := a.Store.ResolveAlert(c.Request.Context(), id)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) ReportsSummary(c *gin.Context) {
	ctx := c.Request.Context()
	kpis, _ := a.Store.ListKPISnapshots(ctx, "", 30)
	alerts, _ := a.Store.ListAlerts(ctx, "", 20)
	downtime, _ := a.Store.ListDowntimeEvents(ctx, "", 20)
	c.JSON(http.StatusOK, gin.H{
		"kpi_snapshots":   kpis,
		"recent_alerts":   alerts,
		"recent_downtime": downtime,
	})
}
