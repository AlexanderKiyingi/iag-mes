package jobs

import (
	"context"
	"fmt"
	"time"

	"iag-mes/backend/internal/store"
)

// RollupKPIs computes simple plant KPI snapshots from asset OEE and open work orders.
func RollupKPIs(ctx context.Context, st *store.Store, plantCode string) (int, error) {
	assets, err := st.ListAssets(ctx, store.AssetFilter{PlantCode: plantCode})
	if err != nil {
		return 0, err
	}
	if len(assets) == 0 {
		return 0, nil
	}
	var sum float64
	var count int
	for _, a := range assets {
		if a.OEEPct != nil {
			sum += *a.OEEPct
			count++
		}
	}
	if count == 0 {
		return 0, nil
	}
	avg := sum / float64(count)
	pc := plantCode
	if err := st.RecordKPISnapshot(ctx, store.KPISnapshot{
		KPICode:    "OEE-01",
		PlantCode:  &pc,
		Value:      avg,
		RecordedAt: time.Now().UTC(),
	}); err != nil {
		return 0, err
	}
	openWOs, err := st.ListWorkOrders(ctx, "open", 1000)
	if err != nil {
		return 0, err
	}
	if err := st.RecordKPISnapshot(ctx, store.KPISnapshot{
		KPICode:    "SCH-01",
		PlantCode:  &pc,
		Value:      scheduleAttainment(len(openWOs)),
		RecordedAt: time.Now().UTC(),
	}); err != nil {
		return 0, err
	}
	written := 2
	since := time.Now().UTC().AddDate(0, 0, -90)
	reliability, err := st.ReliabilityByPlant(ctx, plantCode, since)
	if err == nil && len(reliability) > 0 {
		var mtbfSum float64
		var availSum float64
		for _, r := range reliability {
			mtbfSum += r.MTBFHours
			availSum += r.AvailabilityPct
		}
		n := float64(len(reliability))
		now := time.Now().UTC()
		if err := st.RecordKPISnapshot(ctx, store.KPISnapshot{
			KPICode: "MTBF-01", PlantCode: &pc, Value: mtbfSum / n, RecordedAt: now,
		}); err == nil {
			written++
		}
		if err := st.RecordKPISnapshot(ctx, store.KPISnapshot{
			KPICode: "AVAIL-01", PlantCode: &pc, Value: availSum / n, RecordedAt: now,
		}); err == nil {
			written++
		}
	}
	losses, err := st.SixBigLosses(ctx, since)
	if err == nil {
		var totalMin float64
		for _, l := range losses {
			totalMin += l.Minutes
		}
		if err := st.RecordKPISnapshot(ctx, store.KPISnapshot{
			KPICode: "DWT-01", PlantCode: &pc, Value: totalMin, RecordedAt: time.Now().UTC(),
		}); err == nil {
			written++
		}
	}
	return written, nil
}

func scheduleAttainment(openWOs int) float64 {
	if openWOs <= 5 {
		return 95
	}
	if openWOs <= 10 {
		return 88
	}
	return 78
}

// EvaluateTelemetryAlerts creates alerts when vibration exceeds threshold.
func EvaluateTelemetryAlerts(ctx context.Context, st *store.Store) (int, error) {
	points, err := st.ListTelemetry(ctx, "")
	if err != nil {
		return 0, err
	}
	created := 0
	for _, p := range points {
		if p.Metric != "vibration_rms" || p.Value <= 4.0 {
			continue
		}
		msg := fmt.Sprintf("%s vibration RMS %.2f exceeds threshold 4.0", p.AssetTag, p.Value)
		_, err := st.CreateAlert(ctx, store.Alert{
			Severity:   "crit",
			Source:     p.AssetTag,
			Message:    msg,
			OccurredAt: time.Now().UTC(),
		})
		if err == nil {
			created++
		}
	}
	return created, nil
}
