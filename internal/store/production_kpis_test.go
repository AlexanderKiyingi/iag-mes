package store

import (
	"encoding/json"
	"testing"
)

// The event shape is production's contract (its rolledUpPayload); this
// pins the decode so a renamed field there fails here, not in production
// logs a week later.
func TestParseRolledUp(t *testing.T) {
	raw := `{
	  "plant_code": "KLA", "day": "2026-09-17", "period_start": "2026-09-16T21:00:00Z",
	  "sets": 6, "snapshot_count": 40, "crit": 1,
	  "snapshots": [
	    {"kpi_code":"OEE","scope_type":"asset","scope_key":"PULPER-01","period_start":"2026-09-16T21:00:00Z","period_end":"2026-09-17T21:00:00Z","value":58.2,"target":60,"status":"warn"},
	    {"kpi_code":"SHIFT_OUTPUT_KG","scope_type":"shift","scope_key":"3f1c…","shift_name":"Day","period_start":"2026-09-17T03:00:00Z","period_end":"2026-09-17T11:00:00Z","value":322,"status":"none"}
	  ],
	  "plant_measures": {"runs_completed": 3, "product_kg": 322, "down_min": 30},
	  "definitions": {"OEE": {"name":"OEE (availability × performance × quality)","unit":"%","category":"machine","direction":"up"}}
	}`
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	ev, err := ParseRolledUp(data)
	if err != nil {
		t.Fatal(err)
	}
	if ev.PlantCode != "KLA" || len(ev.Snapshots) != 2 || ev.PeriodStart.IsZero() {
		t.Fatalf("decoded: %+v", ev)
	}
	oee := ev.Snapshots[0]
	if oee.KPICode != "OEE" || oee.ScopeType != "asset" || oee.Target == nil || *oee.Target != 60 || oee.Status != "warn" {
		t.Errorf("oee snapshot: %+v", oee)
	}
	if ev.Snapshots[1].ShiftName != "Day" || ev.Snapshots[1].Target != nil {
		t.Errorf("shift snapshot: %+v", ev.Snapshots[1])
	}
	if ev.PlantMeasures["product_kg"] != 322 || ev.Definitions["OEE"].Unit != "%" {
		t.Errorf("measures/definitions: %+v %+v", ev.PlantMeasures, ev.Definitions)
	}
	if _, err := ParseRolledUp(map[string]any{}); err == nil {
		t.Error("empty event accepted")
	}
}

func TestPlantMeasureCodesAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for measure, meta := range plantMeasureCodes {
		if seen[meta.Name] {
			t.Errorf("code %s used twice", meta.Name)
		}
		seen[meta.Name] = true
		if meta.Name == "" || measure == "" {
			t.Errorf("empty mapping %q → %q", measure, meta.Name)
		}
	}
	for _, code := range []string{"PROD_RUNS_COMPLETED", "PROD_PRODUCT_KG", "PROD_DOWN_MIN", "PROD_KG_IN", "PROD_REJECT_KG"} {
		// DailyProductionSummary reads these by name.
		if !seen[code] {
			t.Errorf("%s not produced by the projection", code)
		}
	}
}
