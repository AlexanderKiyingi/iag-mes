package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"

	"iag-mes/backend/internal/db"
	"iag-mes/backend/internal/migrate"
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
