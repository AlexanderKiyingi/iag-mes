package store

import (
	"context"
	"time"
)

type TelemetryPoint struct {
	AssetTag   string    `json:"asset_tag"`
	Metric     string    `json:"metric"`
	Value      float64   `json:"value"`
	Unit       string    `json:"unit"`
	RecordedAt time.Time `json:"recorded_at"`
}

type AIRecommendation struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	Confidence *float64   `json:"confidence,omitempty"`
	AssetTag   *string    `json:"asset_tag,omitempty"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (s *Store) UpsertTelemetry(ctx context.Context, p TelemetryPoint) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mes_asset_telemetry_latest (asset_tag, metric, value, unit, recorded_at)
		VALUES ($1,$2,$3,$4,COALESCE($5,NOW()))
		ON CONFLICT (asset_tag, metric) DO UPDATE SET
		  value = EXCLUDED.value, unit = EXCLUDED.unit, recorded_at = EXCLUDED.recorded_at`,
		p.AssetTag, p.Metric, p.Value, p.Unit, p.RecordedAt)
	return err
}

func (s *Store) ListTelemetry(ctx context.Context, assetTag string) ([]TelemetryPoint, error) {
	q := `SELECT asset_tag, metric, value, unit, recorded_at FROM mes_asset_telemetry_latest WHERE 1=1`
	args := []any{}
	if assetTag != "" {
		q += ` AND asset_tag = $1`
		args = append(args, assetTag)
	}
	q += ` ORDER BY asset_tag, metric`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TelemetryPoint
	for rows.Next() {
		var p TelemetryPoint
		if err := rows.Scan(&p.AssetTag, &p.Metric, &p.Value, &p.Unit, &p.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListAIRecommendations(ctx context.Context, status string) ([]AIRecommendation, error) {
	q := `SELECT id::text, kind, title, body, confidence, asset_tag, status, created_at
	      FROM mes_ai_recommendations WHERE 1=1`
	args := []any{}
	if status != "" {
		q += ` AND status = $1`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT 20`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIRecommendation
	for rows.Next() {
		var r AIRecommendation
		if err := rows.Scan(&r.ID, &r.Kind, &r.Title, &r.Body, &r.Confidence, &r.AssetTag, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateAIRecommendation(ctx context.Context, r AIRecommendation) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mes_ai_recommendations (kind, title, body, confidence, asset_tag, status)
		VALUES ($1,$2,$3,$4,$5,COALESCE(NULLIF($6,''),'open'))`,
		r.Kind, r.Title, r.Body, r.Confidence, r.AssetTag, r.Status)
	return err
}
