package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-mes/backend/internal/store"
)

// PatchPMTemplate edits a template in place. The Maintenance app's PM
// Templates screen had create and read only, so a wrong interval or a missing
// checklist step meant a second template and re-pointing every schedule.
func (a *API) PatchPMTemplate(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var patch store.PMTemplatePatch
	if err := bindJSONCoerced(c, &patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// interval_days drives next_due_at after every completion; zero or less
	// would schedule the next service at or before the one just done.
	if patch.IntervalDays != nil && *patch.IntervalDays <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "interval_days must be at least 1"})
		return
	}
	if patch.Name != nil && strings.TrimSpace(*patch.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name cannot be blank"})
		return
	}
	item, err := a.Store.PatchPMTemplate(c.Request.Context(), id, patch)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// PatchPMSchedule reschedules one machine's next service.
func (a *API) PatchPMSchedule(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var patch store.PMSchedulePatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if patch.NextDueAt == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "next_due_at is required (RFC3339)"})
		return
	}
	item, err := a.Store.PatchPMSchedule(c.Request.Context(), id, patch)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}
