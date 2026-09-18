package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Scheduling moved to iag-production with the runs (008–011 there). MES
// keeps the ERP-synced production order list its planners read, the
// technician roster and the shift timetable per plant.

type ProductionOrder struct {
	ID        uuid.UUID      `json:"id"`
	PONum     string         `json:"po_num"`
	Customer  string         `json:"customer"`
	Product   string         `json:"product"`
	QtyKg     float64        `json:"qty_kg"`
	OriginLot *string        `json:"origin_lot,omitempty"`
	AssetTag  *string        `json:"asset_tag,omitempty"`
	Status    string         `json:"status"`
	DueAt     *time.Time     `json:"due_at,omitempty"`
	ERPRef    *string        `json:"erp_ref,omitempty"`
	Attrs     map[string]any `json:"attrs"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
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
