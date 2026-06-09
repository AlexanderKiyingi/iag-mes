package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Plant struct {
	ID        uuid.UUID      `json:"id"`
	Code      string         `json:"code"`
	Name      string         `json:"name"`
	Region    string         `json:"region"`
	Timezone  string         `json:"timezone"`
	Status    string         `json:"status"`
	Attrs     map[string]any `json:"attrs"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Section struct {
	ID        uuid.UUID      `json:"id"`
	PlantID   uuid.UUID      `json:"plant_id"`
	PlantCode string         `json:"plant_code,omitempty"`
	Code      string         `json:"code"`
	Name      string         `json:"name"`
	LineType  string         `json:"line_type"`
	Attrs     map[string]any `json:"attrs"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Asset struct {
	ID                uuid.UUID      `json:"id"`
	SectionID         uuid.UUID      `json:"section_id"`
	PlantCode         string         `json:"plant_code,omitempty"`
	SectionCode       string         `json:"section_code,omitempty"`
	Tag               string         `json:"tag"`
	Name              string         `json:"name"`
	Category          string         `json:"category"`
	Criticality       string         `json:"criticality"`
	Status            string         `json:"status"`
	VendorPartyID     *string        `json:"vendor_party_id,omitempty"`
	WarehouseAssetTag *string        `json:"warehouse_asset_tag,omitempty"`
	OEEPct            *float64       `json:"oee_pct,omitempty"`
	MTBFHours         *float64       `json:"mtbf_hours,omitempty"`
	SensorCount       int            `json:"sensor_count"`
	Attrs             map[string]any `json:"attrs"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

func scanAttrs(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (s *Store) ListPlants(ctx context.Context) ([]Plant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, region, timezone, status, attrs, created_at, updated_at
		FROM mes_plants ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plant
	for rows.Next() {
		var p Plant
		var attrs []byte
		if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.Region, &p.Timezone, &p.Status, &attrs, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Attrs = scanAttrs(attrs)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPlantByCode(ctx context.Context, code string) (*Plant, error) {
	var p Plant
	var attrs []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, code, name, region, timezone, status, attrs, created_at, updated_at
		FROM mes_plants WHERE code = $1`, code).Scan(
		&p.ID, &p.Code, &p.Name, &p.Region, &p.Timezone, &p.Status, &attrs, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.Attrs = scanAttrs(attrs)
	return &p, nil
}

func (s *Store) CreatePlant(ctx context.Context, p Plant) (*Plant, error) {
	var attrs []byte
	if p.Attrs != nil {
		attrs, _ = json.Marshal(p.Attrs)
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mes_plants (code, name, region, timezone, status, attrs)
		VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5,''), 'active'), COALESCE($6::jsonb, '{}'))
		RETURNING id, code, name, region, timezone, status, attrs, created_at, updated_at`,
		p.Code, p.Name, p.Region, p.Timezone, p.Status, attrs).Scan(
		&p.ID, &p.Code, &p.Name, &p.Region, &p.Timezone, &p.Status, &attrs, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.Attrs = scanAttrs(attrs)
	return &p, nil
}

func (s *Store) ListSections(ctx context.Context, plantCode string) ([]Section, error) {
	q := `
		SELECT sec.id, sec.plant_id, p.code, sec.code, sec.name, sec.line_type, sec.attrs, sec.created_at, sec.updated_at
		FROM mes_sections sec
		JOIN mes_plants p ON p.id = sec.plant_id`
	args := []any{}
	if plantCode != "" {
		q += ` WHERE p.code = $1`
		args = append(args, plantCode)
	}
	q += ` ORDER BY p.code, sec.code`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Section
	for rows.Next() {
		var sec Section
		var attrs []byte
		if err := rows.Scan(&sec.ID, &sec.PlantID, &sec.PlantCode, &sec.Code, &sec.Name, &sec.LineType, &attrs, &sec.CreatedAt, &sec.UpdatedAt); err != nil {
			return nil, err
		}
		sec.Attrs = scanAttrs(attrs)
		out = append(out, sec)
	}
	return out, rows.Err()
}

func (s *Store) CreateSection(ctx context.Context, plantCode string, sec Section) (*Section, error) {
	plant, err := s.GetPlantByCode(ctx, plantCode)
	if err != nil {
		return nil, err
	}
	var attrs []byte
	if sec.Attrs != nil {
		attrs, _ = json.Marshal(sec.Attrs)
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO mes_sections (plant_id, code, name, line_type, attrs)
		VALUES ($1, $2, $3, $4, COALESCE($5::jsonb, '{}'))
		RETURNING id, plant_id, code, name, line_type, attrs, created_at, updated_at`,
		plant.ID, sec.Code, sec.Name, sec.LineType, attrs).Scan(
		&sec.ID, &sec.PlantID, &sec.Code, &sec.Name, &sec.LineType, &attrs, &sec.CreatedAt, &sec.UpdatedAt)
	if err != nil {
		return nil, err
	}
	sec.PlantCode = plantCode
	sec.Attrs = scanAttrs(attrs)
	return &sec, nil
}

func (s *Store) ListAssets(ctx context.Context, filter AssetFilter) ([]Asset, error) {
	q := `
		SELECT a.id, a.section_id, p.code, sec.code, a.tag, a.name, a.category, a.criticality, a.status,
		       a.vendor_party_id, a.warehouse_asset_tag, a.oee_pct, a.mtbf_hours, a.sensor_count, a.attrs,
		       a.created_at, a.updated_at
		FROM mes_assets a
		JOIN mes_sections sec ON sec.id = a.section_id
		JOIN mes_plants p ON p.id = sec.plant_id
		WHERE 1=1`
	args := []any{}
	n := 1
	if filter.PlantCode != "" {
		q += fmt.Sprintf(` AND p.code = $%d`, n)
		args = append(args, filter.PlantCode)
		n++
	}
	if filter.Category != "" {
		q += fmt.Sprintf(` AND a.category = $%d`, n)
		args = append(args, filter.Category)
		n++
	}
	if filter.Status != "" {
		q += fmt.Sprintf(` AND a.status = $%d`, n)
		args = append(args, filter.Status)
		n++
	}
	q += ` ORDER BY a.tag`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAssets(rows)
}

type AssetFilter struct {
	PlantCode string
	Category  string
	Status    string
}

func (s *Store) GetAssetByTag(ctx context.Context, tag string) (*Asset, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT a.id, a.section_id, p.code, sec.code, a.tag, a.name, a.category, a.criticality, a.status,
		       a.vendor_party_id, a.warehouse_asset_tag, a.oee_pct, a.mtbf_hours, a.sensor_count, a.attrs,
		       a.created_at, a.updated_at
		FROM mes_assets a
		JOIN mes_sections sec ON sec.id = a.section_id
		JOIN mes_plants p ON p.id = sec.plant_id
		WHERE a.tag = $1`, tag)
	return scanAssetRow(row)
}

func (s *Store) CreateAsset(ctx context.Context, sectionID uuid.UUID, a Asset) (*Asset, error) {
	var attrs []byte
	if a.Attrs != nil {
		attrs, _ = json.Marshal(a.Attrs)
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mes_assets (section_id, tag, name, category, criticality, status, vendor_party_id,
		                        warehouse_asset_tag, oee_pct, mtbf_hours, sensor_count, attrs)
		VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5,''), 'C'), COALESCE(NULLIF($6,''), 'idle'),
		        $7, $8, $9, $10, COALESCE($11, 0), COALESCE($12::jsonb, '{}'))
		RETURNING id`, sectionID, a.Tag, a.Name, a.Category, a.Criticality, a.Status,
		a.VendorPartyID, a.WarehouseAssetTag, a.OEEPct, a.MTBFHours, a.SensorCount, attrs).Scan(&a.ID)
	if err != nil {
		return nil, err
	}
	return s.GetAssetByTag(ctx, a.Tag)
}

func (s *Store) PatchAsset(ctx context.Context, tag string, patch AssetPatch) (*Asset, error) {
	cur, err := s.GetAssetByTag(ctx, tag)
	if err != nil {
		return nil, err
	}
	if patch.Name != nil {
		cur.Name = *patch.Name
	}
	if patch.Status != nil {
		cur.Status = *patch.Status
	}
	if patch.OEEPct != nil {
		cur.OEEPct = patch.OEEPct
	}
	if patch.MTBFHours != nil {
		cur.MTBFHours = patch.MTBFHours
	}
	if patch.Attrs != nil {
		cur.Attrs = patch.Attrs
	}
	attrs, _ := json.Marshal(cur.Attrs)
	_, err = s.pool.Exec(ctx, `
		UPDATE mes_assets SET name=$2, status=$3, oee_pct=$4, mtbf_hours=$5, attrs=$6::jsonb, updated_at=NOW()
		WHERE tag=$1`, tag, cur.Name, cur.Status, cur.OEEPct, cur.MTBFHours, attrs)
	if err != nil {
		return nil, err
	}
	return s.GetAssetByTag(ctx, tag)
}

type AssetPatch struct {
	Name      *string
	Status    *string
	OEEPct    *float64
	MTBFHours *float64
	Attrs     map[string]any
}

type assetScanner interface {
	Scan(dest ...any) error
}

func scanAssetRow(row assetScanner) (*Asset, error) {
	var a Asset
	var attrs []byte
	err := row.Scan(&a.ID, &a.SectionID, &a.PlantCode, &a.SectionCode, &a.Tag, &a.Name, &a.Category,
		&a.Criticality, &a.Status, &a.VendorPartyID, &a.WarehouseAssetTag, &a.OEEPct, &a.MTBFHours,
		&a.SensorCount, &attrs, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.Attrs = scanAttrs(attrs)
	return &a, nil
}

func scanAssets(rows pgx.Rows) ([]Asset, error) {
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		var a Asset
		var attrs []byte
		if err := rows.Scan(&a.ID, &a.SectionID, &a.PlantCode, &a.SectionCode, &a.Tag, &a.Name, &a.Category,
			&a.Criticality, &a.Status, &a.VendorPartyID, &a.WarehouseAssetTag, &a.OEEPct, &a.MTBFHours,
			&a.SensorCount, &attrs, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.Attrs = scanAttrs(attrs)
		out = append(out, a)
	}
	return out, rows.Err()
}
