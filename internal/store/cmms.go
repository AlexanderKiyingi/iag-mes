package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-mes/backend/internal/events"
)

type WorkOrder struct {
	ID          uuid.UUID      `json:"id"`
	Num         string         `json:"num"`
	Title       string         `json:"title"`
	AssetTag    string         `json:"asset_tag"`
	WOType      string         `json:"wo_type"`
	Priority    string         `json:"priority"`
	Status      string         `json:"status"`
	Assignee    *string        `json:"assignee,omitempty"`
	DueAt       *time.Time     `json:"due_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	Attrs       map[string]any `json:"attrs"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type DowntimeEvent struct {
	ID          uuid.UUID      `json:"id"`
	AssetTag    string         `json:"asset_tag"`
	Category    string         `json:"category"`
	Reason      string         `json:"reason"`
	StartedAt   time.Time      `json:"started_at"`
	EndedAt     *time.Time     `json:"ended_at,omitempty"`
	KgLost      *float64       `json:"kg_lost,omitempty"`
	OperatorRef *string        `json:"operator_ref,omitempty"`
	Attrs       map[string]any `json:"attrs"`
	CreatedAt   time.Time      `json:"created_at"`
}

type PMTemplate struct {
	ID            uuid.UUID      `json:"id"`
	Code          string         `json:"code"`
	Name          string         `json:"name"`
	AssetCategory string         `json:"asset_category"`
	Checklist     []any          `json:"checklist"`
	IntervalDays  int            `json:"interval_days"`
	Attrs         map[string]any `json:"attrs"`
}

type PMSchedule struct {
	ID         uuid.UUID  `json:"id"`
	TemplateID uuid.UUID  `json:"template_id"`
	AssetTag   string     `json:"asset_tag"`
	NextDueAt  time.Time  `json:"next_due_at"`
	LastDoneAt *time.Time `json:"last_done_at,omitempty"`
	Status     string     `json:"status"`
}

func (s *Store) ListWorkOrders(ctx context.Context, status string, limit int) ([]WorkOrder, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, num, title, asset_tag, wo_type, priority, status, assignee, due_at, completed_at, attrs, created_at, updated_at
	      FROM mes_work_orders WHERE 1=1`
	args := []any{}
	if status != "" {
		q += ` AND status = $1`
		args = append(args, status)
	}
	q += fmt.Sprintf(` ORDER BY due_at NULLS LAST, created_at DESC LIMIT %d`, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWorkOrders(rows)
}

func (s *Store) GetWorkOrder(ctx context.Context, num string) (*WorkOrder, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, num, title, asset_tag, wo_type, priority, status, assignee, due_at, completed_at, attrs, created_at, updated_at
		FROM mes_work_orders WHERE num = $1`, num)
	return scanWorkOrderRow(row)
}

func (s *Store) CreateWorkOrder(ctx context.Context, wo WorkOrder) (*WorkOrder, error) {
	var attrs []byte
	if wo.Attrs != nil {
		attrs, _ = json.Marshal(wo.Attrs)
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mes_work_orders (num, title, asset_tag, wo_type, priority, status, assignee, due_at, attrs)
		VALUES ($1,$2,$3,COALESCE(NULLIF($4,''),'preventive'),COALESCE(NULLIF($5,''),'medium'),
		        COALESCE(NULLIF($6,''),'open'),$7,$8,COALESCE($9::jsonb,'{}'))
		RETURNING id, num, title, asset_tag, wo_type, priority, status, assignee, due_at, completed_at, attrs, created_at, updated_at`,
		wo.Num, wo.Title, wo.AssetTag, wo.WOType, wo.Priority, wo.Status, wo.Assignee, wo.DueAt, attrs).Scan(
		&wo.ID, &wo.Num, &wo.Title, &wo.AssetTag, &wo.WOType, &wo.Priority, &wo.Status,
		&wo.Assignee, &wo.DueAt, &wo.CompletedAt, &attrs, &wo.CreatedAt, &wo.UpdatedAt)
	if err != nil {
		return nil, err
	}
	wo.Attrs = scanAttrs(attrs)
	return &wo, nil
}

func (s *Store) CompleteWorkOrder(ctx context.Context, num string) (*WorkOrder, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	wo, err := s.getWorkOrderTx(ctx, tx, num)
	if err != nil {
		return nil, err
	}
	var pmScheduleID *uuid.UUID
	_ = tx.QueryRow(ctx, `SELECT pm_schedule_id FROM mes_work_orders WHERE num=$1`, num).Scan(&pmScheduleID)
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `UPDATE mes_work_orders SET status='completed', completed_at=$2, updated_at=NOW() WHERE num=$1`, num, now)
	if err != nil {
		return nil, err
	}
	wo.Status = "completed"
	wo.CompletedAt = &now
	if s.bus != nil {
		data := map[string]any{
			"work_order_num": num,
			"asset_tag":      wo.AssetTag,
			"timestamp":      now.Format(time.RFC3339),
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeWorkOrderDone, data, wo.AssetTag); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if pmScheduleID != nil {
		_ = s.AdvancePMScheduleAfterComplete(ctx, *pmScheduleID)
	}
	return wo, nil
}

func (s *Store) getWorkOrderTx(ctx context.Context, tx pgx.Tx, num string) (*WorkOrder, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, num, title, asset_tag, wo_type, priority, status, assignee, due_at, completed_at, attrs, created_at, updated_at
		FROM mes_work_orders WHERE num = $1`, num)
	return scanWorkOrderRow(row)
}

func (s *Store) ListDowntimeEvents(ctx context.Context, assetTag string, limit int) ([]DowntimeEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, asset_tag, category, reason, started_at, ended_at, kg_lost, operator_ref, attrs, created_at
	      FROM mes_downtime_events WHERE 1=1`
	args := []any{}
	if assetTag != "" {
		q += ` AND asset_tag = $1`
		args = append(args, assetTag)
	}
	q += fmt.Sprintf(` ORDER BY started_at DESC LIMIT %d`, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DowntimeEvent
	for rows.Next() {
		var d DowntimeEvent
		var attrs []byte
		if err := rows.Scan(&d.ID, &d.AssetTag, &d.Category, &d.Reason, &d.StartedAt, &d.EndedAt,
			&d.KgLost, &d.OperatorRef, &attrs, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Attrs = scanAttrs(attrs)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) CreateDowntimeEvent(ctx context.Context, d DowntimeEvent) (*DowntimeEvent, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var attrs []byte
	if d.Attrs != nil {
		attrs, _ = json.Marshal(d.Attrs)
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO mes_downtime_events (asset_tag, category, reason, started_at, kg_lost, operator_ref, attrs)
		VALUES ($1,$2,$3,COALESCE($4,NOW()),$5,NULLIF($6,''),COALESCE($7::jsonb,'{}'))
		RETURNING id, asset_tag, category, reason, started_at, ended_at, kg_lost, operator_ref, attrs, created_at`,
		d.AssetTag, d.Category, d.Reason, d.StartedAt, d.KgLost, d.OperatorRef, attrs).Scan(
		&d.ID, &d.AssetTag, &d.Category, &d.Reason, &d.StartedAt, &d.EndedAt,
		&d.KgLost, &d.OperatorRef, &attrs, &d.CreatedAt)
	if err != nil {
		return nil, err
	}
	d.Attrs = scanAttrs(attrs)
	_, _ = tx.Exec(ctx, `UPDATE mes_assets SET status='down', updated_at=NOW() WHERE tag=$1`, d.AssetTag)
	if s.bus != nil {
		data := map[string]any{
			"asset_tag": d.AssetTag,
			"category":  d.Category,
			"reason":    d.Reason,
			"timestamp": d.StartedAt.UTC().Format(time.RFC3339),
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeDowntimeStarted, data, d.AssetTag); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) EndDowntimeEvent(ctx context.Context, id uuid.UUID) (*DowntimeEvent, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var d DowntimeEvent
	var attrs []byte
	err = tx.QueryRow(ctx, `
		SELECT id, asset_tag, category, reason, started_at, ended_at, kg_lost, operator_ref, attrs, created_at
		FROM mes_downtime_events WHERE id = $1 FOR UPDATE`, id).Scan(
		&d.ID, &d.AssetTag, &d.Category, &d.Reason, &d.StartedAt, &d.EndedAt,
		&d.KgLost, &d.OperatorRef, &attrs, &d.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `UPDATE mes_downtime_events SET ended_at=$2 WHERE id=$1`, id, now)
	if err != nil {
		return nil, err
	}
	d.EndedAt = &now
	d.Attrs = scanAttrs(attrs)
	_, _ = tx.Exec(ctx, `UPDATE mes_assets SET status='idle', updated_at=NOW() WHERE tag=$1`, d.AssetTag)
	if s.bus != nil {
		data := map[string]any{
			"asset_tag": d.AssetTag,
			"timestamp": now.Format(time.RFC3339),
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeDowntimeEnded, data, d.AssetTag); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) ListPMTemplates(ctx context.Context) ([]PMTemplate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, asset_category, checklist, interval_days, attrs FROM mes_pm_templates ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PMTemplate
	for rows.Next() {
		var t PMTemplate
		var checklist, attrs []byte
		if err := rows.Scan(&t.ID, &t.Code, &t.Name, &t.AssetCategory, &checklist, &t.IntervalDays, &attrs); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(checklist, &t.Checklist)
		t.Attrs = scanAttrs(attrs)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListPMSchedules(ctx context.Context, assetTag string) ([]PMSchedule, error) {
	q := `SELECT id, template_id, asset_tag, next_due_at, last_done_at, status FROM mes_pm_schedules WHERE 1=1`
	args := []any{}
	if assetTag != "" {
		q += ` AND asset_tag = $1`
		args = append(args, assetTag)
	}
	q += ` ORDER BY next_due_at`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PMSchedule
	for rows.Next() {
		var sch PMSchedule
		if err := rows.Scan(&sch.ID, &sch.TemplateID, &sch.AssetTag, &sch.NextDueAt, &sch.LastDoneAt, &sch.Status); err != nil {
			return nil, err
		}
		out = append(out, sch)
	}
	return out, rows.Err()
}

func scanWorkOrderRow(row pgx.Row) (*WorkOrder, error) {
	var wo WorkOrder
	var attrs []byte
	err := row.Scan(&wo.ID, &wo.Num, &wo.Title, &wo.AssetTag, &wo.WOType, &wo.Priority, &wo.Status,
		&wo.Assignee, &wo.DueAt, &wo.CompletedAt, &attrs, &wo.CreatedAt, &wo.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	wo.Attrs = scanAttrs(attrs)
	return &wo, nil
}

func scanWorkOrders(rows pgx.Rows) ([]WorkOrder, error) {
	var out []WorkOrder
	for rows.Next() {
		var wo WorkOrder
		var attrs []byte
		if err := rows.Scan(&wo.ID, &wo.Num, &wo.Title, &wo.AssetTag, &wo.WOType, &wo.Priority, &wo.Status,
			&wo.Assignee, &wo.DueAt, &wo.CompletedAt, &attrs, &wo.CreatedAt, &wo.UpdatedAt); err != nil {
			return nil, err
		}
		wo.Attrs = scanAttrs(attrs)
		out = append(out, wo)
	}
	return out, rows.Err()
}
