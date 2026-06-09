package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type KPIDefinition struct {
	ID            uuid.UUID `json:"id"`
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	Category      string    `json:"category"`
	Unit          string    `json:"unit"`
	Target        float64   `json:"target"`
	WarnThreshold *float64  `json:"warn_threshold,omitempty"`
	CritThreshold *float64  `json:"crit_threshold,omitempty"`
	Direction     string    `json:"direction"`
	Active        bool      `json:"active"`
}

type KPISnapshot struct {
	ID         uuid.UUID `json:"id"`
	KPICode    string    `json:"kpi_code"`
	PlantCode  *string   `json:"plant_code,omitempty"`
	AssetTag   *string   `json:"asset_tag,omitempty"`
	Value      float64   `json:"value"`
	RecordedAt time.Time `json:"recorded_at"`
}

type AlertRule struct {
	ID        uuid.UUID      `json:"id"`
	Code      string         `json:"code"`
	Name      string         `json:"name"`
	Condition string         `json:"condition"`
	Severity  string         `json:"severity"`
	Active    bool           `json:"active"`
	Attrs     map[string]any `json:"attrs"`
}

type Alert struct {
	ID             uuid.UUID      `json:"id"`
	RuleID         *uuid.UUID     `json:"rule_id,omitempty"`
	Severity       string         `json:"severity"`
	Source         string         `json:"source"`
	Message        string         `json:"message"`
	Status         string         `json:"status"`
	AcknowledgedAt *time.Time     `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time     `json:"resolved_at,omitempty"`
	OccurredAt     time.Time      `json:"occurred_at"`
	Attrs          map[string]any `json:"attrs"`
}

func (s *Store) ListKPIDefinitions(ctx context.Context) ([]KPIDefinition, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, category, unit, target, warn_threshold, crit_threshold, direction, active
		FROM mes_kpi_definitions WHERE active = true ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KPIDefinition
	for rows.Next() {
		var k KPIDefinition
		if err := rows.Scan(&k.ID, &k.Code, &k.Name, &k.Category, &k.Unit, &k.Target,
			&k.WarnThreshold, &k.CritThreshold, &k.Direction, &k.Active); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) ListKPISnapshots(ctx context.Context, kpiCode string, limit int) ([]KPISnapshot, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, kpi_code, plant_code, asset_tag, value, recorded_at FROM mes_kpi_snapshots WHERE 1=1`
	args := []any{}
	if kpiCode != "" {
		q += ` AND kpi_code = $1`
		args = append(args, kpiCode)
	}
	q += ` ORDER BY recorded_at DESC LIMIT ` + strconv.Itoa(limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KPISnapshot
	for rows.Next() {
		var snap KPISnapshot
		if err := rows.Scan(&snap.ID, &snap.KPICode, &snap.PlantCode, &snap.AssetTag, &snap.Value, &snap.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

func (s *Store) RecordKPISnapshot(ctx context.Context, snap KPISnapshot) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mes_kpi_snapshots (kpi_code, plant_code, asset_tag, value, recorded_at)
		VALUES ($1,$2,$3,$4,COALESCE($5,NOW()))`,
		snap.KPICode, snap.PlantCode, snap.AssetTag, snap.Value, snap.RecordedAt)
	return err
}

func (s *Store) ListAlertRules(ctx context.Context) ([]AlertRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, condition, severity, active, attrs FROM mes_alert_rules WHERE active = true ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertRule
	for rows.Next() {
		var r AlertRule
		var attrs []byte
		if err := rows.Scan(&r.ID, &r.Code, &r.Name, &r.Condition, &r.Severity, &r.Active, &attrs); err != nil {
			return nil, err
		}
		r.Attrs = scanAttrs(attrs)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ListAlerts(ctx context.Context, status string, limit int) ([]Alert, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, rule_id, severity, source, message, status, acknowledged_at, resolved_at, occurred_at, attrs
	      FROM mes_alerts WHERE 1=1`
	args := []any{}
	if status != "" {
		q += ` AND status = $1`
		args = append(args, status)
	}
	q += ` ORDER BY occurred_at DESC LIMIT ` + strconv.Itoa(limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		var a Alert
		var attrs []byte
		if err := rows.Scan(&a.ID, &a.RuleID, &a.Severity, &a.Source, &a.Message, &a.Status,
			&a.AcknowledgedAt, &a.ResolvedAt, &a.OccurredAt, &attrs); err != nil {
			return nil, err
		}
		a.Attrs = scanAttrs(attrs)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) CreateAlert(ctx context.Context, a Alert) (*Alert, error) {
	var attrs []byte
	if a.Attrs != nil {
		attrs, _ = json.Marshal(a.Attrs)
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mes_alerts (rule_id, severity, source, message, status, occurred_at, attrs)
		VALUES ($1,$2,$3,$4,COALESCE(NULLIF($5,''),'new'),COALESCE($6,NOW()),COALESCE($7::jsonb,'{}'))
		RETURNING id, rule_id, severity, source, message, status, acknowledged_at, resolved_at, occurred_at, attrs`,
		a.RuleID, a.Severity, a.Source, a.Message, a.Status, a.OccurredAt, attrs).Scan(
		&a.ID, &a.RuleID, &a.Severity, &a.Source, &a.Message, &a.Status,
		&a.AcknowledgedAt, &a.ResolvedAt, &a.OccurredAt, &attrs)
	if err != nil {
		return nil, err
	}
	a.Attrs = scanAttrs(attrs)
	return &a, nil
}

func (s *Store) AckAlert(ctx context.Context, id uuid.UUID) (*Alert, error) {
	now := time.Now().UTC()
	var a Alert
	var attrs []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE mes_alerts SET status='ack', acknowledged_at=$2 WHERE id=$1
		RETURNING id, rule_id, severity, source, message, status, acknowledged_at, resolved_at, occurred_at, attrs`,
		id, now).Scan(&a.ID, &a.RuleID, &a.Severity, &a.Source, &a.Message, &a.Status,
		&a.AcknowledgedAt, &a.ResolvedAt, &a.OccurredAt, &attrs)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.Attrs = scanAttrs(attrs)
	return &a, nil
}

func (s *Store) ResolveAlert(ctx context.Context, id uuid.UUID) (*Alert, error) {
	now := time.Now().UTC()
	var a Alert
	var attrs []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE mes_alerts SET status='resolved', resolved_at=$2 WHERE id=$1
		RETURNING id, rule_id, severity, source, message, status, acknowledged_at, resolved_at, occurred_at, attrs`,
		id, now).Scan(&a.ID, &a.RuleID, &a.Severity, &a.Source, &a.Message, &a.Status,
		&a.AcknowledgedAt, &a.ResolvedAt, &a.OccurredAt, &attrs)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.Attrs = scanAttrs(attrs)
	return &a, nil
}
