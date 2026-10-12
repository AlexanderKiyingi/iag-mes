package store

import (
	"errors"
	"testing"
	"time"
)

// A clerk types in a technician's paper sheet. The signature is the
// technician's; the clerk is kept as entered_by. The four-eyes rule then
// compares the names on the paper, not the person typing.
func TestPaperSignaturesAreTheSignersNotTheClerks(t *testing.T) {
	st, ctx := phase2Store(t)
	_, tag := machine(t, st, ctx)
	num, _ := st.NextWorkOrderNum(ctx)
	if _, err := st.CreateWorkOrder(ctx, WorkOrder{Num: num, Title: "Monthly PM", AssetTag: tag, WOType: "preventive",
		Attrs: map[string]any{"checklist": []any{"Grease bearings"}}}); err != nil {
		t.Fatalf("wo: %v", err)
	}
	wo, err := st.RecordChecklist(ctx, num, []ChecklistEntry{{Index: 0, Result: "pass", By: "Okello"}}, "Clerk K")
	if err != nil {
		t.Fatalf("checklist: %v", err)
	}
	step := wo.Attrs["checklist_results"].([]any)[0].(map[string]any)
	if step["by"] != "Okello" || step["entered_by"] != "Clerk K" {
		t.Fatalf("step marked by %v entered by %v", step["by"], step["entered_by"])
	}

	if _, err := st.Signoff(ctx, num, "technician", "Clerk K", PaperSignature{SignedBy: "Okello", SignedOn: time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("a sheet dated tomorrow: %v", err)
	}
	wo, err = st.Signoff(ctx, num, "technician", "Clerk K", PaperSignature{SignedBy: "Okello", SignedOn: "2026-10-01"})
	if err != nil {
		t.Fatalf("technician: %v", err)
	}
	tech := wo.Attrs["signoff"].(map[string]any)["technician"].(map[string]any)
	if tech["by"] != "Okello" || tech["entered_by"] != "Clerk K" || tech["on_paper"] != true || tech["at"] != "2026-10-01T00:00:00Z" {
		t.Fatalf("technician signature: %v", tech)
	}
	if _, err := st.CompleteWorkOrder(ctx, num); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// The same clerk enters the supervisor's signature: allowed, because the
	// names on the paper differ. Okello checking his own work is not.
	if _, err := st.Signoff(ctx, num, "supervisor", "Clerk K", PaperSignature{SignedBy: "okello"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("technician checking own work on paper: %v", err)
	}
	if _, err := st.Signoff(ctx, num, "supervisor", "Clerk K", PaperSignature{SignedBy: "Achan"}); err != nil {
		t.Fatalf("supervisor on paper: %v", err)
	}
}
