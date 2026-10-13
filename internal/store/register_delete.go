package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type tableRef struct{ table, column, label string }

// registerDelete describes removing one row from a reference register.
type registerDelete struct {
	table  string // the register itself
	keyCol string // the column the key below matches
	key    string // the value to delete
	noun   string // what to call it in a refusal
	hint   string // what to do instead when it is in use
	refs   []tableRef
}

// deleteRegisterRow removes a register entry that nothing refers to.
//
// Some of these references are real foreign keys (mes_sections.plant_id,
// mes_assets.section_id) and some are bare TEXT codes that declare nothing
// (mes_technicians.plant_code and friends). The database stops the first kind
// and is indifferent to the second, so counting every reference here is what
// makes the two behave alike — and what lets the refusal say which rows are in
// the way instead of surfacing a foreign-key error.
//
// Which referencing tables a deployment actually holds is resolved once, up
// front. Querying a missing table inside the transaction aborts it and every
// statement after fails with 25P02, and migration 010 drops several of these
// tables when they are empty, so their presence varies between deployments.
func (s *Store) deleteRegisterRow(ctx context.Context, d registerDelete) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var exists bool
	err = tx.QueryRow(ctx,
		`SELECT true FROM `+d.table+` WHERE `+d.keyCol+` = $1 FOR UPDATE`, d.key).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	names := make([]string, 0, len(d.refs))
	for _, r := range d.refs {
		names = append(names, r.table)
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
	for _, r := range d.refs {
		if !present[r.table] {
			continue
		}
		var n int
		// Table and column come from the constant lists below, never from input.
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*)::int FROM `+r.table+` WHERE `+r.column+` = $1`, d.key).Scan(&n); err != nil {
			return err
		}
		if n > 0 && !seen[r.label] {
			seen[r.label] = true
			holding = append(holding, r.label)
		}
	}
	if len(holding) > 0 {
		return fmt.Errorf("%s %s still has %s — %s: %w",
			d.noun, d.key, strings.Join(holding, ", "), d.hint, ErrConflict)
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM `+d.table+` WHERE `+d.keyCol+` = $1`, d.key); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Tables that quote a factory by its code and declare nothing about it. Shop
// floors and shift patterns are not here: they hang off plant_id, so they are
// counted by join in DeletePlant.
var plantCodeReferences = []tableRef{
	{"mes_technicians", "plant_code", "technicians"},
	{"mes_shift_logs", "plant_code", "shift logs"},
	{"mes_kpi_snapshots", "plant_code", "KPI snapshots"},
	{"mes_energy_readings", "plant_code", "energy readings"},
}

// DeletePlant removes a factory that has nothing in it.
//
// A factory with shop floors, shift patterns or recorded history is refused and
// set inactive instead: its code is stamped on logs, snapshots and readings
// that would otherwise name a factory that no longer exists.
func (s *Store) DeletePlant(ctx context.Context, code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return ErrBadInput
	}
	// Shop floors and shift patterns hang off plant_id, so they are counted by
	// join rather than by code.
	var inner int
	err := s.pool.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM mes_sections sec JOIN mes_plants p ON p.id = sec.plant_id WHERE p.code = $1)
		     + (SELECT COUNT(*) FROM mes_shift_definitions sd JOIN mes_plants p ON p.id = sd.plant_id WHERE p.code = $1)`,
		code).Scan(&inner)
	if err != nil {
		return err
	}
	if inner > 0 {
		return fmt.Errorf("factory %s still has shop floors or shift patterns — empty it first, or set it inactive: %w",
			code, ErrConflict)
	}
	return s.deleteRegisterRow(ctx, registerDelete{
		table: "mes_plants", keyCol: "code", key: code,
		noun: "factory", hint: "set it inactive instead of deleting it",
		refs: plantCodeReferences,
	})
}

// DeleteSection removes a shop floor with no machines on it.
//
// mes_assets.section_id is a real foreign key, so the database would stop this
// anyway — but it would stop it with a constraint error rather than a sentence,
// and a client cannot tell that apart from a server fault.
func (s *Store) DeleteSection(ctx context.Context, plantCode, sectionCode string) error {
	plantCode, sectionCode = strings.TrimSpace(plantCode), strings.TrimSpace(sectionCode)
	if plantCode == "" || sectionCode == "" {
		return ErrBadInput
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		SELECT sec.id::text FROM mes_sections sec
		JOIN mes_plants p ON p.id = sec.plant_id
		WHERE p.code = $1 AND sec.code = $2`, plantCode, sectionCode).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.deleteRegisterRow(ctx, registerDelete{
		table: "mes_sections", keyCol: "id", key: id,
		noun: "shop floor", hint: "move its machines first",
		refs: []tableRef{{"mes_assets", "section_id", "machines"}},
	})
}
