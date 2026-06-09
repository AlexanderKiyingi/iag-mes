package jobs

import (
	"context"
	"fmt"
	"time"

	"iag-mes/backend/internal/store"
)

// SyncPreventiveMaintenance marks overdue preventive maintenance schedules and auto-generates work orders.
func SyncPreventiveMaintenance(ctx context.Context, st *store.Store) (created int, overdue int, err error) {
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
