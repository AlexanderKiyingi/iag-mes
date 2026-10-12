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

func TestLabourRatesValidate(t *testing.T) {
	neg, huge := -1.0, float64(MaxLabourRate+1)
	bad := []PlantPatch{
		{LabourRates: map[string]*float64{"": nil}},
		{LabourRates: map[string]*float64{"Fitter": &neg}},
		{LabourRates: map[string]*float64{"Fitter": &huge}},
		{LabourCurrency: ptr("ugx")},
		{LabourCurrency: ptr("SHILLING")},
	}
	for i, in := range bad {
		if err := in.ValidateLabourRates(); !errors.Is(err, ErrBadInput) {
			t.Errorf("case %d accepted: %v", i, err)
		}
	}
	ok := 15000.0
	if err := (PlantPatch{LabourRates: map[string]*float64{"*": &ok, "Electrician": nil}, LabourCurrency: ptr("UGX")}).ValidateLabourRates(); err != nil {
		t.Fatalf("valid rates refused: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

// Rates merge per role into attrs and never disturb the rest of attrs — the
// production shift snapshot lives on the same row.
func TestLabourRatesMergeIntoAttrs(t *testing.T) {
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
	code := "lr-" + uuid.NewString()[:6]
	if _, err := st.Pool().Exec(ctx, `INSERT INTO mes_plants (code, name, attrs) VALUES ($1, 'Rates plant', '{"production_shifts":{"published_at":"x"}}')`, code); err != nil {
		t.Fatalf("plant: %v", err)
	}
	fitter, anyone := 12000.0, 9000.0
	if _, err := st.UpdatePlant(ctx, code, PlantPatch{LabourRates: map[string]*float64{"Fitter": &fitter, "*": &anyone}, LabourCurrency: ptr("UGX")}); err != nil {
		t.Fatalf("set: %v", err)
	}
	// A later patch names one role: the other stays; null removes.
	electrician := 15000.0
	p, err := st.UpdatePlant(ctx, code, PlantPatch{LabourRates: map[string]*float64{"Electrician": &electrician, "*": nil}})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	rates, _ := p.Attrs["labour_rates"].(map[string]any)
	if rates["Fitter"] != 12000.0 || rates["Electrician"] != 15000.0 || rates["*"] != nil || len(rates) != 2 {
		t.Fatalf("rates after merge: %#v", rates)
	}
	if p.Attrs["labour_currency"] != "UGX" {
		t.Fatalf("currency: %#v", p.Attrs["labour_currency"])
	}
	if _, ok := p.Attrs["production_shifts"]; !ok {
		t.Fatal("setting rates dropped the shift snapshot")
	}
	// A patch without rates leaves them alone.
	name := "Renamed"
	p, err = st.UpdatePlant(ctx, code, PlantPatch{Name: &name})
	if err != nil || p.Attrs["labour_rates"] == nil {
		t.Fatalf("rename lost the rates: %#v %v", p.Attrs, err)
	}
	// A bad rate is refused before the write.
	neg := -5.0
	if _, err := st.UpdatePlant(ctx, code, PlantPatch{LabourRates: map[string]*float64{"Fitter": &neg}}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("negative rate: %v", err)
	}
}
