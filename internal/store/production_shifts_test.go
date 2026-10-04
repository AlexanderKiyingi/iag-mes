package store

import (
	"context"
	"testing"
	"time"

	"os"

	"github.com/google/uuid"

	"iag-mes/backend/internal/db"
	"iag-mes/backend/internal/migrate"
)

func pshift(name, start, end, from, to string, active bool) ProductionShift {
	return ProductionShift{ID: uuid.NewString(), Name: name, StartTime: start, EndTime: end,
		Days: []int{1, 2, 3, 4, 5}, EffectiveFrom: from, EffectiveTo: to, Active: active}
}

func TestShiftsInForceOn(t *testing.T) {
	rows := []ProductionShift{
		pshift("Day", "06:00", "14:00", "2026-01-01", "", true),
		pshift("Day", "07:00", "15:00", "2026-11-01", "", true), // new hours from November
		pshift("Night", "22:00", "06:00", "2026-01-01", "", false),
		pshift("Evening", "14:00", "22:00", "2026-01-01", "2026-10-15", true),
	}
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	render := func(list []ProductionShift) []string {
		out := []string{}
		for _, sh := range list {
			out = append(out, sh.Name+" "+sh.StartTime)
		}
		return out
	}
	if got := render(ShiftsInForceOn(rows, day("2026-10-02"))); len(got) != 2 || got[0] != "Day 06:00" || got[1] != "Evening 14:00" {
		t.Errorf("2 Oct: %v", got)
	}
	// After Evening ended and Day's new hours took effect: one row, the new one.
	if got := render(ShiftsInForceOn(rows, day("2026-11-02"))); len(got) != 1 || got[0] != "Day 07:00" {
		t.Errorf("2 Nov: %v", got)
	}
}

func TestParseShiftSnapshot(t *testing.T) {
	ok := map[string]any{"plant_code": " kampala ", "published_at": "2026-10-04T08:00:00.123Z",
		"shifts": []any{map[string]any{"name": "Day", "start_time": "06:00", "end_time": "14:00", "days": []any{1, 2}, "active": true, "effective_from": "2026-01-01"}}}
	snap, err := ParseShiftSnapshot(ok)
	if err != nil || snap.PlantCode != "kampala" || len(snap.Shifts) != 1 || snap.Shifts[0].Days[1] != 2 {
		t.Fatalf("parse: %+v %v", snap, err)
	}
	for _, bad := range []map[string]any{
		{"published_at": "2026-10-04T08:00:00Z"},
		{"plant_code": "kampala", "published_at": "yesterday"},
	} {
		if _, err := ParseShiftSnapshot(bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

// Database-backed: the snapshot round trip through mes_plants.attrs.
func TestApplyProductionShifts(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	// db.NewPool creates the mes schema and sets search_path, as the service does.
	pool, err := db.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Up(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := New(pool)
	code := "pl-" + uuid.NewString()[:6]
	if _, err := st.Pool().Exec(ctx, `INSERT INTO mes_plants (code, name) VALUES ($1, 'Test plant')`, code); err != nil {
		t.Fatalf("plant: %v", err)
	}
	newer := ProductionShiftSnapshot{PlantCode: code, PublishedAt: "2026-10-04T08:00:00Z",
		Shifts: []ProductionShift{pshift("Day", "06:00", "14:00", "2026-01-01", "", true)}}
	older := ProductionShiftSnapshot{PlantCode: code, PublishedAt: "2026-10-03T08:00:00Z",
		Shifts: []ProductionShift{pshift("Old", "05:00", "13:00", "2026-01-01", "", true)}}

	// Plant codes match case-insensitively.
	newer.PlantCode = "PL" + code[2:]
	if applied, err := st.ApplyProductionShifts(ctx, newer); err != nil || !applied {
		t.Fatalf("apply: %v %v", applied, err)
	}
	// An older snapshot arriving late does not win.
	if applied, err := st.ApplyProductionShifts(ctx, older); err != nil || applied {
		t.Fatalf("older snapshot applied: %v %v", applied, err)
	}
	d := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	got, err := st.PlantShifts(ctx, code, &d, false)
	if err != nil || len(got) != 1 || got[0].Name != "Day" || len(got[0].Days) != 5 {
		t.Fatalf("plant shifts: %+v %v", got, err)
	}

	// Unknown plant: dropped, not an error (retrying cannot create it).
	if applied, err := st.ApplyProductionShifts(ctx, ProductionShiftSnapshot{PlantCode: "nowhere", PublishedAt: "2026-10-04T08:00:00Z"}); err != nil || applied {
		t.Fatalf("unknown plant: %v %v", applied, err)
	}
	if _, err := st.PlantShifts(ctx, "nowhere", nil, false); err != ErrNotFound {
		t.Fatalf("unknown plant read: %v", err)
	}
	// A plant production has published nothing for: empty, not the old seed.
	empty := "pl-" + uuid.NewString()[:6]
	_, _ = st.Pool().Exec(ctx, `INSERT INTO mes_plants (code, name) VALUES ($1, 'Empty')`, empty)
	if got, err := st.PlantShifts(ctx, empty, nil, false); err != nil || len(got) != 0 {
		t.Fatalf("empty plant: %+v %v", got, err)
	}
}
