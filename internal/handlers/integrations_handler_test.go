package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/clients"
	"iag-mes/backend/internal/config"
	"iag-mes/backend/internal/integrations"
)

func TestIntegrationStatus_disabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api := &API{Cfg: &config.Config{IntegrationsEnabled: false}}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/integrations/status", nil)

	api.IntegrationStatus(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["enabled"] != false {
		t.Fatalf("enabled = %v, want false", body["enabled"])
	}
}

func TestIntegrationStatus_withBridge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bridge := &integrations.Bridge{
		Warehouse: clients.NewWarehouse("http://warehouse:4005", "", "", ""),
		QC:        clients.NewQualityControl("http://qc:4004", "", "", ""),
	}
	api := &API{
		Cfg:    &config.Config{IntegrationsEnabled: true},
		Bridge: bridge,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/integrations/status", nil)

	api.IntegrationStatus(c)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	upstreams, ok := body["upstreams"].(map[string]any)
	if !ok {
		t.Fatalf("upstreams = %#v", body["upstreams"])
	}
	if upstreams["warehouse"] != true || upstreams["qc"] != true {
		t.Fatalf("upstreams = %#v", upstreams)
	}
}

func TestIntegrationBootstrap(t *testing.T) {
	api := &API{
		Cfg: &config.Config{IntegrationsEnabled: true},
		Bridge: &integrations.Bridge{
			SCM: clients.NewSCM("http://scm:4007", "", "", ""),
		},
	}
	out := integrationBootstrap(api)
	if out["enabled"] != true {
		t.Fatalf("enabled = %v", out["enabled"])
	}
	upstreams, ok := out["upstreams"].(map[string]bool)
	if !ok || !upstreams["scm"] {
		t.Fatalf("upstreams = %#v", out["upstreams"])
	}
}
