package jobs

import (
	"context"
	"fmt"
	"time"

	"iag-mes/backend/internal/store"
)

// pmSyncLockKey is the advisory-lock key for the preventive-maintenance sync.
// Arbitrary but fixed; "MESPMSYN" as bytes.
const pmSyncLockKey int64 = 0x4d4553504d53594e

// SyncPreventiveMaintenance marks overdue preventive maintenance schedules and
// auto-generates work orders.
//
// It holds an advisory lock while it runs. The API server runs it on a loop,
// mes-jobs may too, and so can the admin route; the open-work-order check and
// the insert below are not atomic, so two concurrent runs could each raise a
// work order for the same schedule. A run that finds the lock taken does
// nothing — the holder is already doing the same work.
func SyncPreventiveMaintenance(ctx context.Context, st *store.Store) (created int, overdue int, err error) {
	_, err = st.WithAdvisoryLock(ctx, pmSyncLockKey, func() error {
		var e error
		created, overdue, e = syncPreventiveMaintenance(ctx, st)
		return e
	})
	return created, overdue, err
}

func syncPreventiveMaintenance(ctx context.Context, st *store.Store) (created int, overdue int, err error) {
	overdue, err = st.SyncPMScheduleStatuses(ctx)
	if err != nil {
		return 0, 0, err
	}
	schedules, err := st.ListPMSchedules(ctx, "")
	if err != nil {
		return 0, overdue, err
	}
	now := time.Now().UTC()
	for _, sch := range schedules {
		if sch.NextDueAt.After(now.Add(24 * time.Hour)) && sch.Status != "overdue" {
			continue
		}
		open, err := st.HasOpenWorkOrderForPMSchedule(ctx, sch.ID)
		if err != nil || open {
			continue
		}
		tpl, err := st.GetPMTemplate(ctx, sch.TemplateID)
		if err != nil {
			continue
		}
		if _, err := st.CreateWorkOrderFromPM(ctx, sch, *tpl); err != nil {
			continue
		}
		created++
		if sch.Status == "overdue" {
			msg := fmt.Sprintf("Preventive maintenance overdue for %s — work order auto-generated", sch.AssetTag)
			_, _ = st.CreateAlert(ctx, store.Alert{
				Severity:   "warn",
				Source:     sch.AssetTag,
				Message:    msg,
				OccurredAt: now,
			})
			st.Notify(ctx, store.Notice{Audience: store.AudiencePMOverdue, Title: "PM overdue on " + sch.AssetTag, Message: msg, Key: sch.AssetTag})
		}
	}
	// Running hours: a template with interval_hours is due by use as well as
	// by date, whichever comes first. A work order is raised at 90 % of the
	// interval — the date check's 24 h lead, in hours — and the schedule is
	// flagged overdue once the interval has run out.
	meters, err := st.ListMeterStatus(ctx)
	if err != nil {
		return created, overdue, err
	}
	byID := map[string]store.PMSchedule{}
	for _, sch := range schedules {
		byID[sch.ID.String()] = sch
	}
	for _, m := range meters {
		if m.State != "due" && m.State != "due_soon" {
			continue
		}
		sch, ok := byID[m.ScheduleID.String()]
		if !ok {
			continue
		}
		if m.State == "due" && sch.Status == "scheduled" {
			if err := st.MarkScheduleOverdue(ctx, sch.ID); err == nil {
				overdue++
				_, _ = st.CreateAlert(ctx, store.Alert{
					Severity:   "warn",
					Source:     sch.AssetTag,
					Message:    fmt.Sprintf("Preventive maintenance due by running hours for %s (%.0f of %.0f h)", sch.AssetTag, deref(m.UsedHours), m.IntervalHours),
					OccurredAt: now,
				})
				st.Notify(ctx, store.Notice{
					Audience: store.AudiencePMOverdue,
					Title:    "PM due by running hours on " + sch.AssetTag,
					Message:  fmt.Sprintf("%.0f of %.0f h run since the last service.", deref(m.UsedHours), m.IntervalHours),
					Key:      sch.AssetTag,
				})
			}
		}
		open, err := st.HasOpenWorkOrderForPMSchedule(ctx, sch.ID)
		if err != nil || open {
			continue
		}
		tpl, err := st.GetPMTemplate(ctx, sch.TemplateID)
		if err != nil {
			continue
		}
		if _, err := st.CreateWorkOrderFromPM(ctx, sch, *tpl); err != nil {
			continue
		}
		created++
	}
	return created, overdue, nil
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
