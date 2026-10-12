package jobs

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"iag-mes/backend/internal/db"
	"iag-mes/backend/internal/migrate"
	"iag-mes/backend/internal/store"
)

// A schedule a year from due by date is still due by running hours.
func TestPMSyncRaisesWorkByRunningHours(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Up(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(pool)
	sfx := uuid.NewString()[:6]
	plant := "pm-" + sfx
	if _, err := st.CreatePlant(ctx, store.Plant{Code: plant, Name: plant}); err != nil {
		t.Fatalf("plant: %v", err)
	}
	sec, err := st.CreateSection(ctx, plant, store.Section{Code: "rst", Name: "Roasting", LineType: "general"})
	if err != nil {
		t.Fatalf("section: %v", err)
	}
	tag := "R-" + sfx
	if _, err := st.CreateAsset(ctx, sec.ID, store.Asset{Tag: tag, Name: "Roaster", Category: "roaster"}); err != nil {
		t.Fatalf("asset: %v", err)
	}
	tpl, err := st.CreatePMTemplate(ctx, store.PMTemplate{Code: "RH-" + sfx, Name: "500 h service", IntervalDays: 365,
		Attrs: map[string]any{"interval_hours": 500.0}})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	sch, err := st.CreatePMSchedule(ctx, store.PMSchedule{TemplateID: tpl.ID, AssetTag: tag, NextDueAt: time.Now().Add(300 * 24 * time.Hour)})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	earlier := time.Now().Add(-72 * time.Hour)
	if _, err := st.RecordMeterReading(ctx, tag, store.MeterReadingInput{Value: 2000, RecordedAt: &earlier}); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if _, err := st.RecordMeterReading(ctx, tag, store.MeterReadingInput{Value: 2510}); err != nil {
		t.Fatalf("reading: %v", err)
	}

	if _, _, err := SyncPreventiveMaintenance(ctx, st); err != nil {
		t.Fatalf("sync: %v", err)
	}
	open, err := st.HasOpenWorkOrderForPMSchedule(ctx, sch.ID)
	if err != nil || !open {
		t.Fatalf("510 of 500 h should have raised a work order: %v", err)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM mes_pm_schedules WHERE id = $1`, sch.ID).Scan(&status)
	if status != "overdue" {
		t.Fatalf("schedule status: %s", status)
	}
	var count int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM mes_work_orders WHERE pm_schedule_id = $1`, sch.ID).Scan(&count)
	if _, _, err := SyncPreventiveMaintenance(ctx, st); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	var again int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM mes_work_orders WHERE pm_schedule_id = $1`, sch.ID).Scan(&again)
	if count != 1 || again != 1 {
		t.Fatalf("one work order per due schedule: %d then %d", count, again)
	}
}
