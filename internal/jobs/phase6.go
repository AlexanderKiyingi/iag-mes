package jobs

import (
	"context"
	"fmt"
	"time"

	"iag-mes/backend/internal/integrations"
	"iag-mes/backend/internal/store"
)

func SyncERP(ctx context.Context, bridge *integrations.Bridge) (int, error) {
	if bridge == nil {
		return 0, nil
	}
	return bridge.SyncERPProductionOrders(ctx)
}

func GenerateAIRecommendations(ctx context.Context, st *store.Store) (int, error) {
	assets, err := st.ListAssets(ctx, store.AssetFilter{})
	if err != nil {
		return 0, err
	}
	telemetry, err := st.ListTelemetry(ctx, "")
	if err != nil {
		return 0, err
	}
	created := 0
	vibByAsset := map[string]float64{}
	for _, p := range telemetry {
		if p.Metric == "vibration_rms" {
			vibByAsset[p.AssetTag] = p.Value
		}
	}
	for _, a := range assets {
		if v, ok := vibByAsset[a.Tag]; ok && v > 4.0 {
			conf := 87.0
			tag := a.Tag
			err := st.CreateAIRecommendation(ctx, store.AIRecommendation{
				Kind:       "predictive_maintenance",
				Title:      fmt.Sprintf("%s bearing wear predicted", a.Tag),
				Body:       fmt.Sprintf("Vibration RMS %.2f mm/s exceeds threshold — schedule inspection for %s.", v, a.Name),
				Confidence: &conf,
				AssetTag:   &tag,
				Status:     "open",
			})
			if err == nil {
				created++
			}
		}
		if a.OEEPct != nil && *a.OEEPct < 70 {
			conf := 72.0
			tag := a.Tag
			_ = st.CreateAIRecommendation(ctx, store.AIRecommendation{
				Kind:       "performance",
				Title:      fmt.Sprintf("Low OEE on %s", a.Tag),
				Body:       fmt.Sprintf("OEE at %.1f%% — review downtime and speed losses.", *a.OEEPct),
				Confidence: &conf,
				AssetTag:   &tag,
				Status:     "open",
			})
			created++
		}
	}
	schedules, _ := st.ListPMSchedules(ctx, "")
	now := time.Now().UTC()
	for _, sch := range schedules {
		if sch.NextDueAt.Before(now) {
			conf := 90.0
			tag := sch.AssetTag
			_ = st.CreateAIRecommendation(ctx, store.AIRecommendation{
				Kind:       "maintenance",
				Title:      fmt.Sprintf("PM overdue for %s", sch.AssetTag),
				Body:       fmt.Sprintf("Preventive maintenance was due %s — create or complete work order.", sch.NextDueAt.Format(time.RFC3339)),
				Confidence: &conf,
				AssetTag:   &tag,
				Status:     "open",
			})
			created++
		}
	}
	return created, nil
}

func GenerateEnergyInsights(ctx context.Context, st *store.Store, plantCode string) (int, error) {
	since := time.Now().UTC().AddDate(0, 0, -1)
	summary, err := st.EnergySummary(ctx, plantCode, since)
	if err != nil {
		return 0, err
	}
	peak := summary["peak"]
	total := summary["total"]
	if total <= 0 {
		return 0, nil
	}
	if peak/total > 0.35 {
		conf := 78.0
		err := st.CreateAIRecommendation(ctx, store.AIRecommendation{
			Kind:  "energy",
			Title: "Shift load off peak tariff window",
			Body: fmt.Sprintf(
				"%.0f%% of energy in the last 24h was during peak tariff (%.0f kWh of %.0f kWh). Defer roasting/grinding to off-peak where capacity allows.",
				peak/total*100, peak, total),
			Confidence: &conf,
			Status:     "open",
		})
		if err == nil {
			return 1, nil
		}
	}
	return 0, nil
}
