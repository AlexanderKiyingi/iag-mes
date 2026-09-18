package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Projection of iag-production's shop-floor KPIs (010).
//
// Production computes measures and KPIs from its execution data and
// publishes production.measures.rolled_up per plant-day with the plant,
// asset and shift snapshots. MES stores them in mes_kpi_snapshots with
// source='production' so its KPI screens, reports and alert rules read one
// table. The projection is an upsert keyed by (code, scope, key, period),
// because production republishes a plant-day every time it recomputes.

// RolledUpEvent is the data of a production.measures.rolled_up event.
type RolledUpEvent struct {
	PlantCode     string                       `json:"plant_code"`
	Day           string                       `json:"day"`
	PeriodStart   time.Time                    `json:"period_start"`
	Snapshots     []ProductionSnapshot         `json:"snapshots"`
	PlantMeasures map[string]float64           `json:"plant_measures"`
	Definitions   map[string]ProductionKPIMeta `json:"definitions"`
}

type ProductionSnapshot struct {
	KPICode     string    `json:"kpi_code"`
	ScopeType   string    `json:"scope_type"`
	ScopeKey    string    `json:"scope_key"`
	ShiftName   string    `json:"shift_name"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	Value       float64   `json:"value"`
	Target      *float64  `json:"target"`
	Status      string    `json:"status"`
}

type ProductionKPIMeta struct {
	Name      string `json:"name"`
	Unit      string `json:"unit"`
	Category  string `json:"category"`
	Direction string `json:"direction"`
}

// plantMeasureCodes are the plant-level measures the daily production
// report shows, stored as snapshots under PROD_* codes.
var plantMeasureCodes = map[string]ProductionKPIMeta{
	"runs_completed": {"PROD_RUNS_COMPLETED", "", "production", "up"},
	"kg_in":          {"PROD_KG_IN", "kg", "production", "up"},
	"product_kg":     {"PROD_PRODUCT_KG", "kg", "production", "up"},
	"reject_kg":      {"PROD_REJECT_KG", "kg", "production", "down"},
	"down_min":       {"PROD_DOWN_MIN", "min", "production", "down"},
	"running_min":    {"PROD_RUNNING_MIN", "min", "production", "up"},
	"ccp_fails":      {"PROD_CCP_FAILS", "", "production", "down"},
	"holds":          {"PROD_HOLDS", "", "production", "down"},
}

// ParseRolledUp decodes the event data map.
func ParseRolledUp(data map[string]any) (*RolledUpEvent, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var ev RolledUpEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, err
	}
	if strings.TrimSpace(ev.PlantCode) == "" && len(ev.Snapshots) == 0 {
		return nil, fmt.Errorf("rolled_up event without plant or snapshots")
	}
	return &ev, nil
}

// ApplyProductionRollup projects one event. Definitions are created on
// first sight (the snapshot FK needs them); an asset's OEE snapshot also
// refreshes mes_assets.oee_pct, which used to be typed by hand.
func (s *Store) ApplyProductionRollup(ctx context.Context, ev *RolledUpEvent) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	ensureDef := func(code string, meta ProductionKPIMeta) error {
		name := meta.Name
		if name == "" {
			name = code
		}
		category := meta.Category
		if category == "" {
			category = "production"
		}
		direction := meta.Direction
		if direction != "down" {
			direction = "up"
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO mes_kpi_definitions (code, name, category, unit, direction)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, unit = EXCLUDED.unit`,
			code, name, category, meta.Unit, direction)
		return err
	}
	upsert := func(code, scopeType, scopeKey, plant string, start, end time.Time, value float64, target *float64, status string) error {
		var assetTag *string
		if scopeType == "asset" {
			assetTag = &scopeKey
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO mes_kpi_snapshots (kpi_code, plant_code, asset_tag, value, recorded_at, scope_type, scope_key, period_end, target, status, source)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'production')
			ON CONFLICT (kpi_code, scope_type, scope_key, recorded_at) WHERE source = 'production'
			DO UPDATE SET value = EXCLUDED.value, target = EXCLUDED.target, status = EXCLUDED.status,
			              period_end = EXCLUDED.period_end, plant_code = EXCLUDED.plant_code`,
			code, plant, assetTag, value, start, scopeType, scopeKey, end, target, status)
		return err
	}

	n := 0
	seen := map[string]bool{}
	for _, sn := range ev.Snapshots {
		code := strings.ToUpper(strings.TrimSpace(sn.KPICode))
		if code == "" || sn.PeriodStart.IsZero() {
			continue
		}
		if !seen[code] {
			if err := ensureDef(code, ev.Definitions[code]); err != nil {
				return n, err
			}
			seen[code] = true
		}
		key := sn.ScopeKey
		if sn.ScopeType == "shift" && sn.ShiftName != "" {
			// MES has no shift ids; the name is what its screens show. The
			// plant is part of the key because the upsert key has no plant
			// column and every plant has a "Day" shift.
			key = ev.PlantCode + "/" + sn.ShiftName
		}
		if err := upsert(code, sn.ScopeType, key, ev.PlantCode, sn.PeriodStart, sn.PeriodEnd, sn.Value, sn.Target, sn.Status); err != nil {
			return n, err
		}
		n++
		if code == "OEE" && sn.ScopeType == "asset" {
			if _, err := tx.Exec(ctx, `UPDATE mes_assets SET oee_pct = $2, updated_at = NOW() WHERE tag = $1`, sn.ScopeKey, sn.Value); err != nil {
				return n, err
			}
		}
	}
	if !ev.PeriodStart.IsZero() {
		end := ev.PeriodStart.Add(24 * time.Hour)
		for measure, meta := range plantMeasureCodes {
			v, ok := ev.PlantMeasures[measure]
			if !ok {
				continue
			}
			if !seen[meta.Name] {
				if err := ensureDef(meta.Name, ProductionKPIMeta{Name: strings.ReplaceAll(strings.ToLower(meta.Name), "_", " "), Unit: meta.Unit, Category: meta.Category, Direction: meta.Direction}); err != nil {
					return n, err
				}
				seen[meta.Name] = true
			}
			if err := upsert(meta.Name, "plant", ev.PlantCode, ev.PlantCode, ev.PeriodStart, end, v, nil, ""); err != nil {
				return n, err
			}
			n++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return n, err
	}
	return n, nil
}

// RaiseProductionBreach turns a production.kpi.breached event into an MES
// alert, so the alert screen and notification rules see it with the
// telemetry alerts. Deduplicated on (code, scope, period): production only
// publishes when a snapshot turns crit, but a replayed message must not
// raise twice.
func (s *Store) RaiseProductionBreach(ctx context.Context, data map[string]any) error {
	str := func(k string) string {
		v, _ := data[k].(string)
		return strings.TrimSpace(v)
	}
	code, scopeType, scopeKey, plant := str("kpi_code"), str("scope_type"), str("scope_key"), str("plant_code")
	if code == "" {
		return nil
	}
	value, _ := data["value"].(float64)
	period := str("period_start")
	dedupe := fmt.Sprintf("%s|%s|%s|%s", code, scopeType, scopeKey, period)
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM mes_alerts WHERE source = 'production' AND attrs->>'dedupe' = $1)`, dedupe).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	where := scopeKey
	if name := str("shift_name"); name != "" {
		where = name + " shift"
	}
	if scopeType == "plant" {
		where = "plant " + scopeKey
	}
	msg := fmt.Sprintf("%s at %.1f on %s", code, value, where)
	if t, ok := data["target"].(float64); ok {
		msg += fmt.Sprintf(" (target %.1f)", t)
	}
	attrs := map[string]any{
		"dedupe": dedupe, "kpi_code": code, "scope_type": scopeType, "scope_key": scopeKey,
		"plant_code": plant, "value": value, "period_start": period,
	}
	if t, ok := data["target"].(float64); ok {
		attrs["target"] = t
	}
	_, err := s.CreateAlert(ctx, Alert{Severity: "crit", Source: "production", Message: msg, Attrs: attrs})
	return err
}
