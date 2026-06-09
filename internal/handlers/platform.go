package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/store"
)

func (a *API) Bootstrap(c *gin.Context) {
	ctx := c.Request.Context()
	plants, _ := a.Store.ListPlants(ctx)
	sections, _ := a.Store.ListSections(ctx, "")
	assets, _ := a.Store.ListAssets(ctx, store.AssetFilter{})
	workOrders, _ := a.Store.ListWorkOrders(ctx, "open", 20)
	alerts, _ := a.Store.ListAlerts(ctx, "new", 10)

	c.JSON(http.StatusOK, gin.H{
		"service":          a.Cfg.ServiceName,
		"gateway":          a.Cfg.GatewayAPIPrefix,
		"plants":           plants,
		"sections":         sections,
		"assets":           assets,
		"open_work_orders": workOrders,
		"new_alerts":       alerts,
		"production_api":   "/api/v1/production",
		"integrations":     integrationBootstrap(a),
	})
}

func integrationBootstrap(a *API) gin.H {
	out := gin.H{"enabled": a.Cfg.IntegrationsEnabled}
	if a.Bridge != nil {
		out["upstreams"] = a.Bridge.Status()
	}
	return out
}

func (a *API) PlatformStatus(c *gin.Context) {
	upstreams := map[string]bool{}
	if a.Bridge != nil {
		upstreams = a.Bridge.Status()
	}
	c.JSON(http.StatusOK, gin.H{
		"service":    a.Cfg.ServiceName,
		"audience":   a.Cfg.Audience,
		"gateway":    a.Cfg.GatewayAPIPrefix,
		"event_bus":  a.Bus != nil && a.Bus.Enabled(),
		"auth_mode":  a.Cfg.AuthMode,
		"integrations": gin.H{
			"enabled":   a.Cfg.IntegrationsEnabled,
			"upstreams": upstreams,
		},
		"kafka": gin.H{
			"production":  a.Cfg.KafkaProductionTopic,
			"operations":  a.Cfg.KafkaOperationsTopic,
			"supply_chain": a.Cfg.KafkaSupplyChainTopic,
			"quality":     a.Cfg.KafkaQualityTopic,
		},
	})
}
