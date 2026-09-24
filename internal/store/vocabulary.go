package store

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Closed sets this schema enforces with CHECK constraints, restated in Go.
//
// They are restated rather than discovered because a CHECK refusal reaches the
// caller as SQLSTATE 23514 carrying a constraint name and the offending row —
// accurate, and useless to whoever has to fix the request. Validating here
// means the caller is told which values exist, which is the only thing that
// lets them recover.
//
// Both sets below had a client sending something outside them. The platform's
// Production frontend offered work-order statuses that differed from this set
// in case alone, and defaulted its machine form to "Active", which has never
// been an asset status. Neither reached a column that would take it, and both
// surfaced as a 500.
var (
	// mes_work_orders.status — 002_schema.sql, widened by 005_cmms_gaps.sql.
	WorkOrderStatuses = []string{"draft", "scheduled", "open", "in_progress", "completed", "cancelled"}

	// mes_assets.status — 002_schema.sql.
	AssetStatuses = []string{"running", "idle", "down", "pm", "maint"}

	// mes_work_orders.priority — 002_schema.sql.
	WorkOrderPriorities = []string{"critical", "high", "medium", "low"}

	// mes_assets.criticality — 002_schema.sql.
	AssetCriticalities = []string{"A", "B", "C", "D"}
)

// Canonical folds a caller's value onto the member of set the column stores,
// and reports whether it is one at all.
//
// It returns the canonical spelling rather than a bare bool because a lenient
// check that does not also fold is worse than a strict one: accepting
// "Completed" and then writing "Completed" to the column turns a clean 400
// into the 23514 this file exists to prevent. Handlers assign the result back
// onto the request before the insert, so what was validated is what is stored.
//
// Leniency is limited to case and surrounding space. Vocabulary is not
// guessed at — "In Progress" is not folded to `in_progress`, because a client
// that means a different word should be told so rather than have one chosen
// for it.
//
// Empty is valid and canonicalises to empty: each of these columns is written
// through COALESCE(NULLIF(...)) or carries a default, so an omitted field
// means "use the default" and is not the same as a wrong one.
func Canonical(set []string, value string) (string, bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", true
	}
	lower := strings.ToLower(v)
	for _, allowed := range set {
		if allowed == v || strings.ToLower(allowed) == lower {
			return allowed, true
		}
	}
	return "", false
}

// Valid is Canonical without the folded value, for callers that only branch.
func Valid(set []string, value string) bool {
	_, ok := Canonical(set, value)
	return ok
}

// Allowed renders a set for an error message.
func Allowed(set []string) string {
	return strings.Join(set, ", ")
}

// IsCheckViolation reports a value a CHECK constraint refused (SQLSTATE 23514).
//
// The handlers validate the sets they know about; this catches the rest. It
// matters because a client that retries 5xx — as the platform's frontends do —
// retried a request that could never succeed and then reported something
// generic, so "that status does not exist" reached the user as "saving is
// broken".
func IsCheckViolation(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23514" {
		return pgErr.ConstraintName, true
	}
	return "", false
}
