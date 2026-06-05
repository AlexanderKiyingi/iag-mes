package auditlog

import (
	"context"
	"sync"
	"time"
)

type entry struct {
	Method     string
	Path       string
	StatusCode int
	UserName   string
	DurationMs int
	ClientIP   string
	LoggedAt   time.Time
}

// MemoryStore retains recent HTTP audit rows in-process (no DATABASE_URL required).
type MemoryStore struct {
	mu   sync.Mutex
	rows []entry
	max  int
}

func NewMemoryStore(max int) *MemoryStore {
	if max <= 0 {
		max = 500
	}
	return &MemoryStore{max: max}
}

func (s *MemoryStore) LogAPIRequest(_ context.Context, method, path string, statusCode int, userName string, durationMs int, clientIP string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, entry{
		Method: method, Path: path, StatusCode: statusCode,
		UserName: userName, DurationMs: durationMs, ClientIP: clientIP,
		LoggedAt: time.Now().UTC(),
	})
	if len(s.rows) > s.max {
		s.rows = s.rows[len(s.rows)-s.max:]
	}
	return nil
}

func (s *MemoryStore) list(limit int) []entry {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.rows)
	if n == 0 {
		return nil
	}
	start := n - limit
	if start < 0 {
		start = 0
	}
	out := make([]entry, len(s.rows[start:]))
	copy(out, s.rows[start:])
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (s *MemoryStore) ListAPIAuditLogs(_ context.Context, limit int) ([]map[string]any, int, error) {
	rows := s.list(limit)
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"method": r.Method, "path": r.Path, "status": r.StatusCode,
			"user": r.UserName, "duration_ms": r.DurationMs, "logged_at": r.LoggedAt,
		})
	}
	s.mu.Lock()
	total := len(s.rows)
	s.mu.Unlock()
	return out, total, nil
}

func (s *MemoryStore) MonitoringSummary(_ context.Context, kafkaEnabled bool) (map[string]any, error) {
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	s.mu.Lock()
	defer s.mu.Unlock()
	var total24h, errors24h, sumDur int
	for _, r := range s.rows {
		if r.LoggedAt.Before(cutoff) {
			continue
		}
		total24h++
		if r.StatusCode >= 400 {
			errors24h++
		}
		sumDur += r.DurationMs
	}
	avg := 0.0
	if total24h > 0 {
		avg = float64(sumDur) / float64(total24h)
	}
	return map[string]any{
		"requests_24h":       total24h,
		"errors_24h":         errors24h,
		"avg_duration_ms":    avg,
		"kafka_enabled":      kafkaEnabled,
		"audit_persistence":  "memory",
		"audit_buffer_count": len(s.rows),
	}, nil
}

func (s *MemoryStore) APIMonitoringActivity(ctx context.Context, limit int) ([]map[string]any, error) {
	items, _, err := s.ListAPIAuditLogs(ctx, limit)
	return items, err
}
