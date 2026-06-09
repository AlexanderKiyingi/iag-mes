package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ProductionOrder struct {
	ID         uuid.UUID      `json:"id"`
	PONum      string         `json:"po_num"`
	Customer   string         `json:"customer"`
	Product    string         `json:"product"`
	QtyKg      float64        `json:"qty_kg"`
	OriginLot  *string        `json:"origin_lot,omitempty"`
	AssetTag   *string        `json:"asset_tag,omitempty"`
	Status     string         `json:"status"`
	DueAt      *time.Time     `json:"due_at,omitempty"`
	ERPRef     *string        `json:"erp_ref,omitempty"`
	Attrs      map[string]any `json:"attrs"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type ScheduleBlock struct {
	ID                uuid.UUID  `json:"id"`
	AssetTag          string     `json:"asset_tag"`
	BlockType         string     `json:"block_type"`
	Label             string     `json:"label"`
	StartsAt          time.Time  `json:"starts_at"`
	EndsAt            time.Time  `json:"ends_at"`
	ProductionOrderID *uuid.UUID `json:"production_order_id,omitempty"`
	WorkOrderID       *uuid.UUID `json:"work_order_id,omitempty"`
}

type ShiftLog struct {
	ID            uuid.UUID      `json:"id"`
	PlantCode     string         `json:"plant_code"`
	ShiftName     string         `json:"shift_name"`
	ShiftDate     time.Time      `json:"shift_date"`
	HandoverNotes *string        `json:"handover_notes,omitempty"`
	OutputKg      *float64       `json:"output_kg,omitempty"`
	Attrs         map[string]any `json:"attrs"`
	CreatedAt     time.Time      `json:"created_at"`
}

type Operator struct {
	ID        uuid.UUID      `json:"id"`
	Ref       string         `json:"ref"`
	Name      string         `json:"name"`
	Role      string         `json:"role"`
	Shift     *string        `json:"shift,omitempty"`
	Station   *string        `json:"station,omitempty"`
	Active    bool           `json:"active"`
	Attrs     map[string]any `json:"attrs"`
	CreatedAt time.Time      `json:"created_at"`
}

type Technician struct {
	ID        uuid.UUID      `json:"id"`
	UserID    *uuid.UUID     `json:"user_id,omitempty"`
	Name      string         `json:"name"`
	Role      string         `json:"role"`
	PlantCode *string        `json:"plant_code,omitempty"`
	Active    bool           `json:"active"`
	Attrs     map[string]any `json:"attrs"`
	CreatedAt time.Time      `json:"created_at"`
}

func (s *Store) ListProductionOrders(ctx context.Context, status string) ([]ProductionOrder, error) {
	q := `SELECT id, po_num, customer, product, qty_kg, origin_lot, asset_tag, status, due_at, erp_ref, attrs, created_at, updated_at
	      FROM mes_production_orders WHERE 1=1`
	args := []any{}
	if status != "" {
		q += ` AND status = $1`
		args = append(args, status)
	}
	q += ` ORDER BY due_at NULLS LAST, created_at DESC LIMIT 100`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductionOrder
	for rows.Next() {
		var po ProductionOrder
		var attrs []byte
		if err := rows.Scan(&po.ID, &po.PONum, &po.Customer, &po.Product, &po.QtyKg, &po.OriginLot,
			&po.AssetTag, &po.Status, &po.DueAt, &po.ERPRef, &attrs, &po.CreatedAt, &po.UpdatedAt); err != nil {
			return nil, err
		}
		po.Attrs = scanAttrs(attrs)
		out = append(out, po)
	}
	return out, rows.Err()
}

func (s *Store) CreateProductionOrder(ctx context.Context, po ProductionOrder) (*ProductionOrder, error) {
	var attrs []byte
	if po.Attrs != nil {
		attrs, _ = json.Marshal(po.Attrs)
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mes_production_orders (po_num, customer, product, qty_kg, origin_lot, asset_tag, status, due_at, erp_ref, attrs)
		VALUES ($1,$2,$3,$4,$5,$6,COALESCE(NULLIF($7,''),'queued'),$8,$9,COALESCE($10::jsonb,'{}'))
		RETURNING id, po_num, customer, product, qty_kg, origin_lot, asset_tag, status, due_at, erp_ref, attrs, created_at, updated_at`,
		po.PONum, po.Customer, po.Product, po.QtyKg, po.OriginLot, po.AssetTag, po.Status, po.DueAt, po.ERPRef, attrs).Scan(
		&po.ID, &po.PONum, &po.Customer, &po.Product, &po.QtyKg, &po.OriginLot, &po.AssetTag,
		&po.Status, &po.DueAt, &po.ERPRef, &attrs, &po.CreatedAt, &po.UpdatedAt)
	if err != nil {
		return nil, err
	}
	po.Attrs = scanAttrs(attrs)
	return &po, nil
}

func (s *Store) ListScheduleBlocks(ctx context.Context, assetTag string, from, to time.Time) ([]ScheduleBlock, error) {
	q := `SELECT id, asset_tag, block_type, label, starts_at, ends_at, production_order_id, work_order_id
	      FROM mes_schedule_blocks WHERE starts_at >= $1 AND ends_at <= $2`
	args := []any{from, to}
	if assetTag != "" {
		q += ` AND asset_tag = $3`
		args = append(args, assetTag)
	}
	q += ` ORDER BY starts_at`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduleBlock
	for rows.Next() {
		var b ScheduleBlock
		if err := rows.Scan(&b.ID, &b.AssetTag, &b.BlockType, &b.Label, &b.StartsAt, &b.EndsAt,
			&b.ProductionOrderID, &b.WorkOrderID); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) CreateScheduleBlock(ctx context.Context, b ScheduleBlock) (*ScheduleBlock, error) {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mes_schedule_blocks (asset_tag, block_type, label, starts_at, ends_at, production_order_id, work_order_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, asset_tag, block_type, label, starts_at, ends_at, production_order_id, work_order_id`,
		b.AssetTag, b.BlockType, b.Label, b.StartsAt, b.EndsAt, b.ProductionOrderID, b.WorkOrderID).Scan(
		&b.ID, &b.AssetTag, &b.BlockType, &b.Label, &b.StartsAt, &b.EndsAt, &b.ProductionOrderID, &b.WorkOrderID)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) ListShiftLogs(ctx context.Context, plantCode string) ([]ShiftLog, error) {
	q := `SELECT id, plant_code, shift_name, shift_date, handover_notes, output_kg, attrs, created_at
	      FROM mes_shift_logs WHERE 1=1`
	args := []any{}
	if plantCode != "" {
		q += ` AND plant_code = $1`
		args = append(args, plantCode)
	}
	q += ` ORDER BY shift_date DESC, shift_name LIMIT 50`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShiftLog
	for rows.Next() {
		var sl ShiftLog
		var attrs []byte
		if err := rows.Scan(&sl.ID, &sl.PlantCode, &sl.ShiftName, &sl.ShiftDate, &sl.HandoverNotes,
			&sl.OutputKg, &attrs, &sl.CreatedAt); err != nil {
			return nil, err
		}
		sl.Attrs = scanAttrs(attrs)
		out = append(out, sl)
	}
	return out, rows.Err()
}

func (s *Store) CreateShiftLog(ctx context.Context, sl ShiftLog) (*ShiftLog, error) {
	var attrs []byte
	if sl.Attrs != nil {
		attrs, _ = json.Marshal(sl.Attrs)
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mes_shift_logs (plant_code, shift_name, shift_date, handover_notes, output_kg, attrs)
		VALUES ($1,$2,$3,$4,$5,COALESCE($6::jsonb,'{}'))
		ON CONFLICT (plant_code, shift_name, shift_date) DO UPDATE SET
		  handover_notes = EXCLUDED.handover_notes,
		  output_kg = EXCLUDED.output_kg,
		  attrs = EXCLUDED.attrs
		RETURNING id, plant_code, shift_name, shift_date, handover_notes, output_kg, attrs, created_at`,
		sl.PlantCode, sl.ShiftName, sl.ShiftDate, sl.HandoverNotes, sl.OutputKg, attrs).Scan(
		&sl.ID, &sl.PlantCode, &sl.ShiftName, &sl.ShiftDate, &sl.HandoverNotes, &sl.OutputKg, &attrs, &sl.CreatedAt)
	if err != nil {
		return nil, err
	}
	sl.Attrs = scanAttrs(attrs)
	return &sl, nil
}

func (s *Store) ListOperators(ctx context.Context) ([]Operator, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, ref, name, role, shift, station, active, attrs, created_at
		FROM mes_operators WHERE active = true ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Operator
	for rows.Next() {
		var o Operator
		var attrs []byte
		if err := rows.Scan(&o.ID, &o.Ref, &o.Name, &o.Role, &o.Shift, &o.Station, &o.Active, &attrs, &o.CreatedAt); err != nil {
			return nil, err
		}
		o.Attrs = scanAttrs(attrs)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) ListTechnicians(ctx context.Context) ([]Technician, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, name, role, plant_code, active, attrs, created_at
		FROM mes_technicians WHERE active = true ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Technician
	for rows.Next() {
		var t Technician
		var attrs []byte
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Role, &t.PlantCode, &t.Active, &attrs, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Attrs = scanAttrs(attrs)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetShiftDefinition(ctx context.Context, plantCode string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sd.name, sd.start_time, sd.end_time
		FROM mes_shift_definitions sd
		JOIN mes_plants p ON p.id = sd.plant_id
		WHERE p.code = $1 ORDER BY sd.start_time`, plantCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var name string
		var start, end time.Time
		if err := rows.Scan(&name, &start, &end); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"name":       name,
			"start_time": start.Format("15:04"),
			"end_time":   end.Format("15:04"),
		})
	}
	return out, rows.Err()
}
