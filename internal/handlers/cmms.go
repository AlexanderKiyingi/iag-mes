package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-mes/backend/internal/store"
)

func (a *API) ListWorkOrders(c *gin.Context) {
	items, err := a.Store.ListWorkOrders(c.Request.Context(), c.Query("status"), queryLimit(c))
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
	// Both columns are CHECK-constrained and the INSERT passes them through
	// with only an empty-string fallback. The Production app's status set
	// differed from this one in case alone, so every option it offered was
	// refused by the column and surfaced as a 500.
	// Assigned back, not just checked: writing the caller's spelling of a
	// value we accepted case-insensitively would fail the CHECK anyway.
	status, ok := store.Canonical(store.WorkOrderStatuses, body.Status)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "status must be one of: " + store.Allowed(store.WorkOrderStatuses),
		})
		return
	}
	body.Status = status
	priority, ok := store.Canonical(store.WorkOrderPriorities, body.Priority)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "priority must be one of: " + store.Allowed(store.WorkOrderPriorities),
		})
		return
	}
	body.Priority = priority
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
	items, err := a.Store.ListDowntimeEvents(c.Request.Context(), c.Query("asset"), queryLimit(c))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) CreateDowntimeEvent(c *gin.Context) {
	var body store.DowntimeEvent
	if err := bindJSONCoerced(c, &body); err != nil {
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
