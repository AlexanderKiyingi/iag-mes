package handlers_test

import (
	"testing"

	"iag-mes/backend/internal/store"
)

func TestMapStageEvent(t *testing.T) {
	cases := map[string]string{
		"wetmill:started":   store.MapStageEvent("wetmill", "started"),
		"wetmill:completed": store.MapStageEvent("wetmill", "completed"),
		"drying:started":    store.MapStageEvent("drying", "started"),
		"drying:completed":  store.MapStageEvent("drying", "completed"),
		"drymill:completed": store.MapStageEvent("drymill", "completed"),
		"roast:started":     store.MapStageEvent("roast", "started"),
		"roast:completed":   store.MapStageEvent("roast", "completed"),
	}
	want := map[string]string{
		"wetmill:started":   "mes.wetmill.started",
		"wetmill:completed": "mes.wetmill.completed",
		"drying:started":    "mes.drying.started",
		"drying:completed":  "mes.drying.completed",
		"drymill:completed": "mes.drymill.completed",
		"roast:started":     "mes.roast.started",
		"roast:completed":   "mes.roast.completed",
	}
	for key, got := range cases {
		if got != want[key] {
			t.Fatalf("%s: got %q want %q", key, got, want[key])
		}
	}
}
