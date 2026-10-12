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

// A machine moves to another shop floor, and its factory follows.
//
// Reads derive a machine's factory through its section, and plant_id is the
// denormalised copy; a move that wrote one without the other would leave the
// machine in one factory by one query and another by the next.
func TestAMachineMovesBetweenShopFloors(t *testing.T) {
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
	st := New(pool)
	suffix := uuid.NewString()[:6]
	mustPlant := func(code string) {
		if _, err := st.CreatePlant(ctx, Plant{Code: code, Name: code}); err != nil {
			t.Fatalf("plant %s: %v", code, err)
		}
	}
	mustSection := func(plant, code string) *Section {
		sec, err := st.CreateSection(ctx, plant, Section{Code: code, Name: code, LineType: "general"})
		if err != nil {
			t.Fatalf("section %s/%s: %v", plant, code, err)
		}
		return sec
	}
	a, b := "fa-"+suffix, "fb-"+suffix
	mustPlant(a)
	mustPlant(b)
	roasting := mustSection(a, "roasting")
	packaging := mustSection(a, "packaging")
	// Same code, other factory: codes are unique per factory only.
	otherRoasting := mustSection(b, "roasting")

	tag := "MV-" + suffix
	if _, err := st.CreateAsset(ctx, roasting.ID, Asset{Tag: tag, Name: "Roaster", Category: "roaster", Status: "idle"}); err != nil {
		t.Fatalf("asset: %v", err)
	}

	// Within the factory.
	got, err := st.PatchAsset(ctx, tag, AssetPatch{SectionID: &packaging.ID})
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if got.SectionCode != "packaging" || got.PlantCode != a || got.PlantID == nil || *got.PlantID != packaging.PlantID {
		t.Fatalf("after move within %s: %+v", a, got)
	}

	// To another factory: plant_id follows the section.
	got, err = st.PatchAsset(ctx, tag, AssetPatch{SectionID: &otherRoasting.ID})
	if err != nil {
		t.Fatalf("move across: %v", err)
	}
	if got.SectionCode != "roasting" || got.PlantCode != b || got.PlantID == nil || *got.PlantID != otherRoasting.PlantID {
		t.Fatalf("after move to %s: %+v", b, got)
	}

	// A plant_id that contradicts the section is refused, not half-applied.
	if _, err := st.PatchAsset(ctx, tag, AssetPatch{SectionID: &packaging.ID, PlantID: &otherRoasting.PlantID}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("contradicting plant_id: %v", err)
	}

	// An unknown section is the caller's mistake.
	ghost := uuid.New()
	if _, err := st.PatchAsset(ctx, tag, AssetPatch{SectionID: &ghost}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("unknown section: %v", err)
	}

	// A patch without a section leaves the machine where it is.
	name := "Roaster 2"
	got, err = st.PatchAsset(ctx, tag, AssetPatch{Name: &name})
	if err != nil || got.SectionCode != "roasting" || got.PlantCode != b {
		t.Fatalf("rename moved the machine: %+v %v", got, err)
	}
}
