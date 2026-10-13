package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A machine goes in: installation, then commissioning raised for it with the
// template for its category, tests against limits, a three-party handover,
// and the machine in service with its warranty running.
func TestInstallationToCommissionedMachine(t *testing.T) {
	st, ctx := phase2Store(t)
	_, tag := machine(t, st, ctx)
	category := "huller-" + uuid.NewString()[:6]
	if _, err := st.PatchAsset(ctx, tag, AssetPatch{Attrs: map[string]any{"manufacturer": "Pinhalense"}}); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if _, err := st.pool.Exec(ctx, `UPDATE mes_assets SET category = $2 WHERE tag = $1`, tag, category); err != nil {
		t.Fatalf("category: %v", err)
	}
	if _, err := st.CreatePMTemplate(ctx, PMTemplate{Code: "COM-" + category, Name: "Huller commissioning", AssetCategory: category,
		Attrs: map[string]any{"kind": "commissioning"},
		Checklist: []any{
			"Guards and emergency stop fitted",
			map[string]any{"step": "Motor current at full load", "min": 10.0, "max": 14.0, "unit": "A"},
		}}); err != nil {
		t.Fatalf("template: %v", err)
	}

	num, _ := st.NextWorkOrderNum(ctx)
	inst := WorkOrder{Num: num, Title: "Install huller", AssetTag: tag, WOType: "installation",
		Attrs: map[string]any{"contractor": "Agro Engineering Ltd", "warranty_months": 12.0}}
	if err := st.PrepareInstallationWork(ctx, &inst); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	created, err := st.CreateWorkOrder(ctx, inst)
	if err != nil {
		t.Fatalf("installation: %v", err)
	}
	st.MarkInstallationStarted(ctx, created)
	if a, _ := st.GetAssetByTag(ctx, tag); a.Attrs["serviceState"] != "installing" || a.Attrs["manufacturer"] != "Pinhalense" {
		t.Fatalf("while installing: %v", a.Attrs)
	}
	if _, err := st.CompleteWorkOrder(ctx, num); err != nil {
		t.Fatalf("complete installation: %v", err)
	}
	a, _ := st.GetAssetByTag(ctx, tag)
	if a.Attrs["serviceState"] != "commissioning" || a.Attrs["installedOn"] != time.Now().UTC().Format("2006-01-02") {
		t.Fatalf("after installation: %v", a.Attrs)
	}
	com, err := st.openWorkOrderOfType(ctx, tag, WOTypeCommissioning)
	if err != nil || com == "" {
		t.Fatalf("no commissioning job raised: %q %v", com, err)
	}
	wo, _ := st.GetWorkOrder(ctx, com)
	if len(checklistSteps(wo.Attrs)) != 2 || wo.Attrs["commissioning_template"] != "COM-"+category ||
		wo.Attrs["installation_wo"] != num || wo.Attrs["contractor"] != "Agro Engineering Ltd" {
		t.Fatalf("commissioning job: %v", wo.Attrs)
	}
	// Completing the installation twice (a PATCH after /complete) does not
	// raise a second commissioning job.
	st.afterInstallationWork(ctx, created)
	var count int
	_ = st.pool.QueryRow(ctx, `SELECT COUNT(*) FROM mes_work_orders WHERE asset_tag = $1 AND wo_type = 'commissioning'`, tag).Scan(&count)
	if count != 1 {
		t.Fatalf("%d commissioning jobs", count)
	}

	// Acceptance before the tests are run is refused.
	if _, err := st.SignHandover(ctx, com, "engineering", "Eng Mukasa", HandoverInput{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("accepting before the tests: %v", err)
	}
	// The installer may sign any time.
	if _, err := st.SignHandover(ctx, com, "installer", "Clerk K", HandoverInput{PaperSignature: PaperSignature{SignedBy: "Odongo"}, Company: "Agro Engineering Ltd"}); err != nil {
		t.Fatalf("installer: %v", err)
	}
	reading := func(v float64) *float64 { return &v }
	if _, err := st.RecordChecklist(ctx, com, []ChecklistEntry{{Index: 1, Result: "pass", Reading: reading(16)}}, "Eng Mukasa"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("16 A against 10–14 A as a pass: %v", err)
	}
	if _, err := st.RecordChecklist(ctx, com, []ChecklistEntry{
		{Index: 0, Result: "pass"},
		{Index: 1, Result: "fail", Reading: reading(16), Note: "Drawing 16 A — belt too tight"},
	}, "Eng Mukasa"); err != nil {
		t.Fatalf("tests: %v", err)
	}
	if _, err := st.SignHandover(ctx, com, "engineering", "Eng Mukasa", HandoverInput{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("accepting a failed test: %v", err)
	}
	if _, err := st.RecordChecklist(ctx, com, []ChecklistEntry{{Index: 1, Result: "pass", Reading: reading(12.5)}}, "Eng Mukasa"); err != nil {
		t.Fatalf("re-test: %v", err)
	}
	if _, err := st.SignHandover(ctx, com, "engineering", "Odongo", HandoverInput{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("the installer accepting his own installation: %v", err)
	}
	if _, err := st.SignHandover(ctx, com, "engineering", "Eng Mukasa", HandoverInput{}); err != nil {
		t.Fatalf("engineering: %v", err)
	}
	if _, err := st.SignHandover(ctx, com, "production", "Prod Achan", HandoverInput{}); err != nil {
		t.Fatalf("production: %v", err)
	}
	// A result changed after acceptance withdraws it.
	wo, err = st.RecordChecklist(ctx, com, []ChecklistEntry{{Index: 1, Result: "pass", Reading: reading(13.9)}}, "Eng Mukasa")
	if err != nil {
		t.Fatalf("re-mark: %v", err)
	}
	h := handoverOf(wo.Attrs)
	if _, ok := h["engineering"]; ok {
		t.Fatalf("engineering still signed after the result changed: %v", h)
	}
	if _, ok := h["installer"]; !ok {
		t.Fatalf("the installer's signature was dropped too: %v", h)
	}
	if _, err := st.CompleteWorkOrder(ctx, com); !errors.Is(err, ErrConflict) {
		t.Fatalf("completed without acceptance: %v", err)
	}
	if _, err := st.SignHandover(ctx, com, "engineering", "Eng Mukasa", HandoverInput{}); err != nil {
		t.Fatalf("engineering again: %v", err)
	}
	if _, err := st.SignHandover(ctx, com, "production", "Prod Achan", HandoverInput{}); err != nil {
		t.Fatalf("production again: %v", err)
	}
	if _, err := st.CompleteWorkOrder(ctx, com); err != nil {
		t.Fatalf("complete commissioning: %v", err)
	}
	a, _ = st.GetAssetByTag(ctx, tag)
	today := time.Now().UTC()
	if a.Attrs["serviceState"] != "in_service" || a.Attrs["commissionedOn"] != today.Format("2006-01-02") ||
		a.Attrs["commissioningWO"] != com || a.Attrs["warrantyUntil"] != today.AddDate(0, 12, 0).Format("2006-01-02") ||
		a.Attrs["manufacturer"] != "Pinhalense" {
		t.Fatalf("in service: %v", a.Attrs)
	}
	if _, err := st.SignHandover(ctx, com, "production", "Someone", HandoverInput{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("signing a completed handover: %v", err)
	}
}

func TestHandoverIsOnlyForCommissioning(t *testing.T) {
	st, ctx := phase2Store(t)
	_, tag := machine(t, st, ctx)
	num, _ := st.NextWorkOrderNum(ctx)
	if _, err := st.CreateWorkOrder(ctx, WorkOrder{Num: num, Title: "Grease", AssetTag: tag, WOType: "preventive"}); err != nil {
		t.Fatalf("wo: %v", err)
	}
	if _, err := st.SignHandover(ctx, num, "installer", "A", HandoverInput{}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("handover on a preventive job: %v", err)
	}
	num2, _ := st.NextWorkOrderNum(ctx)
	com := WorkOrder{Num: num2, Title: "Commission", AssetTag: tag, WOType: "commissioning",
		Attrs: map[string]any{"commissioning_template": "NO-SUCH-" + tag}}
	if err := st.PrepareInstallationWork(ctx, &com); !errors.Is(err, ErrBadInput) {
		t.Fatalf("an unknown template: %v", err)
	}
	if _, err := st.SignHandover(ctx, num, "mayor", "A", HandoverInput{}); !errors.Is(err, ErrBadInput) {
		t.Fatalf("an unknown party: %v", err)
	}
}

// The forecast lists each schedule's jobs over the range: by days, by
// running hours at the recent rate, the open job against the first, and a
// gap where hours cannot be projected.
func TestMaintenanceForecast(t *testing.T) {
	st, ctx := phase2Store(t)
	plant, tag := machine(t, st, ctx)
	_, idle := machine(t, st, ctx)
	sfx := uuid.NewString()[:6]
	monthly, err := st.CreatePMTemplate(ctx, PMTemplate{Code: "FC-M-" + sfx, Name: "Monthly", IntervalDays: 30,
		Attrs: map[string]any{"estimated_hours": 2.5, "crew_size": "2", "fixed_dates": true}})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	hours, err := st.CreatePMTemplate(ctx, PMTemplate{Code: "FC-H-" + sfx, Name: "500 h", IntervalDays: 365,
		Attrs: map[string]any{"interval_hours": 500.0}})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	now := time.Now().UTC()
	start := now.AddDate(0, 0, 5)
	sch, err := st.CreatePMSchedule(ctx, PMSchedule{TemplateID: monthly.ID, AssetTag: tag, NextDueAt: start})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if _, err := st.CreatePMSchedule(ctx, PMSchedule{TemplateID: hours.ID, AssetTag: tag, NextDueAt: now.AddDate(1, 0, 0)}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if _, err := st.CreatePMSchedule(ctx, PMSchedule{TemplateID: hours.ID, AssetTag: idle, NextDueAt: now.AddDate(1, 0, 0)}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	// 20 h a day over ten days; 300 h used of 500 since the first reading.
	at := func(daysAgo int) *time.Time { v := now.AddDate(0, 0, -daysAgo); return &v }
	if _, err := st.RecordMeterReading(ctx, tag, MeterReadingInput{Value: 1000, RecordedAt: at(15)}); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if _, err := st.RecordMeterReading(ctx, tag, MeterReadingInput{Value: 1100, RecordedAt: at(10)}); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if _, err := st.RecordMeterReading(ctx, tag, MeterReadingInput{Value: 1300, RecordedAt: at(0)}); err != nil {
		t.Fatalf("reading: %v", err)
	}
	wo, err := st.CreateWorkOrderFromPM(ctx, *sch, *monthly)
	if err != nil {
		t.Fatalf("pm job: %v", err)
	}

	fc, err := st.MaintenanceForecast(ctx, now, now.AddDate(0, 0, 90), plant)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	var byDays, byHours []ForecastItem
	for _, it := range fc.Items {
		if it.AssetTag == idle {
			t.Fatalf("a machine at another factory: %+v", it)
		}
		switch it.TemplateCode {
		case monthly.Code:
			byDays = append(byDays, it)
		case hours.Code:
			byHours = append(byHours, it)
		}
	}
	if len(byDays) != 3 || byDays[0].DueOn != start.Format("2006-01-02") || byDays[0].WorkOrder != wo.Num ||
		byDays[1].WorkOrder != "" || byDays[1].DueOn != start.AddDate(0, 0, 30).Format("2006-01-02") ||
		*byDays[0].EstimatedHours != 2.5 || *byDays[0].CrewSize != 2 || !byDays[0].FixedDates || byDays[0].PlantCode != plant {
		t.Fatalf("by days: %+v", byDays)
	}
	// 300 h used since the first reading 15 days ago, at 300/15 = 20 h a day:
	// the remaining 200 h run out in 10 days, then every 25.
	if len(byHours) != 4 || !byHours[0].Estimated || byHours[0].Basis != "hours" ||
		byHours[0].DueOn != now.AddDate(0, 0, 10).Format("2006-01-02") ||
		byHours[1].DueOn != now.AddDate(0, 0, 35).Format("2006-01-02") {
		t.Fatalf("by hours: %+v", byHours)
	}

	all, err := st.MaintenanceForecast(ctx, now, now.AddDate(0, 0, 30), "")
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	found := false
	for _, g := range all.Gaps {
		if g.AssetTag == idle && g.TemplateCode == hours.Code {
			found = true
		}
	}
	if !found {
		t.Fatalf("the idle machine's hours schedule is not reported as a gap: %+v", all.Gaps)
	}
	if _, err := st.MaintenanceForecast(ctx, now, now.AddDate(0, 0, MaxForecastDays+2), ""); !errors.Is(err, ErrBadInput) {
		t.Fatalf("over the limit: %v", err)
	}
}

// A fixed-date template keeps its calendar when a job is finished late.
func TestFixedDatesSurviveALateJob(t *testing.T) {
	st, ctx := phase2Store(t)
	_, tag := machine(t, st, ctx)
	tpl, err := st.CreatePMTemplate(ctx, PMTemplate{Code: "FX-" + tag, Name: "Fixed", IntervalDays: 30,
		Attrs: map[string]any{"fixed_dates": true}})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	due := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Second)
	sch, err := st.CreatePMSchedule(ctx, PMSchedule{TemplateID: tpl.ID, AssetTag: tag, NextDueAt: due})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := st.AdvancePMScheduleAfterComplete(ctx, sch.ID); err != nil {
		t.Fatalf("advance: %v", err)
	}
	list, _ := st.ListPMSchedules(ctx, tag)
	if len(list) != 1 || !list[0].NextDueAt.Equal(due.AddDate(0, 0, 30)) {
		t.Fatalf("next due %v, want %v", list[0].NextDueAt, due.AddDate(0, 0, 30))
	}
}
