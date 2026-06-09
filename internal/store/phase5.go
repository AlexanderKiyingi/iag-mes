package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
)

func (s *Store) InsertTelemetryPoint(ctx context.Context, p TelemetryPoint) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO mes_telemetry_timeseries (asset_tag, metric, value, unit, recorded_at)
		VALUES ($1,$2,$3,$4,COALESCE($5,NOW()))`,
		p.AssetTag, p.Metric, p.Value, p.Unit, p.RecordedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO mes_asset_telemetry_latest (asset_tag, metric, value, unit, recorded_at)
		VALUES ($1,$2,$3,$4,COALESCE($5,NOW()))
		ON CONFLICT (asset_tag, metric) DO UPDATE SET value=EXCLUDED.value, unit=EXCLUDED.unit, recorded_at=EXCLUDED.recorded_at`,
		p.AssetTag, p.Metric, p.Value, p.Unit, p.RecordedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListTelemetryHistory(ctx context.Context, assetTag, metric string, since time.Time, limit int) ([]TelemetryPoint, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT asset_tag, metric, value, unit, recorded_at FROM mes_telemetry_timeseries WHERE recorded_at >= $1`
	args := []any{since}
	n := 2
	if assetTag != "" {
		q += ` AND asset_tag = $` + strconv.Itoa(n)
		args = append(args, assetTag)
		n++
	}
	if metric != "" {
		q += ` AND metric = $` + strconv.Itoa(n)
		args = append(args, metric)
		n++
	}
	q += ` ORDER BY recorded_at DESC LIMIT ` + strconv.Itoa(limit)
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

func (s *Store) LogIntegrationCall(ctx context.Context, target, operation, correlation, status string, req, resp json.RawMessage, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mes_integration_calls (target, operation, correlation, status, request_body, response_body, error_message)
		VALUES ($1,$2,NULLIF($3,''),$4,$5::jsonb,$6::jsonb,NULLIF($7,''))`,
		target, operation, correlation, status, nullableJSON(req), nullableJSON(resp), errMsg)
	return err
}

func nullableJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (s *Store) RecordWarehouseHandoff(ctx context.Context, batchID, operation string, payload any, status string, response map[string]any, errMsg string) error {
	reqB, _ := json.Marshal(payload)
	respB, _ := json.Marshal(map[string]any{"response": response, "error": errMsg})
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mes_warehouse_handoffs (batch_business_id, operation, payload, status, response)
		VALUES ($1,$2,$3::jsonb,$4,$5::jsonb)`,
		batchID, operation, reqB, status, respB)
	return err
}

func (s *Store) RecordQCHandoff(ctx context.Context, batchID, sampleID string, runID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mes_qc_handoffs (batch_business_id, sample_id, run_id, status)
		VALUES ($1,$2,$3,'submitted')
		ON CONFLICT (batch_business_id, sample_id) DO NOTHING`,
		batchID, sampleID, runID)
	return err
}

func (s *Store) EnqueueERPSync(ctx context.Context, poNum string, payload json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mes_erp_sync_queue (po_num, payload, status)
		VALUES ($1,$2::jsonb,'pending')
		ON CONFLICT (po_num) DO UPDATE SET payload=EXCLUDED.payload, status='pending', error=NULL`,
		poNum, payload)
	return err
}

func (s *Store) ApplyERPSyncQueue(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, po_num, payload FROM mes_erp_sync_queue WHERE status='pending' ORDER BY created_at LIMIT 50`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	applied := 0
	for rows.Next() {
		var id uuid.UUID
		var poNum string
		var payload []byte
		if err := rows.Scan(&id, &poNum, &payload); err != nil {
			return applied, err
		}
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			_, _ = s.pool.Exec(ctx, `UPDATE mes_erp_sync_queue SET status='failed', error=$2 WHERE id=$1`, id, err.Error())
			continue
		}
		po := ProductionOrder{PONum: poNum}
		if v, ok := body["customer"].(string); ok {
			po.Customer = v
		}
		if v, ok := body["product"].(string); ok {
			po.Product = v
		}
		if v, ok := body["qty_kg"].(float64); ok {
			po.QtyKg = v
		}
		if v, ok := body["status"].(string); ok {
			po.Status = v
		}
		if _, err := s.UpsertProductionOrder(ctx, po); err != nil {
			_, _ = s.pool.Exec(ctx, `UPDATE mes_erp_sync_queue SET status='failed', error=$2 WHERE id=$1`, id, err.Error())
			continue
		}
		_, _ = s.pool.Exec(ctx, `UPDATE mes_erp_sync_queue SET status='applied', applied_at=NOW() WHERE id=$1`, id)
		applied++
	}
	return applied, nil
}

func (s *Store) UpsertProductionOrder(ctx context.Context, po ProductionOrder) (*ProductionOrder, error) {
	var attrs []byte
	if po.Attrs != nil {
		attrs, _ = json.Marshal(po.Attrs)
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mes_production_orders (po_num, customer, product, qty_kg, origin_lot, asset_tag, status, due_at, erp_ref, attrs)
		VALUES ($1,$2,$3,$4,$5,$6,COALESCE(NULLIF($7,''),'queued'),$8,$9,COALESCE($10::jsonb,'{}'))
		ON CONFLICT (po_num) DO UPDATE SET
		  customer=EXCLUDED.customer, product=EXCLUDED.product, qty_kg=EXCLUDED.qty_kg,
		  origin_lot=EXCLUDED.origin_lot, asset_tag=EXCLUDED.asset_tag, status=EXCLUDED.status,
		  due_at=EXCLUDED.due_at, erp_ref=EXCLUDED.erp_ref, updated_at=NOW()
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

type EnergyReading struct {
	ID         uuid.UUID `json:"id"`
	PlantCode  string    `json:"plant_code"`
	AssetTag   *string   `json:"asset_tag,omitempty"`
	KWh        float64   `json:"kwh"`
	TariffBand string    `json:"tariff_band"`
	RecordedAt time.Time `json:"recorded_at"`
}

func (s *Store) RecordEnergyReading(ctx context.Context, r EnergyReading) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mes_energy_readings (plant_code, asset_tag, kwh, tariff_band, recorded_at)
		VALUES ($1,$2,$3,COALESCE(NULLIF($4,''),'standard'),COALESCE($5,NOW()))`,
		r.PlantCode, r.AssetTag, r.KWh, r.TariffBand, r.RecordedAt)
	return err
}

func (s *Store) EnergySummary(ctx context.Context, plantCode string, since time.Time) (map[string]float64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tariff_band, COALESCE(SUM(kwh),0)
		FROM mes_energy_readings
		WHERE plant_code=$1 AND recorded_at >= $2
		GROUP BY tariff_band`, plantCode, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{"off_peak": 0, "standard": 0, "peak": 0, "total": 0}
	for rows.Next() {
		var band string
		var sum float64
		if err := rows.Scan(&band, &sum); err != nil {
			return nil, err
		}
		out[band] = sum
		out["total"] += sum
	}
	return out, rows.Err()
}

func (s *Store) UpdateAIRecommendation(ctx context.Context, id uuid.UUID, status string) error {
	res, err := s.pool.Exec(ctx, `
		UPDATE mes_ai_recommendations SET status=$2 WHERE id=$1`, id, status)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListIntegrationCalls(ctx context.Context, target string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT target, operation, correlation, status, error_message, created_at FROM mes_integration_calls WHERE 1=1`
	args := []any{}
	if target != "" {
		q += ` AND target = $1`
		args = append(args, target)
	}
	q += ` ORDER BY created_at DESC LIMIT ` + strconv.Itoa(limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var target, op, corr, status, errMsg string
		var at time.Time
		if err := rows.Scan(&target, &op, &corr, &status, &errMsg, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"target": target, "operation": op, "correlation": corr,
			"status": status, "error": errMsg, "created_at": at,
		})
	}
	return out, rows.Err()
}
