package store

import "context"

// WithAdvisoryLock runs fn while holding the Postgres session advisory lock
// `key`, or skips it when another session already holds it. It reports
// whether fn ran.
//
// For jobs that more than one process may run: the API server's own
// preventive-maintenance loop, a separately deployed mes-jobs daemon, and the
// admin run-job route. Without it two runners can both see "no open work
// order" for the same schedule and each raise one.
func (s *Store) WithAdvisoryLock(ctx context.Context, key int64, fn func() error) (bool, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()

	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		return false, err
	}
	if !got {
		return false, nil
	}
	// Unlock on the same session that locked; a background context so a
	// cancelled request cannot leave the lock held on a pooled connection.
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key) }()
	return true, fn()
}
