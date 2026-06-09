package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-mes/backend/internal/clients"
	"iag-mes/backend/internal/integrations"
	"iag-mes/backend/internal/store"
)

func (a *API) ListProductionRuns(c *gin.Context) {
	items, err := a.Store.ListProductionRuns(c.Request.Context(), c.Query("status"), 50)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) GetProductionRun(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	item, err := a.Store.GetProductionRun(c.Request.Context(), id)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) CreateProductionRun(c *gin.Context) {
	var body store.CreateRunInput
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.BatchBusinessID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "batch_business_id is required"})
		return
	}
	if a.Bridge != nil {
		if err := a.Bridge.ValidateBatch(c.Request.Context(), body.BatchBusinessID); err != nil {
			if err == store.ErrBadInput {
				c.JSON(http.StatusBadRequest, gin.H{"error": "batch not found in supply chain"})
				return
			}
			writeStoreError(c, err)
			return
		}
	}
	item, err := a.Store.CreateProductionRun(c.Request.Context(), body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) AdvanceProductionRun(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body store.AdvanceRunInput
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.AdvanceProductionRun(c.Request.Context(), id, body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) CompleteProductionRun(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body struct {
		store.CompleteRunInput
		WarehouseOutput  *clients.ProductionOutputRequest  `json:"warehouse_output"`
		WarehouseConsume *clients.ProductionConsumeRequest `json:"warehouse_consume"`
		SubmitQCSample   bool                              `json:"submit_qc_sample"`
		SampleID         string                            `json:"sample_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.CompleteProductionRun(c.Request.Context(), id, body.CompleteRunInput)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	if a.Bridge != nil {
		a.Bridge.AfterRunComplete(c.Request.Context(), item, integrations.CompleteHooks{
			WarehouseOutput:  body.WarehouseOutput,
			WarehouseConsume: body.WarehouseConsume,
			SubmitQCSample:   body.SubmitQCSample,
			SampleID:         body.SampleID,
		})
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) ListCCPReadings(c *gin.Context) {
	runID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	items, err := a.Store.ListCCPReadings(c.Request.Context(), runID)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) CreateCCPReading(c *gin.Context) {
	runID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body store.CCPReading
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.AddCCPReading(c.Request.Context(), runID, body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

// PostProductionOrder legacy endpoint — publishes mill-stage events without persisting a run.
func (a *API) PostProductionOrder(c *gin.Context) {
	var body struct {
		BatchBusinessID string  `json:"batch_business_id" binding:"required"`
		Stage           string  `json:"stage" binding:"required"`
		Action          string  `json:"action"`
		Facility        string  `json:"facility"`
		KgIn, KgOut     float64 `json:"kg_in"`
		Moisture        float64 `json:"moisture"`
		BedID           string  `json:"bed_id"`
		GradePrelim     string  `json:"grade_prelim"`
		OccurredAt      string  `json:"occurred_at"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	eventType := store.MapStageEvent(body.Stage, body.Action)
	if eventType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown stage or action"})
		return
	}
	occurred := time.Now().UTC().Format(time.RFC3339)
	if t := strings.TrimSpace(body.OccurredAt); t != "" {
		occurred = t
	}
	data := map[string]any{
		"batch_business_id": body.BatchBusinessID,
		"facility":          body.Facility,
		"kg_in":             body.KgIn,
		"kg_out":            body.KgOut,
		"timestamp":         occurred,
	}
	if body.Moisture > 0 {
		data["moisture"] = body.Moisture
	}
	if body.BedID != "" {
		data["bed_id"] = body.BedID
	}
	if body.GradePrelim != "" {
		data["grade_prelim"] = body.GradePrelim
	}
	if a.Bus == nil || !a.Bus.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "event bus disabled"})
		return
	}
	a.Bus.Publish(c.Request.Context(), eventType, data, body.BatchBusinessID)
	c.JSON(http.StatusCreated, gin.H{"status": "published", "event_type": eventType})
}
