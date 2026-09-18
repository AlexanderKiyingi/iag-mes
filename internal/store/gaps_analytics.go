package store

import (
	"context"
	"time"
)

type ReliabilityAsset struct {
	AssetTag        string  `json:"asset_tag"`
	MTBFHours       float64 `json:"mtbf_hours"`
	MTTRHours       float64 `json:"mttr_hours"`
	AvailabilityPct float64 `json:"availability_pct"`
	FailureCount    int     `json:"failure_count"`
	Status          string  `json:"status"`
}

type DowntimeParetoRow struct {
	Category string  `json:"category"`
	Reason   string  `json:"reason"`
	Events   int     `json:"events"`
	Minutes  float64 `json:"minutes"`
	SharePct float64 `json:"share_pct"`
}

type ShiftMetricRow struct {
	ShiftName string  `json:"shift_name"`
	Samples   int     `json:"samples"`
	AvgOutput float64 `json:"avg_output_kg"`
}

type SixBigLossRow struct {
	Category string  `json:"category"`
	Minutes  float64 `json:"minutes"`
	SharePct float64 `json:"share_pct"`
}

func (s *Store) ReliabilityByPlant(ctx context.Context, plantCode string, since time.Time) ([]ReliabilityAsset, error) {
	assets, err := s.ListAssets(ctx, AssetFilter{PlantCode: plantCode})
	if err != nil {
		return nil, err
	}
	windowHours := time.Since(since).Hours()
	if windowHours < 1 {
		windowHours = 24 * 90
	}
	var out []ReliabilityAsset
	for _, a := range assets {
		r := ReliabilityAsset{AssetTag: a.Tag, Status: a.Status}
		var failureCount int
		var downtimeMin float64
		err := s.pool.QueryRow(ctx, `
			SELECT COUNT(*)::int,
			       COALESCE(SUM(EXTRACT(EPOCH FROM (COALESCE(ended_at, NOW()) - started_at)) / 60.0), 0)
			FROM mes_downtime_events
			WHERE asset_tag = $1 AND started_at >= $2`,
			a.Tag, since).Scan(&failureCount, &downtimeMin)
		if err != nil {
			return nil, err
		}
		r.FailureCount = failureCount
		if failureCount > 0 {
			r.MTTRHours = (downtimeMin / 60.0) / float64(failureCount)
			uptime := windowHours - (downtimeMin / 60.0)
			if uptime < 0 {
				uptime = 0
			}
			r.MTBFHours = uptime / float64(failureCount)
			r.AvailabilityPct = (uptime / windowHours) * 100
		} else {
			r.MTBFHours = windowHours
			r.AvailabilityPct = 100
		}
		if r.AvailabilityPct < 75 {
			r.Status = "risk"
		} else if r.AvailabilityPct < 90 {
			r.Status = "watch"
		} else {
			r.Status = "strong"
		}
		_, _ = s.pool.Exec(ctx, `UPDATE mes_assets SET mtbf_hours=$2, updated_at=NOW() WHERE tag=$1`, a.Tag, r.MTBFHours)
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) DowntimePareto(ctx context.Context, since time.Time, limit int) ([]DowntimeParetoRow, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT category, COALESCE(NULLIF(reason,''), category) AS reason,
		       COUNT(*)::int,
		       COALESCE(SUM(EXTRACT(EPOCH FROM (COALESCE(ended_at, NOW()) - started_at)) / 60.0), 0) AS minutes
		FROM mes_downtime_events
		WHERE started_at >= $1
		GROUP BY category, COALESCE(NULLIF(reason,''), category)
		ORDER BY minutes DESC
		LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []DowntimeParetoRow
	var totalMin float64
	for rows.Next() {
		var row DowntimeParetoRow
		if err := rows.Scan(&row.Category, &row.Reason, &row.Events, &row.Minutes); err != nil {
			return nil, err
		}
		totalMin += row.Minutes
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		if totalMin > 0 {
			items[i].SharePct = items[i].Minutes / totalMin * 100
		}
	}
	return items, nil
}

func (s *Store) SixBigLosses(ctx context.Context, since time.Time) ([]SixBigLossRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			CASE
				WHEN category ILIKE '%breakdown%' THEN 'breakdown'
				WHEN category ILIKE '%changeover%' OR category ILIKE '%setup%' THEN 'changeover'
				WHEN category ILIKE '%minor%' OR category ILIKE '%idle%' THEN 'minor_stop'
				WHEN category ILIKE '%speed%' OR category ILIKE '%performance%' THEN 'reduced_speed'
				WHEN category ILIKE '%startup%' THEN 'startup_reject'
				WHEN category ILIKE '%quality%' OR category ILIKE '%reject%' THEN 'production_reject'
				ELSE 'other'
			END AS loss_bucket,
			COALESCE(SUM(EXTRACT(EPOCH FROM (COALESCE(ended_at, NOW()) - started_at)) / 60.0), 0) AS minutes
		FROM mes_downtime_events
		WHERE started_at >= $1
		GROUP BY 1
		ORDER BY minutes DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []SixBigLossRow
	var total float64
	for rows.Next() {
		var row SixBigLossRow
		if err := rows.Scan(&row.Category, &row.Minutes); err != nil {
			return nil, err
		}
		total += row.Minutes
		items = append(items, row)
	}
	for i := range items {
		if total > 0 {
			items[i].SharePct = items[i].Minutes / total * 100
		}
	}
	return items, rows.Err()
}

// ShiftAnalysis compares shifts on production's SHIFT_OUTPUT_KG snapshots,
// projected here from production.measures.rolled_up. Until 010 this read
// mes_shift_logs, a table no handler ever wrote.
func (s *Store) ShiftAnalysis(ctx context.Context, plantCode string, since time.Time) ([]ShiftMetricRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT scope_key, COUNT(*)::int, COALESCE(AVG(value), 0)::float8
		FROM mes_kpi_snapshots
		WHERE source = 'production' AND scope_type = 'shift' AND kpi_code = 'SHIFT_OUTPUT_KG'
		  AND plant_code = $1 AND recorded_at >= $2
		GROUP BY scope_key
		ORDER BY scope_key`, plantCode, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ShiftMetricRow{}
	for rows.Next() {
		var row ShiftMetricRow
		if err := rows.Scan(&row.ShiftName, &row.Samples, &row.AvgOutput); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// DailyProductionSummary reads the plant-day measures production publishes
// (PROD_* snapshots) plus MES's own downtime events. It used to query
// prod_production_runs across the schema boundary.
func (s *Store) DailyProductionSummary(ctx context.Context, plantCode string, day time.Time) (map[string]any, error) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	measures := map[string]float64{}
	rows, err := s.pool.Query(ctx, `
		SELECT kpi_code, value::float8 FROM mes_kpi_snapshots
		WHERE source = 'production' AND scope_type = 'plant' AND plant_code = $1
		  AND kpi_code LIKE 'PROD_%' AND recorded_at >= $2 - INTERVAL '12 hours' AND recorded_at < $3`,
		plantCode, start, end)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var code string
		var v float64
		if err := rows.Scan(&code, &v); err != nil {
			rows.Close()
			return nil, err
		}
		measures[code] = v
	}
	rows.Close()
	downtime, _ := s.ListDowntimeEvents(ctx, "", 100)
	dtMin := 0.0
	for _, d := range downtime {
		if d.StartedAt.Before(start) || !d.StartedAt.Before(end) {
			continue
		}
		endT := time.Now().UTC()
		if d.EndedAt != nil {
			endT = *d.EndedAt
		}
		dtMin += endT.Sub(d.StartedAt).Minutes()
	}
	return map[string]any{
		"plant":                   plantCode,
		"date":                    start.Format("2006-01-02"),
		"completed_runs":          measures["PROD_RUNS_COMPLETED"],
		"output_kg":               measures["PROD_PRODUCT_KG"],
		"input_kg":                measures["PROD_KG_IN"],
		"reject_kg":               measures["PROD_REJECT_KG"],
		"production_downtime_min": measures["PROD_DOWN_MIN"],
		"downtime_minutes":        dtMin,
		"source":                  "iag-production via production.measures.rolled_up",
	}, nil
}
