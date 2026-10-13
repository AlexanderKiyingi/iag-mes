package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iag-mes/backend/internal/events"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Plant struct {
	ID       uuid.UUID `json:"id"`
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Region   string    `json:"region"`
	Timezone string    `json:"timezone"`
	Status   string    `json:"status"`
	// Where the factory is (014). gps_lat/gps_lng match the supply-chain
	// service's convention so factories and the farms supplying them plot on
	// one map.
	Address   string         `json:"address"`
	City      string         `json:"city"`
	District  string         `json:"district"`
	Country   string         `json:"country"`
	GPSLat    *float64       `json:"gps_lat,omitempty"`
	GPSLng    *float64       `json:"gps_lng,omitempty"`
	Attrs     map[string]any `json:"attrs"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// plantColumns is every column a plant read returns, in scan order.
const plantColumns = `id, code, name, region, timezone, status,
	      address, city, district, country, gps_lat, gps_lng, attrs, created_at, updated_at`

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
	PurchasedOn       *time.Time     `json:"purchased_on,omitempty"`
	Location          string         `json:"location"`
	Capacity          *float64       `json:"capacity,omitempty"`
	CapacityUnit      string         `json:"capacity_unit"`
	PlantID           *uuid.UUID     `json:"plant_id,omitempty"`
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

// UpdatePlant corrects a factory in place. Everything is optional: a nil
// field is left as it is, so moving a pin on a map does not blank an address.
//
// There was no edit path at all before 014 — CreatePlant is a plain INSERT and
// code is UNIQUE, so the three factories that already existed could never have
// been given a location.
type PlantPatch struct {
	Name     *string  `json:"name"`
	Region   *string  `json:"region"`
	Timezone *string  `json:"timezone"`
	Status   *string  `json:"status"`
	Address  *string  `json:"address"`
	City     *string  `json:"city"`
	District *string  `json:"district"`
	Country  *string  `json:"country"`
	GPSLat   *float64 `json:"gps_lat"`
	GPSLng   *float64 `json:"gps_lng"`
	// LabourRates sets the hourly rate maintenance labour is costed at, per
	// technician role ("*" is the rate for anyone whose role has none). Keys
	// are merged into attrs.labour_rates: a role not named is left alone, and
	// a null rate removes that role. Rates are configuration, not pay — the
	// salary itself stays in HR.
	LabourRates map[string]*float64 `json:"labour_rates"`
	// LabourCurrency is the currency those rates are in (ISO 4217).
	LabourCurrency *string `json:"labour_currency"`
	// ApprovalThreshold is the estimated cost at or above which a new work
	// order at this factory waits for approval before it can start. Zero
	// turns approval off.
	ApprovalThreshold *float64 `json:"approval_threshold"`
}

// MaxLabourRate bounds an hourly rate. It is generous for UGX (the platform's
// base currency) and exists to catch a monthly salary typed into an hourly box.
const MaxLabourRate = 10_000_000

// ValidateLabourRates refuses what the cost report could not use: a blank or
// overlong role, a negative or absurd rate, or a currency that is not a code.
func (in PlantPatch) ValidateLabourRates() error {
	for role, rate := range in.LabourRates {
		r := strings.TrimSpace(role)
		if r == "" || len(r) > 80 {
			return fmt.Errorf("%w: a labour rate needs a role of 1–80 characters (\"*\" for everyone else)", ErrBadInput)
		}
		if rate != nil && (*rate < 0 || *rate > MaxLabourRate) {
			return fmt.Errorf("%w: the hourly rate for %q must be between 0 and %d", ErrBadInput, r, MaxLabourRate)
		}
	}
	if in.LabourCurrency != nil {
		c := strings.TrimSpace(*in.LabourCurrency)
		if len(c) != 3 || strings.ToUpper(c) != c {
			return fmt.Errorf("%w: labour_currency must be a three-letter code such as UGX", ErrBadInput)
		}
	}
	if in.ApprovalThreshold != nil && (*in.ApprovalThreshold < 0 || math.IsNaN(*in.ApprovalThreshold)) {
		return fmt.Errorf("%w: approval_threshold is zero (off) or more", ErrBadInput)
	}
	return nil
}

func (s *Store) UpdatePlant(ctx context.Context, code string, in PlantPatch) (*Plant, error) {
	if err := in.ValidateLabourRates(); err != nil {
		return nil, err
	}
	// Merged into attrs in SQL rather than read-modify-write here: the
	// production.shifts.changed consumer writes attrs.production_shifts on the
	// same row, and a read-modify-write would race it.
	var rates []byte
	if len(in.LabourRates) > 0 {
		clean := make(map[string]*float64, len(in.LabourRates))
		for role, rate := range in.LabourRates {
			clean[strings.TrimSpace(role)] = rate
		}
		rates, _ = json.Marshal(clean)
	}
	// Top-level attrs keys this patch sets, merged in SQL like the rates.
	top := map[string]any{}
	if in.LabourCurrency != nil {
		top["labour_currency"] = strings.TrimSpace(*in.LabourCurrency)
	}
	if in.ApprovalThreshold != nil {
		top["approval_threshold"] = *in.ApprovalThreshold
	}
	var currency []byte
	if len(top) > 0 {
		currency, _ = json.Marshal(top)
	}
	var p Plant
	var attrs []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE mes_plants SET
		  name     = COALESCE($2, name),
		  region   = COALESCE($3, region),
		  timezone = COALESCE($4, timezone),
		  status   = COALESCE($5, status),
		  address  = COALESCE($6, address),
		  city     = COALESCE($7, city),
		  district = COALESCE($8, district),
		  country  = COALESCE($9, country),
		  -- The pair moves together or not at all; the CHECK refuses half of one.
		  gps_lat  = COALESCE($10, gps_lat),
		  gps_lng  = COALESCE($11, gps_lng),
		  attrs = (CASE WHEN $12::jsonb IS NULL THEN COALESCE(attrs, '{}'::jsonb) ELSE jsonb_set(
		            COALESCE(attrs, '{}'::jsonb), '{labour_rates}',
		            jsonb_strip_nulls(COALESCE(attrs->'labour_rates', '{}'::jsonb) || $12::jsonb)) END)
		          || COALESCE($13::jsonb, '{}'::jsonb),
		  updated_at = NOW()
		 WHERE code = $1
		RETURNING `+plantColumns,
		code, in.Name, in.Region, in.Timezone, in.Status,
		in.Address, in.City, in.District, in.Country, in.GPSLat, in.GPSLng, rates, currency).Scan(
		&p.ID, &p.Code, &p.Name, &p.Region, &p.Timezone, &p.Status,
		&p.Address, &p.City, &p.District, &p.Country, &p.GPSLat, &p.GPSLng,
		&attrs, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.Attrs = scanAttrs(attrs)
	return &p, nil
}

func (s *Store) ListPlants(ctx context.Context) ([]Plant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+plantColumns+`
		FROM mes_plants ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plant
	for rows.Next() {
		var p Plant
		var attrs []byte
		if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.Region, &p.Timezone, &p.Status,
			&p.Address, &p.City, &p.District, &p.Country, &p.GPSLat, &p.GPSLng,
			&attrs, &p.CreatedAt, &p.UpdatedAt); err != nil {
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
		SELECT `+plantColumns+`
		FROM mes_plants WHERE code = $1`, code).Scan(
		&p.ID, &p.Code, &p.Name, &p.Region, &p.Timezone, &p.Status,
		&p.Address, &p.City, &p.District, &p.Country, &p.GPSLat, &p.GPSLng,
		&attrs, &p.CreatedAt, &p.UpdatedAt)
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
		INSERT INTO mes_plants (code, name, region, timezone, status,
		                        address, city, district, country, gps_lat, gps_lng, attrs)
		VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5,''), 'active'),
		        $6, $7, $8, COALESCE(NULLIF($9,''), 'UG'), $10, $11, COALESCE($12::jsonb, '{}'))
		RETURNING `+plantColumns,
		p.Code, p.Name, p.Region, p.Timezone, p.Status,
		p.Address, p.City, p.District, p.Country, p.GPSLat, p.GPSLng, attrs).Scan(
		&p.ID, &p.Code, &p.Name, &p.Region, &p.Timezone, &p.Status,
		&p.Address, &p.City, &p.District, &p.Country, &p.GPSLat, &p.GPSLng,
		&attrs, &p.CreatedAt, &p.UpdatedAt)
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

// SectionPatch corrects a shop floor in place. Every field is optional, so
// renaming a floor does not reset the line type it was set up with.
//
// There was no edit path at all: CreateSection is a plain INSERT, so a floor
// typed in wrongly — or one whose line changed — could only be left as it was.
// The frontend reports this honestly rather than hiding it (the records route
// sends `capabilities.update`, and the screen drops Edit when it is false), so
// the gap showed as a Shop Floors register you could add to and never correct.
//
// `code` is not patchable: it is half the natural key, assets hang off the
// section by id, and production projects the code onto prod_machines.section.
// Renaming it would silently orphan those references, so a floor that needs a
// different code is a new floor and a move of its machines.
type SectionPatch struct {
	Name     *string        `json:"name"`
	LineType *string        `json:"line_type"`
	Attrs    map[string]any `json:"attrs"`
}

// UpdateSection patches one shop floor of one factory.
//
// Scoped by plant as well as code because section codes are unique per plant
// only (mes_sections UNIQUE (plant_id, code)) — "wet" at one factory and "wet"
// at another are different floors, and a patch that matched on code alone
// would edit whichever came first.
func (s *Store) UpdateSection(ctx context.Context, plantCode, sectionCode string, in SectionPatch) (*Section, error) {
	var attrs []byte
	if in.Attrs != nil {
		attrs, _ = json.Marshal(in.Attrs)
	}
	var sec Section
	var stored []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE mes_sections sec SET
		  name      = COALESCE($3, sec.name),
		  line_type = COALESCE($4, sec.line_type),
		  attrs     = COALESCE($5::jsonb, sec.attrs),
		  updated_at = NOW()
		 FROM mes_plants p
		 WHERE sec.plant_id = p.id AND p.code = $1 AND sec.code = $2
		RETURNING sec.id, sec.plant_id, p.code, sec.code, sec.name, sec.line_type,
		          sec.attrs, sec.created_at, sec.updated_at`,
		plantCode, sectionCode, in.Name, in.LineType, attrs).Scan(
		&sec.ID, &sec.PlantID, &sec.PlantCode, &sec.Code, &sec.Name, &sec.LineType,
		&stored, &sec.CreatedAt, &sec.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	sec.Attrs = scanAttrs(stored)
	return &sec, nil
}

func (s *Store) ListAssets(ctx context.Context, filter AssetFilter) ([]Asset, error) {
	q := `
		SELECT a.id, a.section_id, p.code, sec.code, a.tag, a.name, a.category, a.criticality, a.status,
		       a.vendor_party_id, a.warehouse_asset_tag, a.oee_pct, a.mtbf_hours, a.sensor_count,
		       a.purchased_on, a.location, a.capacity, a.capacity_unit, a.plant_id, a.attrs,
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
		       a.vendor_party_id, a.warehouse_asset_tag, a.oee_pct, a.mtbf_hours, a.sensor_count,
		       a.purchased_on, a.location, a.capacity, a.capacity_unit, a.plant_id, a.attrs,
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
		                        warehouse_asset_tag, oee_pct, mtbf_hours, sensor_count, attrs,
		                        purchased_on, location, capacity, capacity_unit, plant_id)
		VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5,''), 'C'), COALESCE(NULLIF($6,''), 'idle'),
		        $7, $8, $9, $10, COALESCE($11, 0), COALESCE($12::jsonb, '{}'),
		        $13, COALESCE($14, ''), $15, COALESCE($16, ''),
		        COALESCE($17, (SELECT plant_id FROM mes_sections WHERE id = $1)))
		RETURNING id`, sectionID, a.Tag, a.Name, a.Category, a.Criticality, a.Status,
		a.VendorPartyID, a.WarehouseAssetTag, a.OEEPct, a.MTBFHours, a.SensorCount, attrs,
		a.PurchasedOn, a.Location, a.Capacity, a.CapacityUnit, a.PlantID).Scan(&a.ID)
	if err != nil {
		return nil, err
	}
	created, err := s.GetAssetByTag(ctx, a.Tag)
	if err != nil {
		return nil, err
	}
	s.publishAsset(ctx, events.TypeAssetCreated, created)
	return created, nil
}

// publishAsset tells production about a registry change. The payload is
// what prod_machines needs: tag, name, category, status, plant, section,
// capacity — see production's ProjectMESAsset.
func (s *Store) publishAsset(ctx context.Context, eventType string, a *Asset) {
	if s.bus == nil || a == nil {
		return
	}
	data := map[string]any{
		"tag":           a.Tag,
		"name":          a.Name,
		"category":      a.Category,
		"status":        a.Status,
		"plant_code":    a.PlantCode,
		"section_code":  a.SectionCode,
		"capacity_unit": a.CapacityUnit,
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
	}
	if a.Capacity != nil {
		data["capacity"] = *a.Capacity
	}
	s.bus.Publish(ctx, eventType, data, a.Tag)
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
	if patch.PurchasedOn != nil {
		cur.PurchasedOn = patch.PurchasedOn
	}
	if patch.Location != nil {
		cur.Location = *patch.Location
	}
	if patch.Capacity != nil {
		cur.Capacity = patch.Capacity
	}
	if patch.CapacityUnit != nil {
		cur.CapacityUnit = *patch.CapacityUnit
	}
	if patch.PlantID != nil {
		cur.PlantID = patch.PlantID
	}
	if patch.SectionID != nil && *patch.SectionID != cur.SectionID {
		var plantID uuid.UUID
		err := s.pool.QueryRow(ctx, `SELECT plant_id FROM mes_sections WHERE id = $1`, *patch.SectionID).Scan(&plantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: no shop floor with id %s", ErrBadInput, *patch.SectionID)
		}
		if err != nil {
			return nil, err
		}
		if patch.PlantID != nil && *patch.PlantID != plantID {
			return nil, fmt.Errorf("%w: plant_id disagrees with the shop floor's factory; send section_id alone", ErrBadInput)
		}
		cur.SectionID = *patch.SectionID
		cur.PlantID = &plantID
	}
	attrs, _ := json.Marshal(cur.Attrs)
	_, err = s.pool.Exec(ctx, `
		UPDATE mes_assets SET name=$2, status=$3, oee_pct=$4, mtbf_hours=$5, attrs=$6::jsonb,
		    purchased_on=$7, location=$8, capacity=$9, capacity_unit=$10, plant_id=$11, section_id=$12, updated_at=NOW()
		WHERE tag=$1`, tag, cur.Name, cur.Status, cur.OEEPct, cur.MTBFHours, attrs,
		cur.PurchasedOn, cur.Location, cur.Capacity, cur.CapacityUnit, cur.PlantID, cur.SectionID)
	if err != nil {
		return nil, err
	}
	updated, err := s.GetAssetByTag(ctx, tag)
	if err != nil {
		return nil, err
	}
	s.publishAsset(ctx, events.TypeAssetUpdated, updated)
	return updated, nil
}

type AssetPatch struct {
	Name         *string        `json:"name"`
	Status       *string        `json:"status"`
	OEEPct       *float64       `json:"oee_pct"`
	MTBFHours    *float64       `json:"mtbf_hours"`
	Attrs        map[string]any `json:"attrs"`
	PurchasedOn  *time.Time     `json:"purchased_on"`
	Location     *string        `json:"location"`
	Capacity     *float64       `json:"capacity"`
	CapacityUnit *string        `json:"capacity_unit"`
	PlantID      *uuid.UUID     `json:"plant_id"`
	// SectionID moves the machine to another shop floor. Its factory follows:
	// reads derive plant_code through the section, and plant_id is the
	// denormalised copy, so the two are written together here.
	SectionID *uuid.UUID `json:"section_id"`
}

type assetScanner interface {
	Scan(dest ...any) error
}

func scanAssetRow(row assetScanner) (*Asset, error) {
	var a Asset
	var attrs []byte
	err := row.Scan(&a.ID, &a.SectionID, &a.PlantCode, &a.SectionCode, &a.Tag, &a.Name, &a.Category,
		&a.Criticality, &a.Status, &a.VendorPartyID, &a.WarehouseAssetTag, &a.OEEPct, &a.MTBFHours,
		&a.SensorCount, &a.PurchasedOn, &a.Location, &a.Capacity, &a.CapacityUnit, &a.PlantID, &attrs, &a.CreatedAt, &a.UpdatedAt)
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
			&a.SensorCount, &a.PurchasedOn, &a.Location, &a.Capacity, &a.CapacityUnit, &a.PlantID, &attrs, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.Attrs = scanAttrs(attrs)
		out = append(out, a)
	}
	return out, rows.Err()
}

// assetHistoryTables are every table that names a machine by `asset_tag`.
//
// Exactly one of them declares a foreign key (mes_iot_devices, ON DELETE SET
// NULL); the other fourteen hold a bare TEXT column. Postgres would therefore
// let a machine be deleted out from under its own history and leave every one
// of these rows pointing at a tag that no longer exists, so the guard has to be
// here rather than in the schema.
var assetHistoryTables = []struct{ table, label string }{
	{"mes_work_orders", "work orders"},
	{"mes_downtime_events", "downtime events"},
	{"mes_production_runs", "production runs"},
	{"mes_production_orders", "production orders"},
	{"mes_schedule_blocks", "schedule blocks"},
	{"mes_pm_schedules", "maintenance schedules"},
	{"mes_operator_assignments", "operator assignments"},
	{"mes_kpi_snapshots", "KPI snapshots"},
	{"mes_machine_telemetry", "telemetry"},
	{"mes_telemetry_timeseries", "telemetry"},
	{"mes_asset_telemetry_latest", "telemetry"},
	{"mes_machine_oee_daily", "daily OEE"},
	{"mes_energy_readings", "energy readings"},
	{"mes_iot_devices", "IoT devices"},
	{"mes_ai_recommendations", "recommendations"},
}

// DeleteAsset removes a machine that has no history.
//
// Deletion is for a machine that should never have existed -- a mistyped tag, a
// row a probe left behind. A machine that has actually run is retired instead
// (status `retired`, migration 012), because deleting it would silently orphan
// the work orders, downtime and telemetry that refer to it by tag.
//
// The references are counted before the DELETE and named in the refusal, so a
// caller is told what is holding the machine rather than being told no.
func (s *Store) DeleteAsset(ctx context.Context, tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ErrBadInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Lock the row so a concurrent write cannot attach history between the
	// count below and the DELETE.
	var id uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM mes_assets WHERE tag = $1 FOR UPDATE`, tag).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	// Which of those tables this deployment actually has, asked once and up
	// front. Querying a missing table inside the transaction aborts it, and
	// every statement after that fails with 25P02 -- so the absent ones have to
	// be filtered out before the counting starts, not recovered from after.
	names := make([]string, 0, len(assetHistoryTables))
	for _, t := range assetHistoryTables {
		names = append(names, t.table)
	}
	present := map[string]bool{}
	rows, err := tx.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = ANY($1)`, names)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		present[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	seen := map[string]bool{}
	var holding []string
	for _, t := range assetHistoryTables {
		if !present[t.table] {
			continue
		}
		var n int
		// The table name is from the constant list above, never caller input.
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*)::int FROM `+t.table+` WHERE asset_tag = $1`, tag).Scan(&n); err != nil {
			return err
		}
		if n > 0 && !seen[t.label] {
			seen[t.label] = true
			holding = append(holding, t.label)
		}
	}
	if len(holding) > 0 {
		return fmt.Errorf("machine %s still has %s — retire it instead of deleting it: %w",
			tag, strings.Join(holding, ", "), ErrConflict)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM mes_assets WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
