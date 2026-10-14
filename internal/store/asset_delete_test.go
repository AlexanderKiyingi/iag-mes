package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"

	"iag-mes/backend/internal/db"
	"iag-mes/backend/internal/events"
	"iag-mes/backend/internal/migrate"
	"iag-mes/backend/internal/outbox"
)

// assetDeletePool mirrors the other database-backed tests here: db.NewPool
// creates the mes schema and sets search_path exactly as the service does.
func assetDeletePool(t *testing.T) (*Store, context.Context) {
	t.Helper()
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
	return New(pool), ctx
}

// newMachine makes a plant, a section and a machine, and returns the tag.
func newMachine(t *testing.T, s *Store, ctx context.Context) string {
	t.Helper()
	code := "pl-" + uuid.NewString()[:6]
	var plantID, sectionID uuid.UUID
	if err := s.Pool().QueryRow(ctx,
		`INSERT INTO mes_plants (code, name) VALUES ($1,'Test plant') RETURNING id`, code).
		Scan(&plantID); err != nil {
		t.Fatalf("plant: %v", err)
	}
	if err := s.Pool().QueryRow(ctx,
		`INSERT INTO mes_sections (plant_id, code, name) VALUES ($1,'sec','Section') RETURNING id`, plantID).
		Scan(&sectionID); err != nil {
		t.Fatalf("section: %v", err)
	}
	tag := "TAG-" + uuid.NewString()[:8]
	if _, err := s.Pool().Exec(ctx,
		`INSERT INTO mes_assets (section_id, tag, name) VALUES ($1,$2,'Test machine')`, sectionID, tag); err != nil {
		t.Fatalf("asset: %v", err)
	}
	return tag
}

// A machine nothing refers to is a mistake, and a mistake can be deleted.
func TestDeleteAssetRemovesAMachineWithNoHistory(t *testing.T) {
	s, ctx := assetDeletePool(t)
	tag := newMachine(t, s, ctx)

	if err := s.DeleteAsset(ctx, tag); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetAssetByTag(ctx, tag); err == nil {
		t.Fatal("machine still readable after delete")
	}
}

// The guard that the schema does not provide.
//
// Only mes_iot_devices declares a foreign key to mes_assets; the other fourteen
// tables name a machine by a bare TEXT asset_tag, so Postgres would delete the
// machine and leave every one of those rows pointing at a tag that is gone.
func TestDeleteAssetRefusesWhenHistoryWouldBeOrphaned(t *testing.T) {
	s, ctx := assetDeletePool(t)

	for _, table := range []string{"mes_work_orders", "mes_downtime_events"} {
		tag := newMachine(t, s, ctx)
		var err error
		switch table {
		case "mes_work_orders":
			_, err = s.Pool().Exec(ctx,
				`INSERT INTO mes_work_orders (num, title, asset_tag) VALUES ($1,'WO',$2)`,
				"WO-"+uuid.NewString()[:8], tag)
		case "mes_downtime_events":
			_, err = s.Pool().Exec(ctx,
				`INSERT INTO mes_downtime_events (asset_tag, started_at, category, reason)
				 VALUES ($1, NOW(), 'mechanical', 'test')`, tag)
		}
		if err != nil {
			t.Fatalf("%s seed: %v", table, err)
		}

		err = s.DeleteAsset(ctx, tag)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("%s: want ErrConflict, got %v", table, err)
		}
		// The refusal has to say what is holding it, or the only way to find out
		// is to read the schema.
		if err != nil && !containsAll(err.Error(), tag) {
			t.Fatalf("%s: refusal does not name the machine: %v", table, err)
		}
		if _, getErr := s.GetAssetByTag(ctx, tag); getErr != nil {
			t.Fatalf("%s: machine was deleted despite the refusal: %v", table, getErr)
		}
	}
}

func TestDeleteAssetUnknownTag(t *testing.T) {
	s, ctx := assetDeletePool(t)
	if err := s.DeleteAsset(ctx, "NO-SUCH-TAG-"+uuid.NewString()[:6]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.DeleteAsset(ctx, "   "); !errors.Is(err, ErrBadInput) {
		t.Fatalf("blank tag: want ErrBadInput, got %v", err)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		found := false
		for i := 0; i+len(n) <= len(haystack); i++ {
			if haystack[i:i+len(n)] == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// A machine gets re-sited, so it has to be able to move.
//
// AssetPatch carried no section, which meant a client could show a shop-floor
// field, let someone change it, and drop the change on the floor -- the UPDATE
// simply never mentioned the column.
func TestPatchAssetMovesAMachineBetweenShopFloors(t *testing.T) {
	s, ctx := assetDeletePool(t)
	tag := newMachine(t, s, ctx)

	before, err := s.GetAssetByTag(ctx, tag)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// A second floor in the same plant.
	var dest uuid.UUID
	if err := s.Pool().QueryRow(ctx,
		`INSERT INTO mes_sections (plant_id, code, name)
		 SELECT plant_id, 'sec2', 'Second floor' FROM mes_sections WHERE id = $1
		 RETURNING id`, before.SectionID).Scan(&dest); err != nil {
		t.Fatalf("second section: %v", err)
	}

	moved, err := s.PatchAsset(ctx, tag, AssetPatch{SectionID: &dest})
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if moved.SectionID != dest {
		t.Fatalf("machine did not move: want %s, got %s", dest, moved.SectionID)
	}
	if moved.SectionCode != "sec2" {
		t.Fatalf("section code did not follow the move: %q", moved.SectionCode)
	}

	// A patch that says nothing about the section leaves it where it is.
	name := "Renamed"
	same, err := s.PatchAsset(ctx, tag, AssetPatch{Name: &name})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if same.SectionID != dest {
		t.Fatalf("rename moved the machine: %s", same.SectionID)
	}
}

// A shop floor that does not exist is the caller's mistake, not a 500.
func TestPatchAssetRejectsAnUnknownShopFloor(t *testing.T) {
	s, ctx := assetDeletePool(t)
	tag := newMachine(t, s, ctx)
	ghost := uuid.New()
	if _, err := s.PatchAsset(ctx, tag, AssetPatch{SectionID: &ghost}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("want ErrBadInput, got %v", err)
	}
}

// A shop floor with a machine on it is refused; an empty one goes.
func TestDeleteSection(t *testing.T) {
	s, ctx := assetDeletePool(t)
	tag := newMachine(t, s, ctx)

	var plant, section string
	if err := s.Pool().QueryRow(ctx, `
		SELECT p.code, sec.code FROM mes_assets a
		JOIN mes_sections sec ON sec.id = a.section_id
		JOIN mes_plants p ON p.id = sec.plant_id WHERE a.tag = $1`, tag).Scan(&plant, &section); err != nil {
		t.Fatalf("locate: %v", err)
	}

	if err := s.DeleteSection(ctx, plant, section); !errors.Is(err, ErrConflict) {
		t.Fatalf("shop floor with a machine: want ErrConflict, got %v", err)
	}
	if err := s.DeleteAsset(ctx, tag); err != nil {
		t.Fatalf("remove the machine: %v", err)
	}
	if err := s.DeleteSection(ctx, plant, section); err != nil {
		t.Fatalf("empty shop floor should delete: %v", err)
	}
	if err := s.DeleteSection(ctx, plant, section); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: want ErrNotFound, got %v", err)
	}
}

// A factory still holding a shop floor is refused; an empty one goes.
func TestDeletePlant(t *testing.T) {
	s, ctx := assetDeletePool(t)
	tag := newMachine(t, s, ctx)

	var plant, section string
	if err := s.Pool().QueryRow(ctx, `
		SELECT p.code, sec.code FROM mes_assets a
		JOIN mes_sections sec ON sec.id = a.section_id
		JOIN mes_plants p ON p.id = sec.plant_id WHERE a.tag = $1`, tag).Scan(&plant, &section); err != nil {
		t.Fatalf("locate: %v", err)
	}

	if err := s.DeletePlant(ctx, plant); !errors.Is(err, ErrConflict) {
		t.Fatalf("factory with a shop floor: want ErrConflict, got %v", err)
	}
	if err := s.DeleteAsset(ctx, tag); err != nil {
		t.Fatalf("machine: %v", err)
	}
	if err := s.DeleteSection(ctx, plant, section); err != nil {
		t.Fatalf("section: %v", err)
	}

	// A shift pattern does not block the factory: it is configuration of the
	// factory with no existence apart from it, and no endpoint lists or removes
	// one, so counting it as a reference left a factory with no shop floors and
	// no machines permanently undeletable.
	if _, err := s.Pool().Exec(ctx, `
		INSERT INTO mes_shift_definitions (plant_id, name, start_time, end_time)
		SELECT id, 'Day', '06:00', '14:00' FROM mes_plants WHERE code = $1`, plant); err != nil {
		t.Fatalf("shift definition: %v", err)
	}

	// Still refused while a loose plant_code quotes it — no foreign key says so.
	if _, err := s.Pool().Exec(ctx,
		`INSERT INTO mes_technicians (name, plant_code) VALUES ('Tech', $1)`, plant); err != nil {
		t.Fatalf("technician: %v", err)
	}
	if err := s.DeletePlant(ctx, plant); !errors.Is(err, ErrConflict) {
		t.Fatalf("factory quoted by a technician: want ErrConflict, got %v", err)
	}
	if _, err := s.Pool().Exec(ctx, `DELETE FROM mes_technicians WHERE plant_code = $1`, plant); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := s.DeletePlant(ctx, plant); err != nil {
		t.Fatalf("empty factory should delete: %v", err)
	}
	// Its roster went with it rather than being left behind.
	var orphans int
	if err := s.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM mes_shift_definitions sd
		 LEFT JOIN mes_plants p ON p.id = sd.plant_id WHERE p.id IS NULL`).Scan(&orphans); err != nil {
		t.Fatalf("orphan check: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("%d shift patterns left pointing at no factory", orphans)
	}
}

// The deletion has to be announced, or iag-production keeps a projected row
// for a machine that no longer exists. Created and updated were announced;
// deletion was not, which is the whole reason this test exists.
//
// The bus is a concrete type with no seam to fake, so this asserts on the
// outbox it enqueues into -- which is what the relay actually ships.
func TestDeleteAssetAnnouncesTheDeletion(t *testing.T) {
	s, ctx := assetDeletePool(t)
	// A broker address is required for the bus to be enabled at all; nothing
	// connects to it, because Publish enqueues into the outbox and returns
	// before it would reach Kafka.
	bus := events.New(events.Config{Enabled: true, Brokers: []string{"127.0.0.1:1"}})
	bus.SetOutbox(outbox.NewStore(s.Pool()))
	s.SetEventBus(bus)
	t.Cleanup(func() { s.SetEventBus(nil) })

	countFor := func(tag string) int {
		var n int
		if err := s.Pool().QueryRow(ctx, `
			SELECT COUNT(*)::int FROM mes_event_outbox
			WHERE event_type = $1 AND payload::text LIKE '%' || $2 || '%'`,
			events.TypeAssetDeleted, tag).Scan(&n); err != nil {
			t.Fatalf("outbox read: %v", err)
		}
		return n
	}

	tag := newMachine(t, s, ctx)
	if err := s.DeleteAsset(ctx, tag); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := countFor(tag); n != 1 {
		t.Fatalf("want one %s carrying %s, got %d", events.TypeAssetDeleted, tag, n)
	}

	// A refused delete must announce nothing: the machine is still there, and
	// a consumer acting on it would drop a row that still exists.
	kept := newMachine(t, s, ctx)
	if _, err := s.Pool().Exec(ctx,
		`INSERT INTO mes_work_orders (num, title, asset_tag) VALUES ($1,'WO',$2)`,
		"WO-"+uuid.NewString()[:8], kept); err != nil {
		t.Fatalf("work order: %v", err)
	}
	if err := s.DeleteAsset(ctx, kept); err == nil {
		t.Fatal("expected the delete to be refused")
	}
	if n := countFor(kept); n != 0 {
		t.Fatalf("a refused delete announced %d times", n)
	}
}

// Removing the records that keep a machine alive.
//
// These are history and history is normally corrected rather than removed --
// but a probe's work order is not history, and while it exists the machine it
// names cannot be deleted and the shop floor under it cannot be emptied.
func TestDeleteRecordsCreatedByMistake(t *testing.T) {
	s, ctx := assetDeletePool(t)
	tag := newMachine(t, s, ctx)

	// A work order nothing has scheduled goes.
	num := "WO-" + uuid.NewString()[:8]
	if _, err := s.Pool().Exec(ctx,
		`INSERT INTO mes_work_orders (num, title, asset_tag) VALUES ($1,'Probe',$2)`, num, tag); err != nil {
		t.Fatalf("work order: %v", err)
	}
	if err := s.DeleteWorkOrder(ctx, num); err != nil {
		t.Fatalf("delete work order: %v", err)
	}
	if err := s.DeleteWorkOrder(ctx, num); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: want ErrNotFound, got %v", err)
	}

	// One a schedule block points at is refused: that is a real foreign key.
	//
	// mes_schedule_blocks is one of the tables migration 010 drops when it is
	// empty, so whether this case can run at all depends on the deployment --
	// which is the same variance deleteRegisterRow resolves before counting.
	var hasBlocks bool
	if err := s.Pool().QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.tables
		               WHERE table_schema = current_schema() AND table_name = 'mes_schedule_blocks')`).
		Scan(&hasBlocks); err != nil {
		t.Fatalf("table check: %v", err)
	}
	booked := "WO-" + uuid.NewString()[:8]
	var woID string
	if err := s.Pool().QueryRow(ctx,
		`INSERT INTO mes_work_orders (num, title, asset_tag) VALUES ($1,'Booked',$2) RETURNING id::text`,
		booked, tag).Scan(&woID); err != nil {
		t.Fatalf("work order: %v", err)
	}
	if hasBlocks {
		if _, err := s.Pool().Exec(ctx, `
			INSERT INTO mes_schedule_blocks (asset_tag, starts_at, ends_at, work_order_id)
			VALUES ($1, NOW(), NOW() + interval '1 hour', $2)`, tag, woID); err != nil {
			t.Fatalf("schedule block: %v", err)
		}
		if err := s.DeleteWorkOrder(ctx, booked); !errors.Is(err, ErrConflict) {
			t.Fatalf("scheduled work order: want ErrConflict, got %v", err)
		}
	} else if err := s.DeleteWorkOrder(ctx, booked); err != nil {
		// No schedule table here, so nothing can be holding it.
		t.Fatalf("unscheduled work order should delete: %v", err)
	}

	// Downtime, PM schedules and energy readings are referenced by nothing.
	var dtID string
	if err := s.Pool().QueryRow(ctx, `
		INSERT INTO mes_downtime_events (asset_tag, started_at, category, reason)
		VALUES ($1, NOW(), 'mechanical', 'probe') RETURNING id::text`, tag).Scan(&dtID); err != nil {
		t.Fatalf("downtime: %v", err)
	}
	if err := s.DeleteDowntimeEvent(ctx, dtID); err != nil {
		t.Fatalf("delete downtime: %v", err)
	}

	var enID string
	if err := s.Pool().QueryRow(ctx, `
		INSERT INTO mes_energy_readings (asset_tag, plant_code, kwh, recorded_at)
		VALUES ($1, 'x', 1, NOW()) RETURNING id::text`, tag).Scan(&enID); err != nil {
		t.Skipf("energy readings shape differs here: %v", err)
	}
	if err := s.DeleteEnergyReading(ctx, enID); err != nil {
		t.Fatalf("delete energy reading: %v", err)
	}

	// A malformed id is the caller's mistake, not a database error.
	if err := s.DeleteDowntimeEvent(ctx, "not-a-uuid"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("malformed id: want ErrBadInput, got %v", err)
	}
	if err := s.DeleteDowntimeEvent(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: want ErrNotFound, got %v", err)
	}
}

// A reading can be found before it is deleted.
//
// Only the summary was exposed, which is the right shape for a dashboard and
// the wrong one for correcting a mistake: DeleteEnergyReading takes an id and
// nothing could tell you one. Three probe readings were enough to keep a
// machine, its shop floor and its factory in the register permanently.
func TestListEnergyReadingsFindsWhatDeleteNeeds(t *testing.T) {
	s, ctx := assetDeletePool(t)
	tag := newMachine(t, s, ctx)

	for i := 0; i < 3; i++ {
		if err := s.RecordEnergyReading(ctx, EnergyReading{
			PlantCode: "zz-energy", AssetTag: &tag, KWh: float64(i + 1), TariffBand: "standard",
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	mine, err := s.ListEnergyReadings(ctx, "", tag, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(mine) != 3 {
		t.Fatalf("want 3 readings for %s, got %d", tag, len(mine))
	}

	// The filter has to be a filter: another machine's readings must not appear.
	other := newMachine(t, s, ctx)
	if err := s.RecordEnergyReading(ctx, EnergyReading{
		PlantCode: "zz-energy", AssetTag: &other, KWh: 9, TariffBand: "standard",
	}); err != nil {
		t.Fatalf("record other: %v", err)
	}
	again, err := s.ListEnergyReadings(ctx, "", tag, 0)
	if err != nil {
		t.Fatalf("list again: %v", err)
	}
	if len(again) != 3 {
		t.Fatalf("filter leaked another machine's readings: %d", len(again))
	}

	// And the id it returns is the one the delete takes.
	for _, r := range again {
		if err := s.DeleteEnergyReading(ctx, r.ID.String()); err != nil {
			t.Fatalf("delete %s: %v", r.ID, err)
		}
	}
	left, err := s.ListEnergyReadings(ctx, "", tag, 0)
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("%d readings left after deleting them all", len(left))
	}

	// With its readings gone the machine can be removed, which is the point.
	if err := s.DeleteAsset(ctx, tag); err != nil {
		t.Fatalf("machine should delete once its readings are gone: %v", err)
	}
}
