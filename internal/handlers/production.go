package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/events"
)

type Production struct {
	Pub *events.Publisher
}

func (h *Production) PostProductionOrder(c *gin.Context) {
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
	eventType := mapStageEvent(body.Stage, body.Action)
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
	if err := h.Pub.Publish(c.Request.Context(), eventType, data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "kafka publish failed"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"status": "published", "event_type": eventType})
}

func mapStageEvent(stage, action string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		action = "completed"
	}
	switch stage {
	case "wetmill", "wet_mill":
		if action == "started" || action == "start" {
			return "mes.wetmill.started"
		}
		return "mes.wetmill.completed"
	case "drying", "dry":
		if action == "started" || action == "start" {
			return "mes.drying.started"
		}
		return "mes.drying.completed"
	case "drymill", "dry_mill":
		return "mes.drymill.completed"
	default:
		return ""
	}
}
