package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A shutdown planned, filled with jobs, agreed, run and finished: the stops
// follow it, the jobs are tagged, and what was not done stays open.
func TestShutdownFromPlanToFinish(t *testing.T) {
	st, ctx := phase2Store(t)
	plant, tag := machine(t, st, ctx)
	sec, err := st.ListSections(ctx, plant)
	if err != nil || len(sec) == 0 {
		t.Fatalf("sections: %v", err)
	}
	tag2 := "H2-" + uuid.NewString()[:6]
	if _, err := st.CreateAsset(ctx, sec[0].ID, Asset{Tag: tag2, Name: "Huller 2", Category: "huller", Status: "running"}); err != nil {
		t.Fatalf("asset: %v", err)
	}
	_, elsewhere := machine(t, st, ctx)

	start := time.Now().UTC().AddDate(0, 0, 7).Truncate(time.Hour)
	end := start.Add(48 * time.Hour)
	if _, err := st.CreateShutdown(ctx, ShutdownInput{Plant: plant, Title: "Annual", StartsAt: start, EndsAt: end, Machines: []string{elsewhere}}, "Planner"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("a machine at another factory: %v", err)
	}
	if _, err := st.CreateShutdown(ctx, ShutdownInput{Plant: plant, Title: "Annual", StartsAt: end, EndsAt: start, Machines: []string{tag}}, "Planner"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("a window that ends before it starts: %v", err)
	}
	sd, err := st.CreateShutdown(ctx, ShutdownInput{Plant: plant, Title: "Annual shutdown", StartsAt: start, EndsAt: end,
		Lead: "Mukasa", Machines: []string{tag, tag2, tag}}, "Planner")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sd.Status != "planned" || len(sd.Machines) != 2 || len(sd.Stops) != 2 || sd.Stops[0].State != "scheduled" ||
		!sd.Stops[0].PlannedStart.Equal(start) || sd.Stops[0].Category != ShutdownCategory {
		t.Fatalf("planned: %+v", sd)
	}
	// The same machine cannot be in two overlapping shutdowns.
	if _, err := st.CreateShutdown(ctx, ShutdownInput{Plant: plant, Title: "Overlap", StartsAt: start.Add(time.Hour), EndsAt: end.Add(time.Hour), Machines: []string{tag}}, "Planner"); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlapping shutdown: %v", err)
	}
	// Planned stops are not in the live downtime log.
	live, _ := st.ListDowntimeEvents(ctx, tag, "", 50)
	for _, d := range live {
		if d.Attrs["shutdown"] == sd.Code {
			t.Fatalf("a planned stop in the live log: %+v", d)
		}
	}

	// Jobs: an existing one, and a preventive one brought forward.
	num, _ := st.NextWorkOrderNum(ctx)
	if _, err := st.CreateWorkOrder(ctx, WorkOrder{Num: num, Title: "Replace liner", AssetTag: tag, WOType: "overhaul"}); err != nil {
		t.Fatalf("wo: %v", err)
	}
	tpl, err := st.CreatePMTemplate(ctx, PMTemplate{Code: "SD-" + tag, Name: "Quarterly", IntervalDays: 90})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	sch, err := st.CreatePMSchedule(ctx, PMSchedule{TemplateID: tpl.ID, AssetTag: tag2, NextDueAt: start.AddDate(0, 0, 20)})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	other, _ := st.NextWorkOrderNum(ctx)
	if _, err := st.CreateWorkOrder(ctx, WorkOrder{Num: other, Title: "x", AssetTag: elsewhere, WOType: "corrective"}); err != nil {
		t.Fatalf("wo: %v", err)
	}
	if _, err := st.SetShutdownJobs(ctx, sd.Code, ShutdownJobs{Add: []string{other}}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("a job on a machine the shutdown does not stop: %v", err)
	}
	sd, err = st.SetShutdownJobs(ctx, sd.Code, ShutdownJobs{Add: []string{num}, Schedules: []uuid.UUID{sch.ID}})
	if err != nil {
		t.Fatalf("jobs: %v", err)
	}
	if sd.Jobs != 2 || sd.OpenJobs != 2 {
		t.Fatalf("jobs: %+v", sd.WorkOrders)
	}
	day := start.Format("2006-01-02")
	for _, wo := range sd.WorkOrders {
		if wo.Attrs["plannedStart"] != day {
			t.Fatalf("%s not planned for the first day: %v", wo.Num, wo.Attrs)
		}
	}
	// Bringing the same schedule forward again uses the job already raised.
	if sd, err = st.SetShutdownJobs(ctx, sd.Code, ShutdownJobs{Schedules: []uuid.UUID{sch.ID}}); err != nil || sd.Jobs != 2 {
		t.Fatalf("schedule twice: %d jobs, %v", sd.Jobs, err)
	}

	// Moving the window moves the stops; dropping a machine cancels its stop
	// and takes its jobs out.
	newStart, newEnd := start.Add(24*time.Hour), end.Add(24*time.Hour)
	machines := []string{tag}
	sd, err = st.UpdateShutdown(ctx, sd.Code, ShutdownPatch{StartsAt: &newStart, EndsAt: &newEnd, Machines: &machines})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	states := map[string]string{}
	for _, d := range sd.Stops {
		states[d.AssetTag] = d.State
		if d.AssetTag == tag && !d.PlannedStart.Equal(newStart) {
			t.Fatalf("stop not moved: %v", d.PlannedStart)
		}
	}
	if states[tag] != "scheduled" || states[tag2] != "cancelled" || sd.Jobs != 1 {
		t.Fatalf("after dropping %s: %v, %d jobs", tag2, states, sd.Jobs)
	}

	if sd, err = st.AgreeShutdown(ctx, sd.Code, "Clerk", PaperSignature{SignedBy: "Achan (production)"}); err != nil || sd.ProductionAgreed["by"] != "Achan (production)" {
		t.Fatalf("agree: %v %v", sd.ProductionAgreed, err)
	}
	if _, err := st.FinishShutdown(ctx, sd.Code, "Planner"); !errors.Is(err, ErrConflict) {
		t.Fatalf("finishing before starting: %v", err)
	}
	if sd, err = st.StartShutdown(ctx, sd.Code, "Mukasa"); err != nil || sd.Status != "in_progress" {
		t.Fatalf("start: %v", err)
	}
	if a, _ := st.GetAssetByTag(ctx, tag); a.Status != "down" {
		t.Fatalf("machine not down during the shutdown: %s", a.Status)
	}
	if _, err := st.UpdateShutdown(ctx, sd.Code, ShutdownPatch{Lead: &machines[0]}); !errors.Is(err, ErrConflict) {
		t.Fatalf("editing a running shutdown: %v", err)
	}
	if sd, err = st.FinishShutdown(ctx, sd.Code, "Mukasa"); err != nil || sd.Status != "completed" || sd.OpenJobs != 1 {
		t.Fatalf("finish: %+v %v", sd, err)
	}
	for _, d := range sd.Stops {
		if d.AssetTag == tag && (d.State != "closed" || d.EndedAt == nil) {
			t.Fatalf("stop not ended: %+v", d)
		}
	}
	list, err := st.ListShutdowns(ctx, plant)
	if err != nil || len(list) != 1 || list[0].Jobs != 1 || list[0].OpenJobs != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
}

func TestCancellingAShutdownReleasesItsJobs(t *testing.T) {
	st, ctx := phase2Store(t)
	plant, tag := machine(t, st, ctx)
	start := time.Now().UTC().AddDate(0, 0, 3)
	sd, err := st.CreateShutdown(ctx, ShutdownInput{Plant: plant, Title: "Belt change", StartsAt: start, EndsAt: start.Add(8 * time.Hour), Machines: []string{tag}}, "Planner")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	num, _ := st.NextWorkOrderNum(ctx)
	if _, err := st.CreateWorkOrder(ctx, WorkOrder{Num: num, Title: "Belts", AssetTag: tag, WOType: "preventive"}); err != nil {
		t.Fatalf("wo: %v", err)
	}
	if _, err := st.SetShutdownJobs(ctx, sd.Code, ShutdownJobs{Add: []string{num}}); err != nil {
		t.Fatalf("jobs: %v", err)
	}
	sd, err = st.CancelShutdown(ctx, sd.Code, "Planner")
	if err != nil || sd.Status != "cancelled" || sd.Jobs != 0 || sd.Stops[0].State != "cancelled" {
		t.Fatalf("cancel: %+v %v", sd, err)
	}
	wo, _ := st.GetWorkOrder(ctx, num)
	if wo.Status != "open" || wo.Attrs["shutdown"] != nil {
		t.Fatalf("job after cancelling: %s %v", wo.Status, wo.Attrs)
	}
	// A second shutdown can now take the machine for the same window.
	if _, err := st.CreateShutdown(ctx, ShutdownInput{Plant: plant, Title: "Again", StartsAt: start, EndsAt: start.Add(8 * time.Hour), Machines: []string{tag}}, "Planner"); err != nil {
		t.Fatalf("replanning after a cancel: %v", err)
	}
}
