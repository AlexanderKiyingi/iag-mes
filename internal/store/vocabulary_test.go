package store

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"iag-mes/backend/migrations"
)

// The sets in vocabulary.go are a restatement of the CHECK constraints, and a
// restatement drifts. These read the migrations and compare.
//
// Drift is quiet either way: a Go set narrower than its column rejects a value
// the database would have taken, and one that is wider lets a value through to
// a 23514 — the failure vocabulary.go exists to prevent. Both show up as a
// support ticket, not a red build, unless something checks.

const checkInPattern = `(?s)CHECK\s*\(\s*%s\s+IN\s*\((.*?)\)\s*\)`

// setFromMigration pulls the values out of the LAST `CHECK (<column> IN (...))`
// in the named migration. Last, because a migration that widens a set drops the
// old constraint and re-adds it in the same file.
func setFromMigration(t *testing.T, file, column string) []string {
	t.Helper()
	raw, err := migrations.FS.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	re := regexp.MustCompile(strings.Replace(checkInPattern, "%s", regexp.QuoteMeta(column), 1))
	matches := re.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatalf("no CHECK (%s IN (...)) in %s — did the constraint move?", column, file)
	}
	last := matches[len(matches)-1][1]

	var out []string
	for _, piece := range strings.Split(last, ",") {
		piece = strings.TrimSpace(strings.Trim(strings.TrimSpace(piece), "'"))
		if piece != "" {
			out = append(out, piece)
		}
	}
	return out
}

// setForTable is for columns whose name repeats across a migration — every
// table in 002 has a `status` — so the search starts at the table's CREATE.
func setForTable(t *testing.T, file, table, column string) []string {
	t.Helper()
	raw, err := migrations.FS.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	text := string(raw)
	start := strings.Index(text, "CREATE TABLE IF NOT EXISTS "+table)
	if start < 0 {
		t.Fatalf("no CREATE TABLE for %s in %s", table, file)
	}
	end := strings.Index(text[start:], ");")
	if end < 0 {
		t.Fatalf("unterminated CREATE TABLE for %s in %s", table, file)
	}
	body := text[start : start+end]

	re := regexp.MustCompile(strings.Replace(checkInPattern, "%s", regexp.QuoteMeta(column), 1))
	match := re.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("no CHECK (%s IN (...)) on %s in %s", column, table, file)
	}
	var out []string
	for _, piece := range strings.Split(match[1], ",") {
		piece = strings.TrimSpace(strings.Trim(strings.TrimSpace(piece), "'"))
		if piece != "" {
			out = append(out, piece)
		}
	}
	return out
}

func assertSameSet(t *testing.T, name string, got, want []string) {
	t.Helper()
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if strings.Join(g, ",") != strings.Join(w, ",") {
		t.Fatalf("%s drifted from the migration\n  go:        %v\n  migration: %v", name, g, w)
	}
}

func TestWorkOrderStatusesMatchTheColumn(t *testing.T) {
	// 005 drops and re-adds the constraint 002 created, so 005 is current.
	assertSameSet(t, "WorkOrderStatuses", WorkOrderStatuses,
		setFromMigration(t, "005_cmms_gaps.sql", "status"))
}

func TestAssetStatusesMatchTheColumn(t *testing.T) {
	assertSameSet(t, "AssetStatuses", AssetStatuses,
		setForTable(t, "002_schema.sql", "mes_assets", "status"))
}

func TestWorkOrderPrioritiesMatchTheColumn(t *testing.T) {
	assertSameSet(t, "WorkOrderPriorities", WorkOrderPriorities,
		setForTable(t, "002_schema.sql", "mes_work_orders", "priority"))
}

func TestAssetCriticalitiesMatchTheColumn(t *testing.T) {
	assertSameSet(t, "AssetCriticalities", AssetCriticalities,
		setForTable(t, "002_schema.sql", "mes_assets", "criticality"))
}

func TestValidTreatsEmptyAsUnset(t *testing.T) {
	// These columns are written through COALESCE(NULLIF(...)), so a caller
	// omitting the field is saying "use the default", not sending something
	// wrong. Rejecting empty would break every partial write.
	if !Valid(WorkOrderStatuses, "") || !Valid(AssetStatuses, "   ") {
		t.Fatal("empty should be allowed — the column defaults it")
	}
}

func TestCanonicalFoldsCaseAndReturnsWhatTheColumnStores(t *testing.T) {
	// Case is folded, and the folded value comes back — accepting "Completed"
	// and then writing "Completed" would fail the CHECK just the same, so the
	// caller's spelling must not survive validation.
	got, ok := Canonical(WorkOrderStatuses, "  Completed ")
	if !ok || got != "completed" {
		t.Fatalf(`Canonical("  Completed ") = %q, %v; want "completed", true`, got, ok)
	}
	if got, ok := Canonical(AssetStatuses, "MAINT"); !ok || got != "maint" {
		t.Fatalf(`Canonical("MAINT") = %q, %v; want "maint", true`, got, ok)
	}
}

func TestCanonicalDoesNotGuessAtVocabulary(t *testing.T) {
	// "In Progress" differs from `in_progress` by more than case. A client
	// that means a different word is told so rather than having one chosen for
	// it — and it reads the set in the message.
	if _, ok := Canonical(WorkOrderStatuses, "In Progress"); ok {
		t.Fatal("whitespace is not an underscore; the caller should be told")
	}
	if _, ok := Canonical(AssetStatuses, "Active"); ok {
		// The Production app's machine form defaulted to this, and it has
		// never been an asset status, so every machine it created was refused.
		t.Fatal("'Active' is not an asset status and must not be accepted")
	}
	if _, ok := Canonical(AssetCriticalities, "E"); ok {
		t.Fatal("criticality is A–D")
	}
	if got, ok := Canonical(AssetCriticalities, "a"); !ok || got != "A" {
		t.Fatalf(`Canonical("a") = %q, %v; want "A", true — the set is upper case`, got, ok)
	}
}
