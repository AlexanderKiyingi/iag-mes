package store

import (
	"context"
	"fmt"
	"time"
)

type ReliabilityAsset struct {
	AssetTag   string  `json:"asset_tag"`
	MTBFHours  float64 `json:"mtbf_hours"`
	MTTRHours  float64 `json:"mttr_hours"`
	AvailabilityPct float64 `json:"availability_pct"`
	FailureCount int     `json:"failure_count"`
	Status     string  `json:"status"`
}

type DowntimeParetoRow struct {
	Category   string  `json:"category"`
	Reason     string  `json:"reason"`
	Events     int     `json:"events"`
	Minutes    float64 `json:"minutes"`
	SharePct   float64 `json:"share_pct"`
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

func (s *Store) ShiftAnalysis(ctx context.Context, plantCode string, since time.Time) ([]ShiftMetricRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT shift_name,
		       COUNT(*)::int,
		       COALESCE(AVG(output_kg), 0)
		FROM mes_shift_logs
		WHERE plant_code = $1 AND shift_date >= $2::date
		GROUP BY shift_name
		ORDER BY shift_name`, plantCode, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShiftMetricRow
	for rows.Next() {
		var row ShiftMetricRow
		if err := rows.Scan(&row.ShiftName, &row.Samples, &row.AvgOutput); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) DailyProductionSummary(ctx context.Context, plantCode string, day time.Time) (map[string]any, error) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	var runCount int
	var kgOut float64
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)::int, COALESCE(SUM(kg_out), 0)
		FROM mes_production_runs
		WHERE status = 'completed' AND completed_at >= $1 AND completed_at < $2
		  AND ($3 = '' OR facility ILIKE $3 OR facility = $3)`,
		start, end, plantCode).Scan(&runCount, &kgOut)
	if err != nil {
		return nil, err
	}
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
		"plant":            plantCode,
		"date":             start.Format("2006-01-02"),
		"completed_runs":   runCount,
		"output_kg":        kgOut,
		"downtime_minutes": dtMin,
	}, nil
}

func (s *Store) QualitySummaryFromRuns(ctx context.Context, since time.Time, limit int) (map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT batch_business_id, process, kg_out, moisture, status, completed_at
		FROM mes_production_runs
		WHERE completed_at >= $1 AND status = 'completed'
		ORDER BY completed_at DESC
		LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batches []map[string]any
	var moistureSum float64
	var moistureN int
	for rows.Next() {
		var batchID, process, status string
		var kgOut, moisture *float64
		var completedAt *time.Time
		if err := rows.Scan(&batchID, &process, &kgOut, &moisture, &status, &completedAt); err != nil {
			return nil, err
		}
		item := map[string]any{
			"batch_business_id": batchID,
			"process":           process,
			"status":            status,
		}
		if kgOut != nil {
			item["kg_out"] = *kgOut
		}
		if moisture != nil {
			item["moisture"] = *moisture
			moistureSum += *moisture
			moistureN++
		}
		if completedAt != nil {
			item["completed_at"] = completedAt
		}
		batches = append(batches, item)
	}
	avgMoisture := 0.0
	if moistureN > 0 {
		avgMoisture = moistureSum / float64(moistureN)
	}
	return map[string]any{
		"since":          since,
		"batch_count":    len(batches),
		"avg_moisture":   avgMoisture,
		"recent_batches": batches,
		"note":           fmt.Sprintf("Full SPC and cup scores live in quality-control; %d recent MES runs shown", len(batches)),
	}, rows.Err()
}
