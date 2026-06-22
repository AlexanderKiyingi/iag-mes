package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/store"
)

func (a *API) ListProductionOrders(c *gin.Context) {
	items, err := a.Store.ListProductionOrders(c.Request.Context(), c.Query("status"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) CreateProductionOrder(c *gin.Context) {
	var body store.ProductionOrder
	if err := bindJSONCoerced(c, &body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.CreateProductionOrder(c.Request.Context(), body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) ListScheduleBlocks(c *gin.Context) {
	from := time.Now().UTC().AddDate(0, 0, -1)
	to := time.Now().UTC().AddDate(0, 0, 14)
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	items, err := a.Store.ListScheduleBlocks(c.Request.Context(), c.Query("asset"), from, to)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) CreateScheduleBlock(c *gin.Context) {
	var body store.ScheduleBlock
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.CreateScheduleBlock(c.Request.Context(), body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) ListShiftLogs(c *gin.Context) {
	items, err := a.Store.ListShiftLogs(c.Request.Context(), c.Query("plant"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) CreateShiftLog(c *gin.Context) {
	var body store.ShiftLog
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.CreateShiftLog(c.Request.Context(), body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) ListShiftDefinitions(c *gin.Context) {
	items, err := a.Store.GetShiftDefinition(c.Request.Context(), c.Param("code"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) ListOperators(c *gin.Context) {
	items, err := a.Store.ListOperators(c.Request.Context())
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) ListTechnicians(c *gin.Context) {
	items, err := a.Store.ListTechnicians(c.Request.Context())
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
