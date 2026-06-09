package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-mes/backend/internal/events"
)

type ProductionRun struct {
	ID              uuid.UUID      `json:"id"`
	BusinessID      string         `json:"business_id"`
	BatchBusinessID string         `json:"batch_business_id"`
	Process         string         `json:"process"`
	Stage           string         `json:"stage"`
	StageIdx        int            `json:"stage_idx"`
	AssetTag        *string        `json:"asset_tag,omitempty"`
	OperatorRef     *string        `json:"operator_ref,omitempty"`
	Status          string         `json:"status"`
	KgIn            *float64       `json:"kg_in,omitempty"`
	KgOut           *float64       `json:"kg_out,omitempty"`
	Facility        *string        `json:"facility,omitempty"`
	Moisture        *float64       `json:"moisture,omitempty"`
	BedID           *string        `json:"bed_id,omitempty"`
	GradePrelim     *string        `json:"grade_prelim,omitempty"`
	StartedAt       time.Time      `json:"started_at"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
	Attrs           map[string]any `json:"attrs"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type CCPReading struct {
	ID          uuid.UUID `json:"id"`
	RunID       uuid.UUID `json:"run_id"`
	CCPCode     string    `json:"ccp_code"`
	Value       string    `json:"value"`
	Target      string    `json:"target"`
	Pass        bool      `json:"pass"`
	OperatorRef *string   `json:"operator_ref,omitempty"`
	RecordedAt  time.Time `json:"recorded_at"`
}

type CreateRunInput struct {
	BusinessID      string
	BatchBusinessID string
	Process         string
	Stage           string
	StageIdx        int
	AssetTag        string
	OperatorRef     string
	KgIn            float64
	Facility        string
	Moisture        float64
	BedID           string
	GradePrelim     string
}

func (s *Store) ListProductionRuns(ctx context.Context, status string, limit int) ([]ProductionRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `
		SELECT id, business_id, batch_business_id, process, stage, stage_idx, asset_tag, operator_ref,
		       status, kg_in, kg_out, facility, moisture, bed_id, grade_prelim, started_at, completed_at,
		       attrs, created_at, updated_at
		FROM mes_production_runs WHERE 1=1`
	args := []any{}
	if status != "" {
		q += ` AND status = $1`
		args = append(args, status)
	}
	q += fmt.Sprintf(` ORDER BY started_at DESC LIMIT %d`, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

func (s *Store) GetProductionRun(ctx context.Context, id uuid.UUID) (*ProductionRun, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, business_id, batch_business_id, process, stage, stage_idx, asset_tag, operator_ref,
		       status, kg_in, kg_out, facility, moisture, bed_id, grade_prelim, started_at, completed_at,
		       attrs, created_at, updated_at
		FROM mes_production_runs WHERE id = $1`, id)
	return scanRunRow(row)
}

func (s *Store) CreateProductionRun(ctx context.Context, in CreateRunInput) (*ProductionRun, error) {
	if in.BusinessID == "" {
		in.BusinessID = "RUN-" + strings.ToUpper(uuid.NewString()[:8])
	}
	process := normalizeProcess(in.Process)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var run ProductionRun
	var attrs []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO mes_production_runs (business_id, batch_business_id, process, stage, stage_idx,
			asset_tag, operator_ref, status, kg_in, facility, moisture, bed_id, grade_prelim)
		VALUES ($1,$2,$3,COALESCE(NULLIF($4,''), 'start'),COALESCE($5,0),
			NULLIF($6,''),NULLIF($7,''),'running',NULLIF($8,0),NULLIF($9,''),
			NULLIF($10,0),NULLIF($11,''),NULLIF($12,''))
		RETURNING id, business_id, batch_business_id, process, stage, stage_idx, asset_tag, operator_ref,
		          status, kg_in, kg_out, facility, moisture, bed_id, grade_prelim, started_at, completed_at,
		          attrs, created_at, updated_at`,
		in.BusinessID, in.BatchBusinessID, process, in.Stage, in.StageIdx,
		in.AssetTag, in.OperatorRef, in.KgIn, in.Facility, in.Moisture, in.BedID, in.GradePrelim).Scan(
		&run.ID, &run.BusinessID, &run.BatchBusinessID, &run.Process, &run.Stage, &run.StageIdx,
		&run.AssetTag, &run.OperatorRef, &run.Status, &run.KgIn, &run.KgOut, &run.Facility,
		&run.Moisture, &run.BedID, &run.GradePrelim, &run.StartedAt, &run.CompletedAt,
		&attrs, &run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		return nil, err
	}
	run.Attrs = scanAttrs(attrs)

	eventType := startEventForProcess(process)
	data := runEventData(&run)
	if s.bus != nil {
		if err := s.bus.PublishTx(ctx, tx, eventType, data, in.BatchBusinessID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &run, nil
}

type AdvanceRunInput struct {
	ToStage     string
	StageIdx    int
	OperatorRef string
	KgOut       float64
	Moisture    float64
}

func (s *Store) AdvanceProductionRun(ctx context.Context, id uuid.UUID, in AdvanceRunInput) (*ProductionRun, error) {
	run, err := s.GetProductionRun(ctx, id)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	fromStage := run.Stage
	_, err = tx.Exec(ctx, `
		UPDATE mes_production_runs
		SET stage=$2, stage_idx=$3, operator_ref=COALESCE(NULLIF($4,''), operator_ref),
		    kg_out=COALESCE(NULLIF($5,0), kg_out), moisture=COALESCE(NULLIF($6,0), moisture),
		    updated_at=NOW()
		WHERE id=$1`, id, in.ToStage, in.StageIdx, in.OperatorRef, in.KgOut, in.Moisture)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO mes_stage_events (run_id, from_stage, to_stage, stage_idx, operator_ref)
		VALUES ($1,$2,$3,$4,NULLIF($5,''))`, id, fromStage, in.ToStage, in.StageIdx, in.OperatorRef)
	if err != nil {
		return nil, err
	}
	updated, err := s.getRunTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	data := runEventData(updated)
	data["from_stage"] = fromStage
	data["to_stage"] = in.ToStage
	if s.bus != nil {
		if err := s.bus.PublishTx(ctx, tx, events.TypeStageAdvanced, data, updated.BatchBusinessID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

type CompleteRunInput struct {
	KgOut       float64
	Moisture    float64
	GradePrelim string
	OperatorRef string
}

func (s *Store) CompleteProductionRun(ctx context.Context, id uuid.UUID, in CompleteRunInput) (*ProductionRun, error) {
	run, err := s.GetProductionRun(ctx, id)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		UPDATE mes_production_runs
		SET status='completed', completed_at=$2,
		    kg_out=COALESCE(NULLIF($3,0), kg_out), moisture=COALESCE(NULLIF($4,0), moisture),
		    grade_prelim=COALESCE(NULLIF($5,''), grade_prelim),
		    operator_ref=COALESCE(NULLIF($6,''), operator_ref), updated_at=NOW()
		WHERE id=$1`, id, now, in.KgOut, in.Moisture, in.GradePrelim, in.OperatorRef)
	if err != nil {
		return nil, err
	}
	updated, err := s.getRunTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	data := runEventData(updated)
	eventType := completeEventForProcess(run.Process)
	if s.bus != nil {
		if err := s.bus.PublishTx(ctx, tx, eventType, data, updated.BatchBusinessID); err != nil {
			return nil, err
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeRunCompleted, data, updated.BatchBusinessID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Store) AddCCPReading(ctx context.Context, runID uuid.UUID, r CCPReading) (*CCPReading, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	run, err := s.getRunTx(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO mes_ccp_readings (run_id, ccp_code, value, target, pass, operator_ref)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''))
		RETURNING id, run_id, ccp_code, value, target, pass, operator_ref, recorded_at`,
		runID, r.CCPCode, r.Value, r.Target, r.Pass, r.OperatorRef).Scan(
		&r.ID, &r.RunID, &r.CCPCode, &r.Value, &r.Target, &r.Pass, &r.OperatorRef, &r.RecordedAt)
	if err != nil {
		return nil, err
	}
	if s.bus != nil {
		data := map[string]any{
			"batch_business_id": run.BatchBusinessID,
			"run_id":            runID.String(),
			"ccp_code":          r.CCPCode,
			"value":             r.Value,
			"target":            r.Target,
			"pass":              r.Pass,
			"timestamp":         r.RecordedAt.UTC().Format(time.RFC3339),
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeCCPRecorded, data, run.BatchBusinessID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) ListCCPReadings(ctx context.Context, runID uuid.UUID) ([]CCPReading, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, ccp_code, value, target, pass, operator_ref, recorded_at
		FROM mes_ccp_readings WHERE run_id = $1 ORDER BY recorded_at DESC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CCPReading
	for rows.Next() {
		var r CCPReading
		if err := rows.Scan(&r.ID, &r.RunID, &r.CCPCode, &r.Value, &r.Target, &r.Pass, &r.OperatorRef, &r.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpsertBatchRef(ctx context.Context, batchBusinessID, source string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mes_batch_refs (batch_business_id, source, validated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (batch_business_id) DO UPDATE SET validated_at = NOW(), source = EXCLUDED.source`,
		batchBusinessID, source)
	return err
}

func (s *Store) getRunTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*ProductionRun, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, business_id, batch_business_id, process, stage, stage_idx, asset_tag, operator_ref,
		       status, kg_in, kg_out, facility, moisture, bed_id, grade_prelim, started_at, completed_at,
		       attrs, created_at, updated_at
		FROM mes_production_runs WHERE id = $1`, id)
	return scanRunRow(row)
}

func scanRunRow(row pgx.Row) (*ProductionRun, error) {
	var r ProductionRun
	var attrs []byte
	err := row.Scan(&r.ID, &r.BusinessID, &r.BatchBusinessID, &r.Process, &r.Stage, &r.StageIdx,
		&r.AssetTag, &r.OperatorRef, &r.Status, &r.KgIn, &r.KgOut, &r.Facility, &r.Moisture,
		&r.BedID, &r.GradePrelim, &r.StartedAt, &r.CompletedAt, &attrs, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	r.Attrs = scanAttrs(attrs)
	return &r, nil
}

func scanRuns(rows pgx.Rows) ([]ProductionRun, error) {
	var out []ProductionRun
	for rows.Next() {
		var r ProductionRun
		var attrs []byte
		if err := rows.Scan(&r.ID, &r.BusinessID, &r.BatchBusinessID, &r.Process, &r.Stage, &r.StageIdx,
			&r.AssetTag, &r.OperatorRef, &r.Status, &r.KgIn, &r.KgOut, &r.Facility, &r.Moisture,
			&r.BedID, &r.GradePrelim, &r.StartedAt, &r.CompletedAt, &attrs, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Attrs = scanAttrs(attrs)
		out = append(out, r)
	}
	return out, rows.Err()
}

func runEventData(r *ProductionRun) map[string]any {
	data := map[string]any{
		"batch_business_id": r.BatchBusinessID,
		"run_id":            r.ID.String(),
		"business_id":       r.BusinessID,
		"process":           r.Process,
		"stage":             r.Stage,
		"timestamp":         time.Now().UTC().Format(time.RFC3339),
	}
	if r.AssetTag != nil {
		data["asset_tag"] = *r.AssetTag
	}
	if r.Facility != nil {
		data["facility"] = *r.Facility
	}
	if r.KgIn != nil {
		data["kg_in"] = *r.KgIn
	}
	if r.KgOut != nil {
		data["kg_out"] = *r.KgOut
	}
	if r.Moisture != nil {
		data["moisture"] = *r.Moisture
	}
	if r.BedID != nil {
		data["bed_id"] = *r.BedID
	}
	if r.GradePrelim != nil {
		data["grade_prelim"] = *r.GradePrelim
	}
	return data
}

func normalizeProcess(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "wet_mill", "wetmill":
		return "wetmill"
	case "dry", "drying":
		return "drying"
	case "dry_mill", "drymill":
		return "drymill"
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

func startEventForProcess(process string) string {
	switch process {
	case "wetmill", "wet":
		return events.TypeWetmillStarted
	case "drying", "dry":
		return events.TypeDryingStarted
	case "roast":
		return events.TypeRoastStarted
	default:
		return events.TypeStageAdvanced
	}
}

func completeEventForProcess(process string) string {
	switch process {
	case "wetmill", "wet":
		return events.TypeWetmillCompleted
	case "drying", "dry":
		return events.TypeDryingCompleted
	case "drymill":
		return events.TypeDrymillCompleted
	case "roast":
		return events.TypeRoastCompleted
	default:
		return events.TypeRunCompleted
	}
}

// MapStageEvent maps legacy production-order API stage/action to Kafka event type.
func MapStageEvent(stage, action string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		action = "completed"
	}
	switch stage {
	case "wetmill", "wet_mill":
		if action == "started" || action == "start" {
			return events.TypeWetmillStarted
		}
		return events.TypeWetmillCompleted
	case "drying", "dry":
		if action == "started" || action == "start" {
			return events.TypeDryingStarted
		}
		return events.TypeDryingCompleted
	case "drymill", "dry_mill":
		return events.TypeDrymillCompleted
	case "roast":
		if action == "started" || action == "start" {
			return events.TypeRoastStarted
		}
		return events.TypeRoastCompleted
	default:
		return ""
	}
}
