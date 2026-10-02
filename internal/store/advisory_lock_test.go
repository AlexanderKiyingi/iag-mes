package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Database-backed; runs where TEST_DATABASE_URL is set (CI sets it). Needs no
// tables, so it holds before migrations apply.
func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool)
}

func TestWithAdvisoryLockSkipsWhileAnotherRunHoldsIt(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	const key int64 = 0x7465737431 // distinct from every real job key

	inner := false
	ran, err := st.WithAdvisoryLock(ctx, key, func() error {
		// A second runner arriving mid-sync must not run the job too.
		again, err := st.WithAdvisoryLock(ctx, key, func() error {
			inner = true
			return nil
		})
		if err != nil {
			t.Fatalf("nested: %v", err)
		}
		if again {
			t.Fatal("a second runner took the lock while the first held it")
		}
		return nil
	})
	if err != nil || !ran {
		t.Fatalf("first runner: ran=%v err=%v", ran, err)
	}
	if inner {
		t.Fatal("the job ran twice concurrently")
	}

	// Released on return, so the next scheduled run proceeds.
	ran, err = st.WithAdvisoryLock(ctx, key, func() error { return nil })
	if err != nil || !ran {
		t.Fatalf("lock was not released: ran=%v err=%v", ran, err)
	}
}
