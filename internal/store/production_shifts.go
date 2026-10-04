package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Shifts come from iag-production.
//
// MES used to keep its own shift list, mes_shift_definitions, seeded once with
// Day / Evening / Night and with no route to change it — while iag-production,
// which attributes runs to shifts and rolls KPIs up by them, keeps the real
// timetable. The two disagreed. Production now publishes a plant's whole shift
// list (production.shifts.changed) after every change and on boot; MES keeps
// the latest snapshot on the plant row and works out what is in force on a
// date when asked. mes_shift_definitions is no longer read.
//
// The snapshot lives in mes_plants.attrs rather than a new table so this
// needs no migration, and because it is evaluated at read time, a version
// that takes effect on a later date applies on that date without another
// event.

// ProductionShift is one row of production's prod_shifts, as published.
type ProductionShift struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	StartTime     string `json:"start_time"`
	EndTime       string `json:"end_time"`
	Days          []int  `json:"days"`
	CrewSize      int    `json:"crew_size"`
	EffectiveFrom string `json:"effective_from"`
	EffectiveTo   string `json:"effective_to,omitempty"`
	Active        bool   `json:"active"`
}

// ProductionShiftSnapshot is a plant's full list at one moment.
type ProductionShiftSnapshot struct {
	PlantCode   string            `json:"plant_code"`
	Shifts      []ProductionShift `json:"shifts"`
	PublishedAt string            `json:"published_at"`
}

const plantShiftsKey = "production_shifts"

// ParseShiftSnapshot decodes the event's data.
func ParseShiftSnapshot(data map[string]any) (ProductionShiftSnapshot, error) {
	var snap ProductionShiftSnapshot
	raw, err := json.Marshal(data)
	if err != nil {
		return snap, err
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		return snap, err
	}
	snap.PlantCode = strings.TrimSpace(snap.PlantCode)
	if snap.PlantCode == "" {
		return snap, errors.New("shift snapshot has no plant_code")
	}
	if _, err := time.Parse(time.RFC3339Nano, snap.PublishedAt); err != nil {
		return snap, fmt.Errorf("shift snapshot published_at: %w", err)
	}
	return snap, nil
}

// ApplyProductionShifts stores a plant's shift snapshot unless one published
// later is already held — events can arrive out of order across a restart.
// A snapshot for a plant MES does not know is logged and dropped: retrying
// cannot create the plant, and holding the partition on it would stall every
// other operations event.
func (s *Store) ApplyProductionShifts(ctx context.Context, snap ProductionShiftSnapshot) (bool, error) {
	published, _ := time.Parse(time.RFC3339Nano, snap.PublishedAt)
	payload, err := json.Marshal(snap)
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE mes_plants
		SET attrs = jsonb_set(COALESCE(attrs, '{}'::jsonb), '{`+plantShiftsKey+`}', $2::jsonb, true),
		    updated_at = NOW()
		WHERE lower(code) = lower($1)
		  AND (
		    attrs -> '`+plantShiftsKey+`' ->> 'published_at' IS NULL
		    OR (attrs -> '`+plantShiftsKey+`' ->> 'published_at')::timestamptz <= $3
		  )`, snap.PlantCode, payload, published)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		_ = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mes_plants WHERE lower(code) = lower($1))`, snap.PlantCode).Scan(&exists)
		if !exists {
			log.Printf("mes: shift snapshot for unknown plant %q dropped — production's plant_code must be an MES plant code", snap.PlantCode)
		}
		return false, nil
	}
	return true, nil
}

// PlantShifts is GET /plants/:code/shifts: the shifts in force at the plant
// on `date` (plant-local), or every row when `all` is set. ErrNotFound for an
// unknown plant; an empty list for a plant production has published nothing
// for.
func (s *Store) PlantShifts(ctx context.Context, plantCode string, date *time.Time, all bool) ([]ProductionShift, error) {
	var raw []byte
	var tz string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(attrs -> '`+plantShiftsKey+`', 'null'::jsonb), timezone
		FROM mes_plants WHERE lower(code) = lower($1)`, plantCode).Scan(&raw, &tz)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var snap ProductionShiftSnapshot
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &snap); err != nil {
			return nil, err
		}
	}
	if all {
		out := append([]ProductionShift{}, snap.Shifts...)
		sort.SliceStable(out, func(i, j int) bool { return out[i].StartTime < out[j].StartTime })
		return out, nil
	}
	day := time.Now()
	if date != nil {
		day = *date
	} else if loc, err := time.LoadLocation(tz); err == nil {
		day = day.In(loc)
	}
	return ShiftsInForceOn(snap.Shifts, day), nil
}

// ShiftsInForceOn is the timetable on one calendar day: the active rows whose
// effective range covers it, and of several versions of one shift only the
// newest that has taken effect. Weekdays are not filtered — `days` is part of
// the timetable a reader wants to see — so a Mon–Fri shift is listed on a
// Saturday with its days saying it does not run.
func ShiftsInForceOn(shifts []ProductionShift, day time.Time) []ProductionShift {
	date := day.Format("2006-01-02")
	newest := map[string]ProductionShift{}
	for _, sh := range shifts {
		if !sh.Active || sh.EffectiveFrom > date {
			continue
		}
		if sh.EffectiveTo != "" && sh.EffectiveTo < date {
			continue
		}
		key := strings.ToLower(sh.Name)
		if cur, ok := newest[key]; !ok || sh.EffectiveFrom > cur.EffectiveFrom {
			newest[key] = sh
		}
	}
	out := make([]ProductionShift, 0, len(newest))
	for _, sh := range newest {
		out = append(out, sh)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartTime < out[j].StartTime })
	return out
}
