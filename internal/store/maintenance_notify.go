package store

// Telling people, raising work from sensor rules, and asking procurement to
// buy — the last pieces of the maintenance plan that need no migration.
//
// Notifications go through iag-notifications, which already consumes
// mes.alert.raised on iag.operations and delivers the envelope it carries
// ({channel, templateId, audience|recipient, variables, priority}). Most
// notices address an *audience* — a name an admin routes to people with
// PUT /v1/admin/routes/<audience> — because MES knows roles, not inboxes.
// An audience nobody has routed yet is recorded by the notifications service
// as a sighting, not lost.

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"iag-mes/backend/internal/events"
)

// Audiences maintenance notices are addressed to.
const (
	AudienceFaultReports   = "maintenance.fault-reports"
	AudienceMachineStopped = "maintenance.machine-stopped"
	AudienceAssignments    = "maintenance.assignments"
	AudienceApprovals      = "maintenance.approvals"
	AudiencePMOverdue      = "maintenance.pm-overdue"
)

// Notice is one thing someone should hear about.
type Notice struct {
	Audience string
	// Recipient addresses one person directly (a platform user id, which the
	// in-app inbox matches); empty means the audience alone.
	Recipient string
	Title     string
	Message   string
	Critical  bool
	Key       string
}

// noticesOn reports whether maintenance notices are sent. On by default;
// NOTIFY_MAINTENANCE=off silences them without a deploy of anything else.
func noticesOn() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("NOTIFY_MAINTENANCE")), "off")
}

// Notify publishes the notice to the in-app inbox and by email. Templates
// are the notifications service's seeded ones: alert-in-app {Title, Message}
// and mes.alert {Title, Body}.
func (s *Store) Notify(ctx context.Context, n Notice) {
	if s.bus == nil || !s.bus.Enabled() || !noticesOn() {
		return
	}
	priority := "normal"
	if n.Critical {
		priority = "critical"
	}
	envelope := func(channel, template string, vars map[string]any) map[string]any {
		data := map[string]any{
			"channel":    channel,
			"templateId": template,
			"variables":  vars,
			"priority":   priority,
		}
		if n.Audience != "" {
			data["audience"] = n.Audience
		}
		if n.Recipient != "" {
			data["recipient"] = n.Recipient
		}
		return data
	}
	s.bus.Publish(ctx, events.TypeAlertRaised,
		envelope("in_app", "alert-in-app", map[string]any{"Title": n.Title, "Message": n.Message}), n.Key)
	// Email goes to the audience only: MES holds no one's address.
	if n.Audience != "" {
		email := n
		email.Recipient = ""
		s.bus.Publish(ctx, events.TypeAlertRaised,
			map[string]any{
				"channel": "email", "templateId": "mes.alert", "audience": email.Audience, "priority": priority,
				"variables": map[string]any{"Title": n.Title, "Body": n.Message},
			}, n.Key)
	}
}

// NotifyAssigned tells a technician a job is theirs: their own inbox when
// MES knows their platform user, the assignments audience otherwise.
func (s *Store) NotifyAssigned(ctx context.Context, wo *WorkOrder) {
	if wo == nil || wo.Assignee == nil || strings.TrimSpace(*wo.Assignee) == "" {
		return
	}
	name := strings.TrimSpace(*wo.Assignee)
	var userID *string
	_ = s.pool.QueryRow(ctx, `SELECT user_id::text FROM mes_technicians WHERE lower(name) = lower($1) AND active LIMIT 1`, name).Scan(&userID)
	n := Notice{
		Audience: AudienceAssignments,
		Title:    fmt.Sprintf("%s assigned to %s", wo.Num, name),
		Message:  fmt.Sprintf("%s on %s (%s priority).", wo.Title, wo.AssetTag, wo.Priority),
		Critical: wo.Priority == "critical",
		Key:      wo.AssetTag,
	}
	if userID != nil && *userID != "" {
		n.Recipient = *userID
	}
	s.Notify(ctx, n)
}

/* ───────────────────────────── sensor rules ───────────────────────────── */

var ruleCondition = regexp.MustCompile(`^\s*([a-z][a-z0-9_]*)\s*(>=|<=|>|<)\s*(-?[0-9]+(?:\.[0-9]+)?)\s*$`)

// ParseCondition reads an alert rule's condition, "metric op number"
// (e.g. "vibration_rms > 4.0"). ok is false for anything else — rules on
// things that are not readings (oee_pct, pm_due_days) are evaluated elsewhere.
func ParseCondition(cond string) (metric, op string, threshold float64, ok bool) {
	m := ruleCondition.FindStringSubmatch(cond)
	if m == nil {
		return "", "", 0, false
	}
	v, err := strconv.ParseFloat(m[3], 64)
	if err != nil {
		return "", "", 0, false
	}
	return m[1], m[2], v, true
}

func breaches(value float64, op string, threshold float64) bool {
	switch op {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	}
	return false
}

// SensorReadingMaxAge is how recent a reading must be to act on. The latest
// value per metric is kept for ever; a week-old spike is not a fault now.
const SensorReadingMaxAge = 24 * time.Hour

// EvaluateConditionRules turns sensor readings that break an active rule
// into fault reports — one per machine and rule while it is open, so a
// reading that stays high does not raise a report every run. Triage, the
// work order and its completion then follow the ordinary path.
func (s *Store) EvaluateConditionRules(ctx context.Context, now time.Time) (int, error) {
	rules, err := s.ListAlertRules(ctx)
	if err != nil {
		return 0, err
	}
	points, err := s.ListTelemetry(ctx, "")
	if err != nil {
		return 0, err
	}
	raised := 0
	for _, rule := range rules {
		if !rule.Active {
			continue
		}
		metric, op, threshold, ok := ParseCondition(rule.Condition)
		if !ok {
			continue
		}
		for _, p := range points {
			if p.Metric != metric || now.Sub(p.RecordedAt) > SensorReadingMaxAge || !breaches(p.Value, op, threshold) {
				continue
			}
			var open bool
			if err := s.pool.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM mes_alerts
				  WHERE attrs->>'kind' = $1 AND attrs->>'rule_code' = $2 AND attrs->>'asset_tag' = $3
				    AND status IN ('new', 'ack', 'investigating'))`,
				FaultReportKind, rule.Code, p.AssetTag).Scan(&open); err != nil {
				return raised, err
			}
			if open {
				continue
			}
			unit := p.Unit
			if unit != "" {
				unit = " " + unit
			}
			_, err := s.createFaultReport(ctx, FaultReportInput{
				AssetTag:    p.AssetTag,
				Title:       fmt.Sprintf("%s: %s %.2f%s (rule %s)", rule.Name, metric, p.Value, unit, rule.Condition),
				Description: fmt.Sprintf("Raised by sensor rule %s from the reading at %s.", rule.Code, p.RecordedAt.UTC().Format(time.RFC3339)),
				Severity:    rule.Severity,
			}, "Sensor rule "+rule.Code, map[string]any{
				"rule_code": rule.Code,
				"reading":   p.Value,
				"metric":    metric,
				"read_at":   p.RecordedAt.UTC().Format(time.RFC3339),
			})
			if err != nil {
				// A machine the readings name but MES does not know: skip it.
				continue
			}
			raised++
		}
	}
	return raised, nil
}

/* ─────────────────────────── purchase requests ─────────────────────────── */

type PurchaseRequestInput struct {
	SKU           string   `json:"sku"`
	Name          string   `json:"name"`
	Qty           float64  `json:"qty"`
	UOM           string   `json:"uom"`
	EstimatedCost *float64 `json:"estimated_cost"`
	Currency      string   `json:"currency"`
	// Department must match a procurement budget's dept, which is how
	// procurement picks the budget for an imported request.
	Department string `json:"department"`
	Reason     string `json:"reason"`
	Urgent     bool   `json:"urgent"`
	// Ref makes the request idempotent in procurement (origin_ref). Defaults
	// to one request per part per day.
	Ref string `json:"ref"`
}

// RequestPurchase asks procurement to buy a spare part, as the
// procurement.requested event iag-procurement imports from iag.operations
// (origin "maintenance"; status Pending Approval; idempotent on the ref).
// It returns the ref to look the requisition up by (GET
// /requisitions/by-origin?system=maintenance&ref=…).
func (s *Store) RequestPurchase(ctx context.Context, in PurchaseRequestInput, actor string, now time.Time) (string, error) {
	sku := strings.TrimSpace(in.SKU)
	if sku == "" {
		return "", fmt.Errorf("%w: say which part (sku)", ErrBadInput)
	}
	if in.Qty <= 0 {
		return "", fmt.Errorf("%w: a quantity of one or more", ErrBadInput)
	}
	dept := strings.TrimSpace(in.Department)
	if dept == "" {
		return "", fmt.Errorf("%w: say which department's budget pays (department)", ErrBadInput)
	}
	if s.bus == nil || !s.bus.Enabled() {
		return "", fmt.Errorf("%w: purchase requests travel on the event bus, which is off in this deployment", ErrConflict)
	}
	ref := strings.TrimSpace(in.Ref)
	if ref == "" {
		ref = fmt.Sprintf("SP-%s-%s", sku, now.UTC().Format("20060102"))
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = sku
	}
	uom := strings.TrimSpace(in.UOM)
	amount := ""
	if in.EstimatedCost != nil && *in.EstimatedCost > 0 {
		amount = strconv.FormatFloat(*in.EstimatedCost, 'f', 2, 64)
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "UGX"
	}
	urgency := "medium"
	if in.Urgent {
		urgency = "high"
	}
	justification := strings.TrimSpace(in.Reason)
	if justification == "" {
		justification = "Spare part at or below its reorder level."
	}
	s.bus.Publish(ctx, events.TypeProcurementRequested, map[string]any{
		"sourceService": "maintenance",
		"requestId":     ref,
		"title":         strings.TrimSpace(fmt.Sprintf("Spare part: %s (%s) × %g %s", name, sku, in.Qty, uom)),
		"department":    dept,
		"requestedBy":   actor,
		"amount":        amount,
		"currency":      currency,
		"urgency":       urgency,
		"justification": justification,
	}, ref)
	return ref, nil
}
