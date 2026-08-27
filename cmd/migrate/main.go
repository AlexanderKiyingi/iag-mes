// Command migrate applies the MES schema migrations out of band.
//
// The service refuses to auto-migrate in production: config.Validate rejects
// AUTO_MIGRATE=true when ENVIRONMENT=production, on the principle that a schema
// change should be a deliberate act rather than a side effect of a deploy. That
// principle is sound but it was incomplete - there was no out-of-band path, so
// production had no way to apply a migration at all, and migrations sat in the
// repo looking deployed while never running.
//
// This is that path. It shares the runner, the embedded .sql files and the
// ledger with the in-process path, so running it is equivalent to a dev boot
// with AUTO_MIGRATE=true: the same versions, the same mes.schema_migrations
// rows, the same advisory lock, so it is safe to run while the service is up
// and safe to run twice concurrently.
//
// migrate.Up reports only an error, not which versions it applied, so this
// reads the ledger either side of the run and prints the difference - an
// operator applying a migration to production should be told what changed.
//
// Usage:
//
//	DATABASE_URL=postgres://... go run ./cmd/migrate
//	migrate -timeout 20m
//
// Safe to re-run: already-applied versions are skipped.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"iag-mes/backend/internal/db"
	"iag-mes/backend/internal/migrate"
)

func main() {
	var (
		databaseURL = flag.String("database-url", "", "postgres connection string (default: $DATABASE_URL)")
		timeout     = flag.Duration("timeout", 10*time.Minute, "overall deadline for the migration run")
	)
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if err := run(*databaseURL, *timeout); err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}
}

func run(databaseURL string, timeout time.Duration) error {
	url := strings.TrimSpace(databaseURL)
	if url == "" {
		url = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if url == "" {
		return errors.New("no database URL: pass -database-url or set DATABASE_URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	connectCtx, cancelConnect := context.WithTimeout(ctx, 30*time.Second)
	pool, err := db.NewPool(connectCtx, url)
	cancelConnect()
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	// Best effort: the ledger may not exist yet on a fresh database, in which
	// case there is simply nothing applied to compare against.
	before, _ := appliedVersions(ctx, pool)

	if err := migrate.Up(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	after, err := appliedVersions(ctx, pool)
	if err != nil {
		return fmt.Errorf("read ledger after migrating: %w", err)
	}

	var newly []string
	for v := range after {
		if !before[v] {
			newly = append(newly, v)
		}
	}
	sort.Strings(newly)
	if len(newly) == 0 {
		slog.Info("schema already up to date, nothing applied")
		return nil
	}
	slog.Info("migrations applied", "count", len(newly), "versions", newly)
	return nil
}

type versionSet = map[string]bool

func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (versionSet, error) {
	rows, err := pool.Query(ctx,
		fmt.Sprintf(`SELECT version FROM %s.schema_migrations ORDER BY version`, db.Schema))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := versionSet{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}
