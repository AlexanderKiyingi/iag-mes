package store

import (
	"errors"
	"testing"
	"time"
)

func TestTemplatePartsAreCheckedAndMerged(t *testing.T) {
	attrs := map[string]any{"parts": []any{
		map[string]any{"sku": " BRG-6205 ", "qty": 2.0},
		map[string]any{"sku": "belt-a", "qty": "1"},
		map[string]any{"sku": "brg-6205", "qty": 1.0},
	}}
	if err := NormalizeTemplateParts(attrs); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	got := TemplateParts(PMTemplate{Attrs: attrs})
	if len(got) != 2 || got[0] != (PlannedPart{SKU: "BRG-6205", Qty: 3}) || got[1] != (PlannedPart{SKU: "belt-a", Qty: 1}) {
		t.Fatalf("parts: %+v", got)
	}
	if line := partsLine(got); line != "1 × belt-a, 3 × BRG-6205" {
		t.Fatalf("line: %q", line)
	}
	for _, bad := range []any{
		"BRG-6205 x2",
		[]any{map[string]any{"sku": "", "qty": 1.0}},
		[]any{map[string]any{"sku": "X", "qty": 0.0}},
		[]any{map[string]any{"sku": "X"}},
	} {
		if err := NormalizeTemplateParts(map[string]any{"parts": bad}); !errors.Is(err, ErrBadInput) {
			t.Fatalf("%v: %v", bad, err)
		}
	}
	cleared := map[string]any{"parts": nil}
	if err := NormalizeTemplateParts(cleared); err != nil || len(cleared["parts"].([]any)) != 0 {
		t.Fatalf("clearing: %v %v", cleared, err)
	}
}

// A template's parts reach the jobs it raises and every projected job.
func TestTemplatePartsReachJobsAndTheForecast(t *testing.T) {
	st, ctx := phase2Store(t)
	plant, tag := machine(t, st, ctx)
	tpl, err := st.CreatePMTemplate(ctx, PMTemplate{Code: "PP-" + tag, Name: "Bearings", IntervalDays: 30,
		Attrs: map[string]any{"parts": []any{map[string]any{"sku": "BRG-6205", "qty": 2.0}}}})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if _, err := st.CreatePMTemplate(ctx, PMTemplate{Code: "PP-bad-" + tag, Name: "x",
		Attrs: map[string]any{"parts": []any{map[string]any{"sku": "X", "qty": -1.0}}}}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("a negative quantity: %v", err)
	}
	now := time.Now().UTC()
	sch, err := st.CreatePMSchedule(ctx, PMSchedule{TemplateID: tpl.ID, AssetTag: tag, NextDueAt: now.AddDate(0, 0, 3)})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	wo, err := st.CreateWorkOrderFromPM(ctx, *sch, *tpl)
	if err != nil {
		t.Fatalf("job: %v", err)
	}
	if wo.Attrs["partsRequired"] != "2 × BRG-6205" {
		t.Fatalf("job's parts: %v", wo.Attrs)
	}
	fc, err := st.MaintenanceForecast(ctx, now, now.AddDate(0, 0, 60), plant)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	if len(fc.Items) != 2 || len(fc.Items[1].Parts) != 1 || fc.Items[1].Parts[0] != (PlannedPart{SKU: "BRG-6205", Qty: 2}) {
		t.Fatalf("forecast parts: %+v", fc.Items)
	}
	if _, err := st.PatchPMTemplate(ctx, tpl.ID, PMTemplatePatch{Attrs: map[string]any{"parts": []any{}}}); err != nil {
		t.Fatalf("clearing parts: %v", err)
	}
}
