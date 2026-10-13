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

// A conflict says what is in the way, as bad input says what is wrong.
func TestStoreErrorsCarryTheirReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err    error
		status int
		msg    string
	}{
		{fmt.Errorf("%w: 1 acceptance test(s) failed", store.ErrConflict), http.StatusConflict, "1 acceptance test(s) failed"},
		{store.ErrConflict, http.StatusConflict, "conflict"},
		{fmt.Errorf("%w: say which machine", store.ErrBadInput), http.StatusBadRequest, "say which machine"},
		{store.ErrNotFound, http.StatusNotFound, "not found"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		writeStoreError(c, tc.err)
		var body map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != tc.status || body["error"] != tc.msg {
			t.Fatalf("%v: %d %q", tc.err, w.Code, body["error"])
		}
	}
}
