package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"iag-mes/backend/internal/db"
	"iag-mes/backend/internal/migrate"
)

func phase2Store(t *testing.T) (*Store, context.Context) {
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

// machine makes a factory, a shop floor and a machine on it.
func machine(t *testing.T, st *Store, ctx context.Context) (plant, tag string) {
	t.Helper()
	sfx := uuid.NewString()[:6]
	plant = "p2-" + sfx
	if _, err := st.CreatePlant(ctx, Plant{Code: plant, Name: plant}); err != nil {
		t.Fatalf("plant: %v", err)
	}
	sec, err := st.CreateSection(ctx, plant, Section{Code: "hull", Name: "Hulling", LineType: "general"})
	if err != nil {
		t.Fatalf("section: %v", err)
	}
	tag = "H-" + sfx
	if _, err := st.CreateAsset(ctx, sec.ID, Asset{Tag: tag, Name: "Huller", Category: "huller", Status: "running"}); err != nil {
		t.Fatalf("asset: %v", err)
	}
	return plant, tag
}

func TestFaultReportToCompletedBreakdown(t *testing.T) {
	st, ctx := phase2Store(t)
	_, tag := machine(t, st, ctx)

	report, err := st.CreateFaultReport(ctx, FaultReportInput{AssetTag: tag, Title: "Huller seized", MachineStopped: true}, "Operator A")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.Severity != "crit" || report.Attrs["downtime_event_id"] == nil {
		t.Fatalf("a stopped machine is critical and opens a stop: %+v", report)
	}
	asset, _ := st.GetAssetByTag(ctx, tag)
	if asset.Status != "down" {
		t.Fatalf("machine should be down, is %s", asset.Status)
	}
	listed, err := st.ListFaultReports(ctx, "new", 50)
	if err != nil || !containsAlert(listed, report.ID) {
		t.Fatalf("list: %v", err)
	}

	wo, converted, err := st.ConvertFaultReport(ctx, report.ID, FaultReportConvert{}, "Supervisor B")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if wo.WOType != "breakdown" || wo.Priority != "critical" || converted.Status != "investigating" || converted.Attrs["work_order"] != wo.Num {
		t.Fatalf("converted: %+v %+v", wo, converted)
	}
	if _, _, err := st.ConvertFaultReport(ctx, report.ID, FaultReportConvert{}, "Supervisor B"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second convert: %v", err)
	}

	// A breakdown cannot complete without its failure code.
	if _, err := st.CompleteWorkOrder(ctx, wo.Num); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "failure code") {
		t.Fatalf("uncoded completion: %v", err)
	}
	if _, err := st.SetFailure(ctx, wo.Num, FailureInput{Problem: "bearing failure", Cause: "Lack of lubrication", Remedy: "Replaced part"}, "Tech C"); err != nil {
		t.Fatalf("failure: %v", err)
	}
	if _, err := st.CompleteWorkOrder(ctx, wo.Num); err != nil {
		t.Fatalf("complete: %v", err)
	}
	done, _ := st.getFaultReport(ctx, report.ID)
	if done.Status != "resolved" {
		t.Fatalf("report after completion: %s", done.Status)
	}
	dt, _ := uuid.Parse(done.Attrs["downtime_event_id"].(string))
	var state string
	_ = st.Pool().QueryRow(ctx, `SELECT state FROM mes_downtime_events WHERE id = $1`, dt).Scan(&state)
	if state != "closed" {
		t.Fatalf("the stop should be closed, is %s", state)
	}

	counts, err := st.FailureCauses(ctx, time.Now().Add(-time.Hour), "")
	if err != nil || len(counts) == 0 {
		t.Fatalf("failure causes: %v %v", counts, err)
	}
}

func TestRejectingAReportEndsItsStop(t *testing.T) {
	st, ctx := phase2Store(t)
	_, tag := machine(t, st, ctx)
	report, err := st.CreateFaultReport(ctx, FaultReportInput{AssetTag: tag, Title: "Odd noise", MachineStopped: true}, "Operator A")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err := st.RejectFaultReport(ctx, report.ID, "", "Supervisor B"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("reject without reason: %v", err)
	}
	rejected, err := st.RejectFaultReport(ctx, report.ID, "Normal start-up noise", "Supervisor B")
	if err != nil || rejected.Status != "false_positive" {
		t.Fatalf("reject: %+v %v", rejected, err)
	}
	asset, _ := st.GetAssetByTag(ctx, tag)
	if asset.Status == "down" {
		t.Fatal("rejected report left the machine down")
	}
}

func TestChecklistRunAndSignoff(t *testing.T) {
	st, ctx := phase2Store(t)
	_, tag := machine(t, st, ctx)
	num, _ := st.NextWorkOrderNum(ctx)
	wo, err := st.CreateWorkOrder(ctx, WorkOrder{Num: num, Title: "Monthly PM", AssetTag: tag, WOType: "preventive",
		Attrs: map[string]any{"checklist": []any{"Grease bearings", "Check belt tension", "Measure vibration"}}})
	if err != nil {
		t.Fatalf("wo: %v", err)
	}
	if _, err := st.RecordChecklist(ctx, wo.Num, []ChecklistEntry{{Index: 1, Result: "fail"}}, "Tech C"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("failed step without a note: %v", err)
	}
	if _, err := st.RecordChecklist(ctx, wo.Num, []ChecklistEntry{{Index: 9, Result: "pass"}}, "Tech C"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("step out of range: %v", err)
	}
	vib := 2.1
	if _, err := st.RecordChecklist(ctx, wo.Num, []ChecklistEntry{{Index: 0, Result: "pass"}, {Index: 2, Result: "pass", Reading: &vib}}, "Tech C"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if _, err := st.CompleteWorkOrder(ctx, wo.Num); err == nil || !strings.Contains(err.Error(), "1 of 3") {
		t.Fatalf("incomplete checklist: %v", err)
	}
	// Merged by step: marking step 2 keeps steps 1 and 3.
	got, err := st.RecordChecklist(ctx, wo.Num, []ChecklistEntry{{Index: 1, Result: "fail", Note: "Belt glazed — replace next PM"}}, "Tech C")
	if err != nil || len(got.Attrs["checklist_results"].([]any)) != 3 {
		t.Fatalf("merge: %v", err)
	}
	if _, err := st.Signoff(ctx, wo.Num, "supervisor", "Supervisor B"); !errors.Is(err, ErrConflict) {
		t.Fatalf("supervisor before completion: %v", err)
	}
	if _, err := st.Signoff(ctx, wo.Num, "technician", "Tech C"); err != nil {
		t.Fatalf("technician sign-off: %v", err)
	}
	if _, err := st.CompleteWorkOrder(ctx, wo.Num); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := st.Signoff(ctx, wo.Num, "supervisor", "tech c"); !errors.Is(err, ErrConflict) {
		t.Fatalf("technician checking own work: %v", err)
	}
	if _, err := st.Signoff(ctx, wo.Num, "supervisor", "Supervisor B"); err != nil {
		t.Fatalf("supervisor: %v", err)
	}
}

func TestExpensiveWorkWaitsForAnotherPersonsApproval(t *testing.T) {
	st, ctx := phase2Store(t)
	plant, tag := machine(t, st, ctx)
	threshold := 500000.0
	if _, err := st.UpdatePlant(ctx, plant, PlantPatch{ApprovalThreshold: &threshold}); err != nil {
		t.Fatalf("threshold: %v", err)
	}
	num, _ := st.NextWorkOrderNum(ctx)
	w := WorkOrder{Num: num, Title: "Rebuild gearbox", AssetTag: tag, WOType: "overhaul",
		Attrs: map[string]any{"estimatedCost": "750,000"}}
	st.ApplyApprovalRule(ctx, &w, "Planner D")
	if approvalState(w.Attrs) != "pending" {
		t.Fatalf("over the threshold should wait: %+v", w.Attrs)
	}
	created, err := st.CreateWorkOrder(ctx, w)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.StartWorkOrder(ctx, created.Num); !errors.Is(err, ErrConflict) {
		t.Fatalf("started while pending: %v", err)
	}
	if _, err := st.DecideApproval(ctx, created.Num, true, "", "planner d"); !errors.Is(err, ErrConflict) {
		t.Fatalf("self-approval: %v", err)
	}
	if _, err := st.DecideApproval(ctx, created.Num, true, "", "Manager E"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := st.StartWorkOrder(ctx, created.Num); err != nil {
		t.Fatalf("start after approval: %v", err)
	}

	// Under the threshold: nothing to approve.
	small := WorkOrder{AssetTag: tag, Attrs: map[string]any{"estimatedCost": 100000.0}}
	st.ApplyApprovalRule(ctx, &small, "Planner D")
	if approvalState(small.Attrs) != "" {
		t.Fatalf("under the threshold should not wait: %+v", small.Attrs)
	}
}

func TestRunningHoursMeasureAPMInterval(t *testing.T) {
	st, ctx := phase2Store(t)
	_, tag := machine(t, st, ctx)
	tpl, err := st.CreatePMTemplate(ctx, PMTemplate{Code: "RH-" + tag, Name: "250 h service", IntervalDays: 365,
		Attrs: map[string]any{"interval_hours": 250.0}})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	sch, err := st.CreatePMSchedule(ctx, PMSchedule{TemplateID: tpl.ID, AssetTag: tag, NextDueAt: time.Now().Add(300 * 24 * time.Hour)})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	status := func() MeterStatus {
		t.Helper()
		all, err := st.ListMeterStatus(ctx)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		for _, m := range all {
			if m.ScheduleID == sch.ID {
				return m
			}
		}
		t.Fatal("schedule missing from meter status")
		return MeterStatus{}
	}
	if s := status(); s.State != "no_reading" {
		t.Fatalf("no readings: %+v", s)
	}
	at := func(h int) *time.Time { v := time.Now().Add(time.Duration(-h) * time.Hour); return &v }
	if _, err := st.RecordMeterReading(ctx, tag, MeterReadingInput{Value: 1000, RecordedAt: at(48)}); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if _, err := st.RecordMeterReading(ctx, tag, MeterReadingInput{Value: 1230, RecordedAt: at(1)}); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if s := status(); s.State != "due_soon" || *s.UsedHours != 230 {
		t.Fatalf("230 of 250 h: %+v", s)
	}
	if _, err := st.RecordMeterReading(ctx, tag, MeterReadingInput{Value: 900}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("a lower reading without reset: %v", err)
	}
	if _, err := st.RecordMeterReading(ctx, tag, MeterReadingInput{Value: 1260}); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if s := status(); s.State != "due" {
		t.Fatalf("260 of 250 h: %+v", s)
	}
}

func containsAlert(list []Alert, id uuid.UUID) bool {
	for _, a := range list {
		if a.ID == id {
			return true
		}
	}
	return false
}
