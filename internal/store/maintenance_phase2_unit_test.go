package store

import (
	"errors"
	"strings"
	"testing"
)

func wo(woType string, attrs map[string]any) *WorkOrder {
	return &WorkOrder{Num: "WO-1", WOType: woType, Attrs: attrs}
}

func TestCompletionNeedsEveryStepAndTheSignoff(t *testing.T) {
	w := wo("preventive", map[string]any{"checklist": []any{"Grease bearings", map[string]any{"step": "Check belt"}}})
	err := CheckCompletion(w)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "2 of 2 checklist steps unmarked") || !strings.Contains(err.Error(), "sign-off") {
		t.Fatalf("unmarked: %v", err)
	}
	w.Attrs["checklist_results"] = []any{
		map[string]any{"index": 0.0, "result": "pass"},
		map[string]any{"index": 1.0, "result": "na"},
	}
	if err := CheckCompletion(w); err == nil || strings.Contains(err.Error(), "unmarked") {
		t.Fatalf("marked but unsigned: %v", err)
	}
	w.Attrs["signoff"] = map[string]any{"technician": map[string]any{"by": "Okello"}}
	if err := CheckCompletion(w); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func TestABreakdownNeedsItsFailureCode(t *testing.T) {
	w := wo("breakdown", map[string]any{})
	if err := CheckCompletion(w); err == nil || !strings.Contains(err.Error(), "failure code") {
		t.Fatalf("no code: %v", err)
	}
	w.Attrs["failure"] = map[string]any{"problem": "Bearing failure"}
	if err := CheckCompletion(w); err != nil {
		t.Fatalf("coded: %v", err)
	}
	// Preventive work with no checklist needs nothing extra.
	if err := CheckCompletion(wo("preventive", map[string]any{})); err != nil {
		t.Fatalf("plain PM: %v", err)
	}
}

func TestWorkWaitingForApprovalCannotStartOrComplete(t *testing.T) {
	for state, want := range map[string]bool{"pending": true, "rejected": true, "approved": false, "": false} {
		w := wo("corrective", map[string]any{"failure": map[string]any{}, "approval": map[string]any{"state": state}})
		if got := CheckStart(w) != nil; got != want {
			t.Errorf("start with %q blocked=%v, want %v", state, got, want)
		}
	}
}

func TestChecklistStepsReadBothShapes(t *testing.T) {
	got := checklistSteps(map[string]any{"checklist": []any{"  a ", map[string]any{"text": "b"}, map[string]any{"x": 1}, ""}})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("steps: %v", got)
	}
}

func TestFailureCatalogIsCanonicalised(t *testing.T) {
	if v, ok := canonicalIn(FailureCatalog.Problems, " bearing FAILURE "); !ok || v != "Bearing failure" {
		t.Fatalf("canonical: %q %v", v, ok)
	}
	if _, ok := canonicalIn(FailureCatalog.Causes, "gremlins"); ok {
		t.Fatal("accepted an unknown cause")
	}
}
