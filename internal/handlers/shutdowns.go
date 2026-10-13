package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	appmw "iag-mes/backend/internal/middleware"
	"iag-mes/backend/internal/store"
)

// Shutdowns: internal/store/shutdown.go.

func (a *API) ListShutdowns(c *gin.Context) {
	items, err := a.Store.ListShutdowns(c.Request.Context(), c.Query("plant"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) GetShutdown(c *gin.Context) {
	item, err := a.Store.GetShutdown(c.Request.Context(), c.Param("code"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) CreateShutdown(c *gin.Context) {
	var in store.ShutdownInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.CreateShutdown(c.Request.Context(), in, appmw.ActorName(c))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) PatchShutdown(c *gin.Context) {
	var patch store.ShutdownPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.UpdateShutdown(c.Request.Context(), c.Param("code"), patch)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) SetShutdownJobs(c *gin.Context) {
	var in store.ShutdownJobs
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "send {add: [work order numbers], remove: [...], schedules: [schedule ids]}"})
		return
	}
	item, err := a.Store.SetShutdownJobs(c.Request.Context(), c.Param("code"), in)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) AgreeShutdown(c *gin.Context) {
	var sig store.PaperSignature
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&sig); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	item, err := a.Store.AgreeShutdown(c.Request.Context(), c.Param("code"), appmw.ActorName(c), sig)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// shutdownStep runs one of start, finish or cancel on the shutdown named in
// the path.
func shutdownStep(step func(ctx context.Context, code, actor string) (*store.ShutdownDetail, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, err := step(c.Request.Context(), c.Param("code"), appmw.ActorName(c))
		if err != nil {
			writeStoreError(c, err)
			return
		}
		c.JSON(http.StatusOK, item)
	}
}
