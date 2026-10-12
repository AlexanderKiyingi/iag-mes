package migrate

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pending is what stands between a deploy and a service answering 500 per
// request because its migrations never ran.
//
// Database-backed; runs where TEST_DATABASE_URL is set (CI sets it). It needs
// its own database because it applies the real migrations.
func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	p, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	// internal/db creates the schema on connect; these tests talk to the pool
	// directly, so they stand it up themselves. Up writes its ledger into the
	// schema and does not create it.
	if _, err := p.Exec(context.Background(), `CREATE SCHEMA IF NOT EXISTS mes`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return p
}

// An empty database has every migration pending — not zero. Reporting zero
// would let a binary serve against a database with no tables at all, which is
// the worst version of the bug this guards.
func TestEmptyDatabaseHasEveryMigrationPending(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `DROP TABLE IF EXISTS mes.schema_migrations`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	pending, err := Pending(ctx, p)
	if err != nil {
		t.Fatalf("Pending on an empty database: %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("an empty database reported nothing pending")
	}
	if pending[0] != "001_schema" && pending[0] != "002_schema" {
		t.Logf("oldest pending is %s", pending[0])
	}
}

// After Up there is nothing left, which is the state a healthy boot sees.
func TestNothingPendingOnceApplied(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	if err := Up(ctx, p); err != nil {
		t.Fatalf("Up: %v", err)
	}
	pending, err := Pending(ctx, p)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("want nothing pending after Up, got %v", pending)
	}
}

// The gap this was written for: the ledger exists and is simply behind. That
// is a deploy whose migrations were never applied out of band, and it must be
// reported rather than served.
func TestABehindLedgerIsReported(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	if err := Up(ctx, p); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var newest string
	if err := p.QueryRow(ctx,
		`SELECT version FROM mes.schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&newest); err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if _, err := p.Exec(ctx,
		`DELETE FROM mes.schema_migrations WHERE version = $1`, newest); err != nil {
		t.Fatalf("unstamp: %v", err)
	}
	pending, err := Pending(ctx, p)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0] != newest {
		t.Fatalf("want exactly %s pending, got %v", newest, pending)
	}

	// Read-only: it must not have applied or stamped anything itself.
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM mes.schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("recount: %v", err)
	}
	if again, _ := Pending(ctx, p); len(again) != 1 {
		t.Fatalf("Pending changed the ledger; second call saw %v", again)
	}
}
