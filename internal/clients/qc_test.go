package clients

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// QC verifies tokens against its own audience, "iag.quality". Asking auth for
// any other audience gets a token QC refuses, so every sample submit 401s.
func TestQualityControlRequestsQCAudience(t *testing.T) {
	var gotAudience string
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotAudience = r.PostForm.Get("audience")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"t","token_type":"Bearer","expires_in":900}`))
	}))
	defer tokens.Close()
	qc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sample_id":"S1"}`))
	}))
	defer qc.Close()

	c := NewQualityControl(qc.URL, tokens.URL, "svc", "secret")
	if _, err := c.SubmitSample(context.Background(), "BAT-1", "S1"); err != nil {
		t.Fatalf("SubmitSample: %v", err)
	}
	if gotAudience != "iag.quality" {
		t.Fatalf("audience = %q, want %q", gotAudience, "iag.quality")
	}
}
