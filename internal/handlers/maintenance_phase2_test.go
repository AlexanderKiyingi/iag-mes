package handlers

import (
	"testing"

	"iag-mes/backend/internal/config"
)

// gin panics at startup on a route that conflicts with another; building the
// router is the only test that catches it before production does.
func TestRouterBuildsWithPhase2Routes(t *testing.T) {
	r := NewRouter(RouterDeps{API: &API{Cfg: &config.Config{ServiceName: "test"}}})
	want := map[string]bool{
		"POST /api/v1/fault-reports":                       false,
		"POST /api/v1/fault-reports/:id/convert":           false,
		"POST /api/v1/work-orders/:num/checklist":          false,
		"POST /api/v1/work-orders/:num/signoff/supervisor": false,
		"POST /api/v1/work-orders/:num/approve":            false,
		"GET /api/v1/failure-codes":                        false,
		"GET /api/v1/reliability/failure-causes":           false,
		"POST /api/v1/assets/:tag/meter-readings":          false,
		"GET /api/v1/pm-schedules/meter-status":            false,
	}
	for _, rt := range r.Routes() {
		if _, ok := want[rt.Method+" "+rt.Path]; ok {
			want[rt.Method+" "+rt.Path] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("route missing: %s", route)
		}
	}
}
