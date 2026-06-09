package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/auditlog"
	"iag-mes/backend/internal/config"
	"iag-mes/backend/internal/db"
	"iag-mes/backend/internal/events"
	"iag-mes/backend/internal/integrations"
	"iag-mes/backend/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
)

type API struct {
	Cfg    *config.Config
	Store  *store.Store
	Audit  *auditlog.Store
	Bus    *events.Bus
	Pool   *pgxpool.Pool
	Bridge *integrations.Bridge
}

func (a *API) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "service": a.Cfg.ServiceName})
}

func (a *API) Ready(c *gin.Context) {
	if err := db.Ping(c.Request.Context(), a.Pool); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "database": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready", "database": true, "event_bus": a.Bus != nil && a.Bus.Enabled()})
}

func writeStoreError(c *gin.Context, err error) {
	if err == store.ErrNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err == store.ErrConflict {
		c.JSON(http.StatusConflict, gin.H{"error": "conflict"})
		return
	}
	if err == store.ErrBadInput {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
