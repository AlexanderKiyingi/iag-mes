package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-mes/backend/internal/clients"
	"iag-mes/backend/internal/store"
)

func (a *API) ListTelemetry(c *gin.Context) {
	items, err := a.Store.ListTelemetry(c.Request.Context(), c.Query("asset"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) ListTelemetryHistory(c *gin.Context) {
	since := time.Now().UTC().Add(-24 * time.Hour)
	if v := c.Query("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	items, err := a.Store.ListTelemetryHistory(c.Request.Context(), c.Query("asset"), c.Query("metric"), since, 500)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) UpsertTelemetry(c *gin.Context) {
	var body store.TelemetryPoint
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.AssetTag == "" {
		body.AssetTag = c.Param("tag")
	}
	if err := a.Store.InsertTelemetryPoint(c.Request.Context(), body); err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (a *API) IngestTelemetryBatch(c *gin.Context) {
	var body struct {
		Points []store.TelemetryPoint `json:"points" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	for _, p := range body.Points {
		if err := a.Store.InsertTelemetryPoint(ctx, p); err != nil {
			writeStoreError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "count": len(body.Points)})
}

func (a *API) ListAIRecommendations(c *gin.Context) {
	items, err := a.Store.ListAIRecommendations(c.Request.Context(), c.Query("status"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) AcceptAIRecommendation(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := a.Store.UpdateAIRecommendation(c.Request.Context(), id, "accepted"); err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "accepted"})
}

func (a *API) DismissAIRecommendation(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := a.Store.UpdateAIRecommendation(c.Request.Context(), id, "dismissed"); err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "dismissed"})
}

func (a *API) ValidateBatchRef(c *gin.Context) {
	batchID := c.Param("batch_business_id")
	if batchID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "batch_business_id required"})
		return
	}
	if a.Bridge != nil {
		if err := a.Bridge.ValidateBatch(c.Request.Context(), batchID); err != nil {
			writeStoreError(c, err)
			return
		}
	} else if err := a.Store.UpsertBatchRef(c.Request.Context(), batchID, "api"); err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"batch_business_id": batchID, "validated": true})
}

func (a *API) IntegrationStatus(c *gin.Context) {
	status := map[string]any{"enabled": a.Cfg.IntegrationsEnabled}
	if a.Bridge != nil {
		status["upstreams"] = a.Bridge.Status()
	}
	c.JSON(http.StatusOK, status)
}

func (a *API) SyncERP(c *gin.Context) {
	if a.Bridge == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "integrations disabled"})
		return
	}
	n, err := a.Bridge.SyncERPProductionOrders(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"applied": n})
}

func (a *API) ERPWebhook(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if a.Bridge == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "integrations disabled"})
		return
	}
	if err := a.Bridge.IngestERPWebhook(c.Request.Context(), body); err != nil {
		writeStoreError(c, err)
		return
	}
	n, _ := a.Store.ApplyERPSyncQueue(c.Request.Context())
	c.JSON(http.StatusAccepted, gin.H{"status": "queued", "applied": n})
}

func (a *API) WarehouseConsume(c *gin.Context) {
	if a.Bridge == nil || a.Bridge.Warehouse == nil || !a.Bridge.Warehouse.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "warehouse upstream not configured"})
		return
	}
	var req clients.ProductionConsumeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := a.Bridge.Warehouse.ProductionConsume(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

func (a *API) WarehouseOutput(c *gin.Context) {
	if a.Bridge == nil || a.Bridge.Warehouse == nil || !a.Bridge.Warehouse.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "warehouse upstream not configured"})
		return
	}
	var req clients.ProductionOutputRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := a.Bridge.Warehouse.ProductionOutput(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

func (a *API) SubmitQCSample(c *gin.Context) {
	if a.Bridge == nil || a.Bridge.QC == nil || !a.Bridge.QC.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "quality-control upstream not configured"})
		return
	}
	var body struct {
		BatchBusinessID string `json:"batch_business_id" binding:"required"`
		SampleID        string `json:"sample_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := a.Bridge.QC.SubmitSample(c.Request.Context(), body.BatchBusinessID, body.SampleID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, out)
}

func (a *API) ListIntegrationCalls(c *gin.Context) {
	items, err := a.Store.ListIntegrationCalls(c.Request.Context(), c.Query("target"), 50)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) EnergySummary(c *gin.Context) {
	plant := c.DefaultQuery("plant", "kampala")
	since := time.Now().UTC().AddDate(0, 0, -7)
	if v := c.Query("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	summary, err := a.Store.EnergySummary(c.Request.Context(), plant, since)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"plant": plant, "since": since, "kwh_by_band": summary})
}

func (a *API) RecordEnergyReading(c *gin.Context) {
	var body store.EnergyReading
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := a.Store.RecordEnergyReading(c.Request.Context(), body); err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"status": "ok"})
}
