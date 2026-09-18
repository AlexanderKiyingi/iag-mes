package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (a *API) ListShiftDefinitions(c *gin.Context) {
	items, err := a.Store.GetShiftDefinition(c.Request.Context(), c.Param("code"))
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
