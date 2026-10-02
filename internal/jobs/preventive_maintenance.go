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
		}
	}
	return created, overdue, nil
}
