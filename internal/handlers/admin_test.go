package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/config"
)

func TestAdminConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api := &API{
		Cfg: &config.Config{
			ServiceName:                "mes",
			Environment:                "development",
			AuthMode:                   "jwt",
			IntegrationsEnabled:        true,
			AutoQCOnRunComplete:        true,
			UpstreamWarehouse:          "http://warehouse:4005",
			KafkaProductionTopic:       "iag.production",
			KafkaConsumerGroup:         "iag.mes",
		},
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/config", nil)

	api.AdminConfig(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	integ, ok := body["integrations"].(map[string]any)
	if !ok || integ["enabled"] != true {
		t.Fatalf("integrations = %#v", body["integrations"])
	}
}

func TestAdminRunJob_unknown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api := &API{Cfg: &config.Config{}, Store: nil}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/jobs/invalid", nil)
	c.Params = gin.Params{{Key: "job", Value: "invalid"}}

	api.AdminRunJob(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}
