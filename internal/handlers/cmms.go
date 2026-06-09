package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-mes/backend/internal/store"
)

func (a *API) ListWorkOrders(c *gin.Context) {
	items, err := a.Store.ListWorkOrders(c.Request.Context(), c.Query("status"), 50)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) GetWorkOrder(c *gin.Context) {
	item, err := a.Store.GetWorkOrder(c.Request.Context(), c.Param("num"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) CreateWorkOrder(c *gin.Context) {
	var body store.WorkOrder
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Num == "" {
		num, err := a.Store.NextWorkOrderNum(c.Request.Context())
		if err != nil {
			writeStoreError(c, err)
			return
		}
		body.Num = num
	}
	item, err := a.Store.CreateWorkOrder(c.Request.Context(), body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) CompleteWorkOrder(c *gin.Context) {
	item, err := a.Store.CompleteWorkOrder(c.Request.Context(), c.Param("num"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) ListDowntimeEvents(c *gin.Context) {
	items, err := a.Store.ListDowntimeEvents(c.Request.Context(), c.Query("asset"), 50)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) CreateDowntimeEvent(c *gin.Context) {
	var body store.DowntimeEvent
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.CreateDowntimeEvent(c.Request.Context(), body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) EndDowntimeEvent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	item, err := a.Store.EndDowntimeEvent(c.Request.Context(), id)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) ListPMTemplates(c *gin.Context) {
	items, err := a.Store.ListPMTemplates(c.Request.Context())
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) ListPMSchedules(c *gin.Context) {
	items, err := a.Store.ListPMSchedules(c.Request.Context(), c.Query("asset"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) MaintenanceCalendar(c *gin.Context) {
	schedules, err := a.Store.ListPMSchedules(c.Request.Context(), c.Query("asset"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	workOrders, err := a.Store.ListWorkOrders(c.Request.Context(), "open", 100)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"pm_schedules": schedules, "open_work_orders": workOrders})
}
