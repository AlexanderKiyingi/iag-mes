package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// The PM edit handlers' own checks run before the store. Store is nil, so a
// check moved after the store call panics rather than passing silently.

func patchWith(t *testing.T, handler func(*API, *gin.Context), id, body string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPatch, "/x/"+id, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: id}}
	api := &API{}
	reachedStore := false
	func() {
		defer func() {
			if recover() != nil {
				reachedStore = true
			}
		}()
		handler(api, c)
	}()
	return w, reachedStore
}

const someID = "942a3319-eed3-4c3c-84c3-1814a613f3bf"

func TestPatchPMTemplateRefusesANonPositiveInterval(t *testing.T) {
	for _, body := range []string{`{"interval_days":0}`, `{"interval_days":-7}`, `{"interval_days":"0"}`} {
		w, reached := patchWith(t, (*API).PatchPMTemplate, someID, body)
		if reached || w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "interval_days") {
			t.Fatalf("%s: want 400 naming interval_days before the store, got %d %s (store reached: %v)", body, w.Code, w.Body.String(), reached)
		}
	}
}

func TestPatchPMTemplateRefusesABlankName(t *testing.T) {
	w, reached := patchWith(t, (*API).PatchPMTemplate, someID, `{"name":"  "}`)
	if reached || w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 before the store, got %d (store reached: %v)", w.Code, reached)
	}
}

func TestPatchPMTemplateAcceptsAnIntervalSentAsAString(t *testing.T) {
	// Browser forms submit numbers as strings; bindJSONCoerced folds them.
	_, reached := patchWith(t, (*API).PatchPMTemplate, someID, `{"interval_days":"45"}`)
	if !reached {
		t.Fatal("a valid interval sent as a string was refused before the store")
	}
}

func TestPatchPMTemplateRefusesAnInvalidID(t *testing.T) {
	w, reached := patchWith(t, (*API).PatchPMTemplate, "TPL-ROAST-PM", `{"name":"x"}`)
	if reached || w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for a non-uuid id, got %d (store reached: %v)", w.Code, reached)
	}
}

func TestPatchPMScheduleNeedsANextDueDate(t *testing.T) {
	w, reached := patchWith(t, (*API).PatchPMSchedule, someID, `{}`)
	if reached || w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "next_due_at") {
		t.Fatalf("want 400 naming next_due_at, got %d %s (store reached: %v)", w.Code, w.Body.String(), reached)
	}
}

func TestPatchPMScheduleAcceptsAnRFC3339Date(t *testing.T) {
	_, reached := patchWith(t, (*API).PatchPMSchedule, someID, `{"next_due_at":"2026-11-01T06:00:00+03:00"}`)
	if !reached {
		t.Fatal("a valid next_due_at was refused before the store")
	}
}
