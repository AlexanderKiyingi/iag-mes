package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	appmw "iag-mes/backend/internal/middleware"
	"iag-mes/backend/internal/store"
)

// Phase 2 of the maintenance plan; the rules live in
// internal/store/maintenance_phase2.go.

func pathUUID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad " + name})
		return uuid.Nil, false
	}
	return id, true
}

func (a *API) ListFaultReports(c *gin.Context) {
	items, err := a.Store.ListFaultReports(c.Request.Context(), c.Query("status"), queryLimit(c))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) CreateFaultReport(c *gin.Context) {
	var in store.FaultReportInput
	if err := bindJSONCoerced(c, &in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.CreateFaultReport(c.Request.Context(), in, appmw.ActorName(c))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) ConvertFaultReport(c *gin.Context) {
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var in store.FaultReportConvert
	if c.Request.ContentLength > 0 {
		if err := bindJSONCoerced(c, &in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	wo, report, err := a.Store.ConvertFaultReport(c.Request.Context(), id, in, appmw.ActorName(c))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"work_order": wo, "report": report})
}

func (a *API) RejectFaultReport(c *gin.Context) {
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	item, err := a.Store.RejectFaultReport(c.Request.Context(), id, body.Reason, appmw.ActorName(c))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) RecordChecklist(c *gin.Context) {
	var body struct {
		Results []store.ChecklistEntry `json:"results"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Results) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "send results: [{index, result: pass|fail|na, reading?, note?}]"})
		return
	}
	item, err := a.Store.RecordChecklist(c.Request.Context(), c.Param("num"), body.Results, appmw.ActorName(c))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) signoff(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, err := a.Store.Signoff(c.Request.Context(), c.Param("num"), role, appmw.ActorName(c))
		if err != nil {
			writeStoreError(c, err)
			return
		}
		c.JSON(http.StatusOK, item)
	}
}

func (a *API) ListFailureCodes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"problems":    store.FailureCatalog.Problems,
		"causes":      store.FailureCatalog.Causes,
		"remedies":    store.FailureCatalog.Remedies,
		"required_on": store.FailureCodedTypes,
	})
}

func (a *API) SetFailure(c *gin.Context) {
	var in store.FailureInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.SetFailure(c.Request.Context(), c.Param("num"), in, appmw.ActorName(c))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) FailureCauses(c *gin.Context) {
	items, err := a.Store.FailureCauses(c.Request.Context(), querySince(c, 365), c.Query("plant"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) decideApproval(approve bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Reason string `json:"reason"`
		}
		_ = c.ShouldBindJSON(&body)
		item, err := a.Store.DecideApproval(c.Request.Context(), c.Param("num"), approve, body.Reason, appmw.ActorName(c))
		if err != nil {
			writeStoreError(c, err)
			return
		}
		c.JSON(http.StatusOK, item)
	}
}

func (a *API) RecordMeterReading(c *gin.Context) {
	var in store.MeterReadingInput
	if err := bindJSONCoerced(c, &in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.RecordMeterReading(c.Request.Context(), c.Param("tag"), in)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) ListMeterStatus(c *gin.Context) {
	items, err := a.Store.ListMeterStatus(c.Request.Context())
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// RequestPurchase asks procurement to buy a spare part (procurement.requested
// on iag.operations). The answer is the ref to find the requisition by.
func (a *API) RequestPurchase(c *gin.Context) {
	var in store.PurchaseRequestInput
	if err := bindJSONCoerced(c, &in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ref, err := a.Store.RequestPurchase(c.Request.Context(), in, appmw.ActorName(c), time.Now())
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ref": ref, "origin_system": "maintenance", "status": "sent to procurement"})
}
