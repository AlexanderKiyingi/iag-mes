package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alvor-technologies/iag-platform-go/authclient"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"iag-mes/backend/internal/config"
	"iag-mes/backend/internal/ctxkeys"
	appmw "iag-mes/backend/internal/middleware"
)

func withClaims(c *gin.Context, perms ...string) {
	uid := uuid.New()
	claims := &authclient.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: uid.String()},
		Permissions:      perms,
	}
	c.Set(ctxkeys.UserID, uid)
	c.Set(ctxkeys.Claims, claims)
	c.Set(ctxkeys.Permissions, perms)
}

func TestPlatformStatus_integrations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api := &API{
		Cfg: &config.Config{
			ServiceName:            "mes",
			Audience:               "iag.mes",
			AuthMode:               "jwt",
			IntegrationsEnabled:    true,
			KafkaProductionTopic:   "iag.production",
			KafkaOperationsTopic:   "iag.operations",
			KafkaSupplyChainTopic:  "iag.supply-chain",
			KafkaQualityTopic:      "iag.quality",
		},
		Bus: nil,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/platform/status", nil)

	api.PlatformStatus(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
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

func TestRequirePermission_deniesWithoutCodename(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(appmw.StrictRBAC())
	r.GET("/bootstrap", appmw.RequirePermission("mes.view_overview"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bootstrap", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated strict RBAC: status %d, want 403", w.Code)
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/bootstrap", nil)
	c, _ := gin.CreateTestContext(w2)
	c.Request = req2
	withClaims(c, "mes.view_plant")
	handler := appmw.RequirePermission("mes.view_overview")
	handler(c)
	if !c.IsAborted() {
		t.Fatal("expected abort without mes.view_overview")
	}

	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/bootstrap", nil)
	c3, _ := gin.CreateTestContext(w3)
	c3.Request = req3
	withClaims(c3, "mes.view_overview")
	handler(c3)
	if c3.IsAborted() {
		t.Fatal("expected pass with mes.view_overview")
	}
}
