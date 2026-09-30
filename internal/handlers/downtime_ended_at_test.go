package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// A stoppage has to be recordable after it is over.
//
// ended_at was not in CreateDowntimeEvent's INSERT, so the only way to close an
// event was POST /:id/end, which stamps NOW(). Yesterday's 45-minute outage
// therefore became an open event whose duration, once ended, was the time since
// someone typed it in — and every client computing minutes from started_at and
// ended_at was reading a number nobody could enter. The Production app asks for
// "Minutes lost" as a required field for exactly this reason and had nowhere to
// put it.
//
// Store is nil throughout: these assert the handler's own checks, which run
// before it, so a validation moved after the store call panics rather than
// passes.

func postDowntime(t *testing.T, body string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/downtime-events", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	api := &API{}
	reachedStore := false
	func() {
		defer func() {
			if recover() != nil {
				reachedStore = true
			}
		}()
		api.CreateDowntimeEvent(c)
	}()
	return w, reachedStore
}

func TestCreateDowntimeAcceptsAnEndedEvent(t *testing.T) {
	started := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	ended := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	_, reachedStore := postDowntime(t, `{"asset_tag":"RST-1","category":"breakdown",`+
		`"reason":"belt","started_at":"`+started+`","ended_at":"`+ended+`"}`)
	if !reachedStore {
		t.Fatal("a closed historical event was refused before the store; it must be accepted")
	}
}

func TestCreateDowntimeRejectsAnEndBeforeItsStart(t *testing.T) {
	started := time.Now().UTC().Format(time.RFC3339)
	ended := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	w, reachedStore := postDowntime(t, `{"asset_tag":"RST-1","started_at":"`+started+
		`","ended_at":"`+ended+`"}`)
	if reachedStore {
		t.Fatal("a negative duration reached the store")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %s", w.Body.String())
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "before started_at") {
		t.Fatalf("the message should say which way round it is: %q", msg)
	}
}

func TestCreateDowntimeStillAcceptsAnOpenEvent(t *testing.T) {
	// The ordering check must not refuse the ordinary case: a live stoppage
	// sends no ended_at at all.
	_, reachedStore := postDowntime(t, `{"asset_tag":"RST-1","category":"breakdown","reason":"belt"}`)
	if !reachedStore {
		t.Fatal("an open event was refused; ended_at is optional")
	}
}
