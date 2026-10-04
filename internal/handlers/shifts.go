package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ListShiftDefinitions serves the plant's shifts as iag-production publishes
// them: those in force today (plant-local), or on ?date=YYYY-MM-DD, or every
// row with ?all=true. MES keeps no shift list of its own.
func (a *API) ListShiftDefinitions(c *gin.Context) {
	var date *time.Time
	if v := strings.TrimSpace(c.Query("date")); v != "" {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "date must be YYYY-MM-DD"})
			return
		}
		date = &d
	}
	all := strings.EqualFold(c.Query("all"), "true") || c.Query("all") == "1"
	items, err := a.Store.PlantShifts(c.Request.Context(), c.Param("code"), date, all)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "source": "iag-production"})
}

func (a *API) ListTechnicians(c *gin.Context) {
	items, err := a.Store.ListTechnicians(c.Request.Context())
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
