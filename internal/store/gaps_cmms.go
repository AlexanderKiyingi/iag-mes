package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type WorkOrderPatch struct {
	Title     *string        `json:"title"`
	Priority  *string        `json:"priority"`
	Status    *string        `json:"status"`
	Assignee  *string        `json:"assignee"`
	DueAt     *time.Time     `json:"due_at"`
	Checklist []any          `json:"checklist"`
	Attrs     map[string]any `json:"attrs"`
}

func (s *Store) NextWorkOrderNum(ctx context.Context) (string, error) {
	var maxNum int
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(
			CASE WHEN num ~ '^WO-[0-9]+$' THEN SUBSTRING(num FROM 4)::int ELSE 0 END
		), 2280) FROM mes_work_orders`).Scan(&maxNum)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("WO-%d", maxNum+1), nil
}

func (s *Store) PatchWorkOrder(ctx context.Context, num string, patch WorkOrderPatch) (*WorkOrder, error) {
	wo, err := s.GetWorkOrder(ctx, num)
	if err != nil {
		return nil, err
	}
	if patch.Title != nil {
		wo.Title = *patch.Title
	}
	if patch.Priority != nil {
		wo.Priority = *patch.Priority
	}
	if patch.Status != nil {
		wo.Status = *patch.Status
	}
	if patch.Assignee != nil {
		wo.Assignee = patch.Assignee
	}
	if patch.DueAt != nil {
		wo.DueAt = patch.DueAt
	}
	if patch.Checklist != nil {
		if wo.Attrs == nil {
			wo.Attrs = map[string]any{}
		}
		wo.Attrs["checklist"] = patch.Checklist
	}
	if patch.Attrs != nil {
		if wo.Attrs == nil {
			wo.Attrs = map[string]any{}
		}
		for k, v := range patch.Attrs {
			wo.Attrs[k] = v
		}
	}
	attrs, _ := json.Marshal(wo.Attrs)
	err = s.pool.QueryRow(ctx, `
		UPDATE mes_work_orders
		SET title=$2, priority=$3, status=$4, assignee=$5, due_at=$6, attrs=$7::jsonb, updated_at=NOW()
		WHERE num=$1
		RETURNING id, num, title, asset_tag, wo_type, priority, status, assignee, due_at, completed_at, attrs, created_at, updated_at`,
		num, wo.Title, wo.Priority, wo.Status, wo.Assignee, wo.DueAt, attrs).Scan(
		&wo.ID, &wo.Num, &wo.Title, &wo.AssetTag, &wo.WOType, &wo.Priority, &wo.Status,
		&wo.Assignee, &wo.DueAt, &wo.CompletedAt, &attrs, &wo.CreatedAt, &wo.UpdatedAt)
	if err != nil {
		return nil, err
	}
	wo.Attrs = scanAttrs(attrs)
	return wo, nil
}

func (s *Store) StartWorkOrder(ctx context.Context, num string) (*WorkOrder, error) {
	status := "in_progress"
	return s.PatchWorkOrder(ctx, num, WorkOrderPatch{Status: &status})
}

func (s *Store) CreateWorkOrderFromPM(ctx context.Context, sch PMSchedule, tpl PMTemplate) (*WorkOrder, error) {
	num, err := s.NextWorkOrderNum(ctx)
	if err != nil {
		return nil, err
	}
	due := sch.NextDueAt
	attrs := map[string]any{
		"checklist":     tpl.Checklist,
		"pm_template":   tpl.Code,
		"auto_generated": true,
	}
	wo := WorkOrder{
		Num:      num,
		Title:    fmt.Sprintf("%s — %s", tpl.Name, sch.AssetTag),
		AssetTag: sch.AssetTag,
		WOType:   "preventive",
		Priority: "medium",
		Status:   "open",
		DueAt:    &due,
		Attrs:    attrs,
	}
	var attrsJSON []byte
	attrsJSON, _ = json.Marshal(attrs)
	var pmScheduleID = sch.ID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO mes_work_orders (num, title, asset_tag, wo_type, priority, status, due_at, attrs, pm_schedule_id)
		VALUES ($1,$2,$3,'preventive','medium','open',$4,$5::jsonb,$6)
		RETURNING id, num, title, asset_tag, wo_type, priority, status, assignee, due_at, completed_at, attrs, created_at, updated_at`,
		wo.Num, wo.Title, wo.AssetTag, wo.DueAt, attrsJSON, pmScheduleID).Scan(
		&wo.ID, &wo.Num, &wo.Title, &wo.AssetTag, &wo.WOType, &wo.Priority, &wo.Status,
		&wo.Assignee, &wo.DueAt, &wo.CompletedAt, &attrsJSON, &wo.CreatedAt, &wo.UpdatedAt)
	if err != nil {
		return nil, err
	}
	wo.Attrs = scanAttrs(attrsJSON)
	return &wo, nil
}

func (s *Store) HasOpenWorkOrderForPMSchedule(ctx context.Context, scheduleID uuid.UUID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM mes_work_orders
			WHERE pm_schedule_id = $1 AND status NOT IN ('completed', 'cancelled')
		)`, scheduleID).Scan(&exists)
	return exists, err
}

func (s *Store) CreatePMTemplate(ctx context.Context, t PMTemplate) (*PMTemplate, error) {
	checklist, _ := json.Marshal(t.Checklist)
	attrs, _ := json.Marshal(t.Attrs)
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mes_pm_templates (code, name, asset_category, checklist, interval_days, attrs)
		VALUES ($1,$2,$3,COALESCE($4::jsonb,'[]'),COALESCE(NULLIF($5,0),30),COALESCE($6::jsonb,'{}'))
		RETURNING id, code, name, asset_category, checklist, interval_days, attrs`,
		t.Code, t.Name, t.AssetCategory, checklist, t.IntervalDays, attrs).Scan(
		&t.ID, &t.Code, &t.Name, &t.AssetCategory, &checklist, &t.IntervalDays, &attrs)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(checklist, &t.Checklist)
	t.Attrs = scanAttrs(attrs)
	return &t, nil
}

func (s *Store) CreatePMSchedule(ctx context.Context, sch PMSchedule) (*PMSchedule, error) {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mes_pm_schedules (template_id, asset_tag, next_due_at, status)
		VALUES ($1,$2,$3,COALESCE(NULLIF($4,''),'scheduled'))
		RETURNING id, template_id, asset_tag, next_due_at, last_done_at, status`,
		sch.TemplateID, sch.AssetTag, sch.NextDueAt, sch.Status).Scan(
		&sch.ID, &sch.TemplateID, &sch.AssetTag, &sch.NextDueAt, &sch.LastDoneAt, &sch.Status)
	if err != nil {
		return nil, err
	}
	return &sch, nil
}

func (s *Store) SyncPMScheduleStatuses(ctx context.Context) (overdue int, err error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE mes_pm_schedules SET status = 'overdue', updated_at = NOW()
		WHERE status = 'scheduled' AND next_due_at < NOW()`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *Store) GetPMTemplate(ctx context.Context, id uuid.UUID) (*PMTemplate, error) {
	var t PMTemplate
	var checklist, attrs []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, code, name, asset_category, checklist, interval_days, attrs
		FROM mes_pm_templates WHERE id = $1`, id).Scan(
		&t.ID, &t.Code, &t.Name, &t.AssetCategory, &checklist, &t.IntervalDays, &attrs)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal(checklist, &t.Checklist)
	t.Attrs = scanAttrs(attrs)
	return &t, nil
}

func (s *Store) AdvancePMScheduleAfterComplete(ctx context.Context, scheduleID uuid.UUID) error {
	schRows, err := s.pool.Query(ctx, `
		SELECT s.id, s.template_id, s.asset_tag, s.next_due_at, s.last_done_at, s.status, t.interval_days
		FROM mes_pm_schedules s
		JOIN mes_pm_templates t ON t.id = s.template_id
		WHERE s.id = $1`, scheduleID)
	if err != nil {
		return err
	}
	defer schRows.Close()
	if !schRows.Next() {
		return ErrNotFound
	}
	var sch PMSchedule
	var intervalDays int
	if err := schRows.Scan(&sch.ID, &sch.TemplateID, &sch.AssetTag, &sch.NextDueAt, &sch.LastDoneAt, &sch.Status, &intervalDays); err != nil {
		return err
	}
	now := time.Now().UTC()
	next := now.Add(time.Duration(intervalDays) * 24 * time.Hour)
	_, err = s.pool.Exec(ctx, `
		UPDATE mes_pm_schedules SET last_done_at=$2, next_due_at=$3, status='scheduled', updated_at=NOW()
		WHERE id=$1`, scheduleID, now, next)
	return err
}
