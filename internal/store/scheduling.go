package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Scheduling and production orders moved to iag-production with the runs
// (008–011 there). MES keeps the technician roster and the shift timetable
// per plant.

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
