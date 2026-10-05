package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"iag-mes/backend/internal/store"
)

// Where a factory is (014).
//
// IAG runs more than one, and the register held only a free-text region and a
// timezone — enough to stamp a timestamp and not much else. These assert the
// handler's own checks, which run before the store, so Store stays nil: a
// validation moved after the store call panics rather than quietly passing.

func patchPlant(t *testing.T, body string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "code", Value: "mbale"}}
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/plants/mbale", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	api := &API{}
	reachedStore := false
	func() {
		defer func() {
			if recover() != nil {
				reachedStore = true // nil Store dereferenced
			}
		}()
		api.PatchPlant(c)
	}()
	return w, reachedStore
}

func TestAFactoryCanBeGivenALocation(t *testing.T) {
	_, reached := patchPlant(t, `{"address":"Plot 4 Industrial Area","city":"Mbale","district":"Mbale","gps_lat":1.0821,"gps_lng":34.1753}`)
	if !reached {
		t.Fatal("a valid location was refused before reaching the store")
	}
}

func TestEveryFieldIsOptional(t *testing.T) {
	// Moving a pin must not require restating the address.
	if _, reached := patchPlant(t, `{"gps_lat":1.0821,"gps_lng":34.1753}`); !reached {
		t.Fatal("a coordinates-only patch was refused")
	}
	if _, reached := patchPlant(t, `{}`); !reached {
		t.Fatal("an empty patch was refused")
	}
}

func TestAMalformedBodyIsA400(t *testing.T) {
	w, reached := patchPlant(t, `{"gps_lat":"not a number"}`)
	if reached {
		t.Fatal("a non-numeric coordinate reached the store")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for a malformed body, got %d", w.Code)
	}
}

// The columns the store writes, so a rename on one side cannot silently stop
// carrying a field the API still advertises.
func TestThePatchCarriesEveryLocationField(t *testing.T) {
	var p store.PlantPatch
	body := `{"name":"Mbale Mill","region":"Eastern","timezone":"Africa/Kampala","status":"active",
	          "address":"Plot 4","city":"Mbale","district":"Mbale","country":"UG",
	          "gps_lat":1.0821,"gps_lng":34.1753}`
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("the patch does not accept its own documented shape: %v", err)
	}
	for name, got := range map[string]bool{
		"name": p.Name != nil, "region": p.Region != nil, "timezone": p.Timezone != nil,
		"status": p.Status != nil, "address": p.Address != nil, "city": p.City != nil,
		"district": p.District != nil, "country": p.Country != nil,
		"gps_lat": p.GPSLat != nil, "gps_lng": p.GPSLng != nil,
	} {
		if !got {
			t.Fatalf("%s did not bind", name)
		}
	}
	if *p.GPSLat != 1.0821 || *p.GPSLng != 34.1753 {
		t.Fatalf("coordinates lost precision: %v, %v", *p.GPSLat, *p.GPSLng)
	}
}
