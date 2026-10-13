package handlers

import (
	"testing"

	"iag-mes/backend/internal/config"
)

// Every verb the store can do needs a route, or it can only be reached from a
// test.
//
// This exists because DeletePlant and DeleteSection shipped without one. The
// store functions were merged, their database tests passed, and nothing could
// call them: the handlers and routes were lost in a rebase, and a store-level
// test cannot notice that. Deleting a shop floor answered `404 page not found`
// in production while the suite was green.
func TestDeleteRoutesAreRegistered(t *testing.T) {
	r := NewRouter(RouterDeps{API: &API{Cfg: &config.Config{ServiceName: "mes-test"}}})

	registered := map[string]bool{}
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	for _, want := range []string{
		"DELETE /api/v1/assets/:tag",
		"DELETE /api/v1/plants/:code",
		"DELETE /api/v1/plants/:code/sections/:section",
	} {
		if !registered[want] {
			t.Errorf("no route for %s — the store can do it but nothing can reach it", want)
		}
	}
}
