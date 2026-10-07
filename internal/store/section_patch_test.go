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

// A shop floor has to be correctable.
//
// CreateSection was a plain INSERT with no edit path, so a floor typed in
// wrongly stayed wrong: the frontend reports capability honestly and drops the
// Edit control when the service has no update verb, which left a Shop Floors
// register you could add to and never fix.

func sectionTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the section-patch tests")
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

func seedPlantAndSection(t *testing.T, s *Store, ctx context.Context) (string, string) {
	t.Helper()
	plant := "pl" + uuid.NewString()[:6]
	if _, err := s.CreatePlant(ctx, Plant{Code: plant, Name: "Test plant", Timezone: "Africa/Kampala", Status: "active"}); err != nil {
		t.Fatalf("plant: %v", err)
	}
	code := "sec" + uuid.NewString()[:4]
	if _, err := s.CreateSection(ctx, plant, Section{Code: code, Name: "Wet processing", LineType: "wet"}); err != nil {
		t.Fatalf("section: %v", err)
	}
	return plant, code
}

func TestASectionCanBeRenamed(t *testing.T) {
	s, ctx := sectionTestStore(t)
	plant, code := seedPlantAndSection(t, s, ctx)

	name := "Wet processing floor"
	got, err := s.UpdateSection(ctx, plant, code, SectionPatch{Name: &name})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if got.Name != name {
		t.Errorf("name = %q, want %q", got.Name, name)
	}
	// What was not named is left alone: renaming a floor must not reset the
	// line it was set up as.
	if got.LineType != "wet" {
		t.Errorf("line_type = %q, want it untouched at %q", got.LineType, "wet")
	}
	if got.Code != code || got.PlantCode != plant {
		t.Errorf("the natural key moved: %s/%s", got.PlantCode, got.Code)
	}
}

func TestTheLineTypeMovesOnItsOwn(t *testing.T) {
	s, ctx := sectionTestStore(t)
	plant, code := seedPlantAndSection(t, s, ctx)
	lt := "roasting"
	got, err := s.UpdateSection(ctx, plant, code, SectionPatch{LineType: &lt})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if got.LineType != "roasting" || got.Name != "Wet processing" {
		t.Errorf("line=%q name=%q — the name should be untouched", got.LineType, got.Name)
	}
}

// Section codes are unique per plant only, so a patch has to be scoped by
// plant or it edits whichever factory's floor came first.
func TestTheSameCodeAtAnotherFactoryIsADifferentFloor(t *testing.T) {
	s, ctx := sectionTestStore(t)
	plantA, code := seedPlantAndSection(t, s, ctx)

	plantB := "pl" + uuid.NewString()[:6]
	if _, err := s.CreatePlant(ctx, Plant{Code: plantB, Name: "Other plant", Timezone: "Africa/Kampala", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSection(ctx, plantB, Section{Code: code, Name: "Untouched", LineType: "wet"}); err != nil {
		t.Fatal(err)
	}

	name := "Renamed at A"
	if _, err := s.UpdateSection(ctx, plantA, code, SectionPatch{Name: &name}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	other, err := s.ListSections(ctx, plantB)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 || other[0].Name != "Untouched" {
		t.Errorf("the other factory's floor was edited: %+v", other)
	}
}

func TestPatchingAFloorThatIsNotThereIs404(t *testing.T) {
	s, ctx := sectionTestStore(t)
	plant, _ := seedPlantAndSection(t, s, ctx)
	name := "nope"
	if _, err := s.UpdateSection(ctx, plant, "no-such-floor", SectionPatch{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
