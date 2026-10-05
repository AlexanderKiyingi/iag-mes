package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"iag-mes/backend/internal/store"
)

// A stop can now be agreed before it happens (013).
//
// Two things here are worth holding. The list must keep planned stops out of
// the live downtime log unless asked, because every caller written before 013
// reads that list to mean "what has actually stopped"; and a refusal has to
// arrive as a 4xx carrying its reason.
//
// Store is nil throughout: these assert the handler's own checks, which run
// before it, so a validation moved after the store call panics rather than
// silently passes.

func getDowntime(t *testing.T, query string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/downtime-events"+query, nil)

	api := &API{}
	reachedStore := false
	func() {
		defer func() {
			if recover() != nil {
				reachedStore = true // nil Store dereferenced
			}
		}()
		api.ListDowntimeEvents(c)
	}()
	return w, reachedStore
}

func TestListRejectsAnUnknownState(t *testing.T) {
	w, reachedStore := getDowntime(t, "?state=pending")
	if reachedStore {
		t.Fatal("an unknown state reached the store; it must be refused by the handler")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for an unknown state, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	// The message has to name the alternatives — "invalid" alone leaves the
	// caller guessing at a closed set they cannot see.
	for _, want := range store.DowntimeStates {
		if !contains(body["error"], want) {
			t.Fatalf("the refusal does not name %q: %q", want, body["error"])
		}
	}
}

func TestListAcceptsEveryRealState(t *testing.T) {
	for _, state := range store.DowntimeStates {
		w, reachedStore := getDowntime(t, "?state="+state)
		if !reachedStore {
			t.Fatalf("state=%s was refused with %d; it is a real state", state, w.Code)
		}
	}
	// Case and padding come from query strings people type and from form state.
	if _, reached := getDowntime(t, "?state=%20Scheduled%20"); !reached {
		t.Fatal("a padded, capitalised state should normalise rather than 400")
	}
}

func TestNoStateMeansTheLiveLog(t *testing.T) {
	// No state must not be refused: it is the default every existing caller
	// uses, and it means "the live downtime log, without planned stops".
	if _, reached := getDowntime(t, ""); !reached {
		t.Fatal("a list with no state filter was refused")
	}
}

// writeStoreError used to compare sentinels with ==, so a sentinel wrapped to
// say *why* the input was refused missed every branch and fell through to 500.
// The persist layer retries a 5xx three times and then reports a generic
// failure, so the clearer the message, the more certainly it was lost.
func TestAWrappedBadInputIsA400ThatSaysWhy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	reason := "a scheduled stop needs a planned start"
	writeStoreError(c, fmt.Errorf("%w: %s", store.ErrBadInput, reason))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for a wrapped ErrBadInput, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != reason {
		t.Fatalf("the reason did not survive: want %q, got %q", reason, body["error"])
	}
}

func TestABareBadInputStillAnswers400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	writeStoreError(c, store.ErrBadInput)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for a bare ErrBadInput, got %d", w.Code)
	}
}

func TestAWrappedNotFoundIsStill404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	writeStoreError(c, fmt.Errorf("loading the stop: %w", store.ErrNotFound))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 for a wrapped ErrNotFound, got %d", w.Code)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
