package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PMTemplatePatch is what may change on a template after it is created. The
// code is the template's business key and stays fixed; schedules point at the
// row by id, so renaming or re-timing a template carries every schedule on it.
type PMTemplatePatch struct {
	Name          *string        `json:"name"`
	AssetCategory *string        `json:"asset_category"`
	Checklist     []any          `json:"checklist"`
	IntervalDays  *int           `json:"interval_days"`
	Attrs         map[string]any `json:"attrs"`
}

// PMSchedulePatch moves a schedule's next due date. Status follows the date —
// a schedule moved into the future is scheduled again, one moved into the past
// is overdue — because SyncPMScheduleStatuses only ever flips scheduled to
// overdue and nothing else would bring it back.
type PMSchedulePatch struct {
	NextDueAt *time.Time `json:"next_due_at"`
}

func (s *Store) PatchPMTemplate(ctx context.Context, id uuid.UUID, patch PMTemplatePatch) (*PMTemplate, error) {
	var t PMTemplate
	var checklist, attrs []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, code, name, asset_category, checklist, interval_days, attrs
		FROM mes_pm_templates WHERE id = $1`, id).Scan(
		&t.ID, &t.Code, &t.Name, &t.AssetCategory, &checklist, &t.IntervalDays, &attrs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(checklist, &t.Checklist)
	t.Attrs = scanAttrs(attrs)

	if patch.Name != nil {
		t.Name = *patch.Name
	}
	if patch.AssetCategory != nil {
		t.AssetCategory = *patch.AssetCategory
	}
	if patch.Checklist != nil {
		t.Checklist = patch.Checklist
	}
	if patch.IntervalDays != nil {
		t.IntervalDays = *patch.IntervalDays
	}
	// Merged key by key, as PatchWorkOrder does: a client that sends one key
	// must not wipe the others.
	if patch.Attrs != nil {
		if t.Attrs == nil {
			t.Attrs = map[string]any{}
		}
		for k, v := range patch.Attrs {
			t.Attrs[k] = v
		}
	}

	checklist, _ = json.Marshal(t.Checklist)
	attrs, _ = json.Marshal(t.Attrs)
	err = s.pool.QueryRow(ctx, `
		UPDATE mes_pm_templates
		SET name=$2, asset_category=$3, checklist=$4::jsonb, interval_days=$5, attrs=$6::jsonb, updated_at=NOW()
		WHERE id=$1
		RETURNING id, code, name, asset_category, checklist, interval_days, attrs`,
		id, t.Name, t.AssetCategory, checklist, t.IntervalDays, attrs).Scan(
		&t.ID, &t.Code, &t.Name, &t.AssetCategory, &checklist, &t.IntervalDays, &attrs)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(checklist, &t.Checklist)
	t.Attrs = scanAttrs(attrs)
	return &t, nil
}

func (s *Store) PatchPMSchedule(ctx context.Context, id uuid.UUID, patch PMSchedulePatch) (*PMSchedule, error) {
	var sch PMSchedule
	err := s.pool.QueryRow(ctx, `
		UPDATE mes_pm_schedules
		SET next_due_at = COALESCE($2::timestamptz, next_due_at),
		    status = CASE
		        WHEN $2::timestamptz IS NULL THEN status
		        WHEN $2::timestamptz < NOW() THEN 'overdue'
		        ELSE 'scheduled'
		    END,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, template_id, asset_tag, next_due_at, last_done_at, status`,
		id, patch.NextDueAt).Scan(
		&sch.ID, &sch.TemplateID, &sch.AssetTag, &sch.NextDueAt, &sch.LastDoneAt, &sch.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sch, nil
}
