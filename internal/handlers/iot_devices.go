package handlers

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/store"
)

// Machine IoT device registry — issues API keys for edge/machine-telemetry.
//
//	GET    /api/v1/iot/devices
//	POST   /api/v1/iot/devices
//	GET    /api/v1/iot/devices/:id
//	PATCH  /api/v1/iot/devices/:id
//	POST   /api/v1/iot/devices/:id/rotate-key
//	GET    /api/v1/iot/ingestion

type createIoTDeviceBody struct {
	Serial   string `json:"serial" binding:"required"`
	Label    string `json:"label"`
	AssetTag string `json:"assetTag"`
	// Pointer so omitted issueKey defaults to true (ingest needs a key).
	// Explicit false still creates a registry row without a key.
	IssueKey *bool `json:"issueKey"`
}

type updateIoTDeviceBody struct {
	Label    *string `json:"label"`
	AssetTag *string `json:"assetTag"`
	IsActive *bool   `json:"isActive"`
}

func (a *API) ListIoTDevices(c *gin.Context) {
	rows, err := a.Store.ListIoTDevices(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (a *API) CreateIoTDevice(c *gin.Context) {
	var body createIoTDeviceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	issueKey := true
	if body.IssueKey != nil {
		issueKey = *body.IssueKey
	}
	created, err := a.Store.CreateIoTDevice(c.Request.Context(), store.CreateIoTDeviceInput{
		Serial:   body.Serial,
		Label:    body.Label,
		AssetTag: body.AssetTag,
		IssueKey: issueKey,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrIoTUnknownAsset) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (a *API) GetIoTDevice(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	d, err := a.Store.GetIoTDevice(c.Request.Context(), id)
	if errors.Is(err, store.ErrIoTDeviceNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

func (a *API) PatchIoTDevice(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body updateIoTDeviceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	d, err := a.Store.UpdateIoTDevice(c.Request.Context(), id, store.UpdateIoTDeviceInput{
		Label:    body.Label,
		AssetTag: body.AssetTag,
		IsActive: body.IsActive,
	})
	if errors.Is(err, store.ErrIoTDeviceNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

func (a *API) RotateIoTDeviceKey(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	key, err := a.Store.RotateIoTDeviceAPIKey(c.Request.Context(), id)
	if errors.Is(err, store.ErrIoTDeviceNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"apiKey": key})
}

// MachineTelemetryIngestionGuide tells operators where relays should POST.
func (a *API) MachineTelemetryIngestionGuide(c *gin.Context) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("MACHINE_TELEMETRY_INGEST_URL")), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_GATEWAY_ORIGIN")), "/")
		if base != "" {
			base += "/api/v1/production/api/iot/readings"
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"service":     "machine-telemetry",
		"httpPath":    "/api/iot/readings",
		"publicUrl":   base,
		"auth":        "Authorization: Bearer <device-api-key>",
		"batchLimit":  1000,
		"contentType": "application/json",
		"sample": gin.H{
			"assetTag":     "MCH-001",
			"ts":           "2026-06-13T08:30:00Z",
			"state":        "running",
			"goodCount":    980,
			"rejectCount":  20,
			"cycleCount":   1000,
			"temperatureC": 64.5,
		},
		"mtpTcpPort": 5037,
		"note":       "Register a device with issueKey=true, bind assetTag to a mes_assets.tag, then POST readings with the returned apiKey.",
	})
}
