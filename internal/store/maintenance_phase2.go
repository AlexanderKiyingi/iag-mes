package store

// Phase 2 of the maintenance plan: fault reports from the floor, PM on
// running hours, checklists that are run rather than read, failure codes, and
// approval of expensive work.
//
// None of it needs a migration. Each lives where the data already belongs:
//   - a fault report is an alert (kind "fault_report"), whose status column
//     already is a triage workflow (new → ack → investigating → resolved, or
//     false_positive);
//   - a meter reading is a telemetry point (metric "running_hours"), whose
//     timeseries is the reading history a PM interval is measured against;
//   - checklist results, sign-off, failure codes and approval are facts about
//     one work order, kept in its attrs and enforced here when it is started
//     or completed.
// MES will not serve behind its schema and its migrations are run by hand, so
// a feature that needs none ships on merge.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/* ───────────────────────────── fault reports ───────────────────────────── */

// FaultReportKind marks an alert raised by a person on the floor rather than
// by a rule.
const FaultReportKind = "fault_report"

type FaultReportInput struct {
	AssetTag       string `json:"asset_tag"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	MachineStopped bool   `json:"machine_stopped"`
	// Severity is crit/warn/info. A stopped machine is always crit.
	Severity    string `json:"severity"`
	Attachments []any  `json:"attachments,omitempty"`
}

type FaultReportConvert struct {
	Title    string     `json:"title"`
	Priority string     `json:"priority"`
	Assignee *string    `json:"assignee"`
	DueAt    *time.Time `json:"due_at"`
}

// CreateFaultReport records what someone on the floor saw. When the machine
// has stopped it also opens a downtime event, so the stop is counted from the
// moment it was reported, not from when maintenance got to it.
func (s *Store) CreateFaultReport(ctx context.Context, in FaultReportInput, reportedBy string) (*Alert, error) {
	return s.createFaultReport(ctx, in, reportedBy, nil)
}

// createFaultReport is CreateFaultReport with extra attrs, for reports a
// sensor rule raises (rule_code, reading) rather than a person.
func (s *Store) createFaultReport(ctx context.Context, in FaultReportInput, reportedBy string, extra map[string]any) (*Alert, error) {
	tag := strings.TrimSpace(in.AssetTag)
	title := strings.TrimSpace(in.Title)
	if tag == "" {
		return nil, fmt.Errorf("%w: say which machine", ErrBadInput)
	}
	if title == "" || len(title) > 200 {
		return nil, fmt.Errorf("%w: describe the fault in 1–200 characters", ErrBadInput)
	}
	if _, err := s.GetAssetByTag(ctx, tag); err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: no machine %q", ErrBadInput, tag)
		}
		return nil, err
	}
	severity := strings.ToLower(strings.TrimSpace(in.Severity))
	if in.MachineStopped {
		severity = "crit"
	}
	if severity == "" {
		severity = "warn"
	}
	if !slices.Contains([]string{"crit", "warn", "info"}, severity) {
		return nil, fmt.Errorf("%w: severity must be crit, warn or info", ErrBadInput)
	}
	attrs := map[string]any{
		"kind":            FaultReportKind,
		"asset_tag":       tag,
		"title":           title,
		"description":     strings.TrimSpace(in.Description),
		"machine_stopped": in.MachineStopped,
		"reported_by":     reportedBy,
	}
	if len(in.Attachments) > 0 {
		attrs["attachments"] = in.Attachments
	}
	for k, v := range extra {
		attrs[k] = v
	}
	alert, err := s.CreateAlert(ctx, Alert{
		Severity: severity,
		Source:   tag,
		Message:  title,
		Attrs:    attrs,
	})
	if err != nil {
		return nil, err
	}
	if in.MachineStopped {
		d, err := s.CreateDowntimeEvent(ctx, DowntimeEvent{
			AssetTag: tag,
			Category: "breakdown",
			Reason:   title,
			Attrs:    map[string]any{"fault_report_id": alert.ID.String(), "reported_by": reportedBy},
		})
		if err != nil {
			return nil, fmt.Errorf("fault reported, but the downtime could not be opened: %w", err)
		}
		alert, err = s.mergeAlertAttrs(ctx, alert.ID, map[string]any{"downtime_event_id": d.ID.String()})
		if err != nil {
			return nil, err
		}
	}
	notice := Notice{
		Audience: AudienceFaultReports,
		Title:    fmt.Sprintf("Fault reported on %s", tag),
		Message:  fmt.Sprintf("%s — reported by %s.", title, reportedBy),
		Critical: severity == "crit",
		Key:      tag,
	}
	s.Notify(ctx, notice)
	if in.MachineStopped {
		notice.Audience = AudienceMachineStopped
		notice.Title = fmt.Sprintf("%s has stopped", tag)
		notice.Critical = true
		s.Notify(ctx, notice)
	}
	return alert, nil
}

func (s *Store) ListFaultReports(ctx context.Context, status string, limit int) ([]Alert, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT id, rule_id, severity, source, message, status, acknowledged_at, resolved_at, occurred_at, attrs
	      FROM mes_alerts WHERE attrs->>'kind' = $1`
	args := []any{FaultReportKind}
	if status != "" {
		q += ` AND status = $2`
		args = append(args, status)
	}
	q += fmt.Sprintf(` ORDER BY occurred_at DESC LIMIT %d`, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		var attrs []byte
		if err := rows.Scan(&a.ID, &a.RuleID, &a.Severity, &a.Source, &a.Message, &a.Status,
			&a.AcknowledgedAt, &a.ResolvedAt, &a.OccurredAt, &attrs); err != nil {
			return nil, err
		}
		a.Attrs = scanAttrs(attrs)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) getFaultReport(ctx context.Context, id uuid.UUID) (*Alert, error) {
	var a Alert
	var attrs []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, rule_id, severity, source, message, status, acknowledged_at, resolved_at, occurred_at, attrs
		FROM mes_alerts WHERE id = $1 AND attrs->>'kind' = $2`, id, FaultReportKind).Scan(
		&a.ID, &a.RuleID, &a.Severity, &a.Source, &a.Message, &a.Status,
		&a.AcknowledgedAt, &a.ResolvedAt, &a.OccurredAt, &attrs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Attrs = scanAttrs(attrs)
	return &a, nil
}

func (s *Store) mergeAlertAttrs(ctx context.Context, id uuid.UUID, patch map[string]any) (*Alert, error) {
	raw, _ := json.Marshal(patch)
	var a Alert
	var attrs []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE mes_alerts SET attrs = COALESCE(attrs, '{}'::jsonb) || $2::jsonb WHERE id = $1
		RETURNING id, rule_id, severity, source, message, status, acknowledged_at, resolved_at, occurred_at, attrs`,
		id, raw).Scan(&a.ID, &a.RuleID, &a.Severity, &a.Source, &a.Message, &a.Status,
		&a.AcknowledgedAt, &a.ResolvedAt, &a.OccurredAt, &attrs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Attrs = scanAttrs(attrs)
	return &a, nil
}

// ConvertFaultReport turns a report into a breakdown work order. The report
// moves to "investigating" and points at the order; completing the order
// resolves it (see completeFaultReport).
func (s *Store) ConvertFaultReport(ctx context.Context, id uuid.UUID, in FaultReportConvert, actor string) (*WorkOrder, *Alert, error) {
	report, err := s.getFaultReport(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if report.Status != "new" && report.Status != "ack" {
		if wo, _ := report.Attrs["work_order"].(string); wo != "" {
			return nil, nil, fmt.Errorf("%w: this report is already work order %s", ErrConflict, wo)
		}
		return nil, nil, fmt.Errorf("%w: this report is %s and cannot become a work order", ErrConflict, report.Status)
	}
	priority := strings.ToLower(strings.TrimSpace(in.Priority))
	if priority == "" {
		priority = "high"
		if report.Severity == "crit" {
			priority = "critical"
		}
	}
	if !slices.Contains(WorkOrderPriorities, priority) {
		return nil, nil, fmt.Errorf("%w: priority must be one of: %s", ErrBadInput, Allowed(WorkOrderPriorities))
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = report.Message
	}
	num, err := s.NextWorkOrderNum(ctx)
	if err != nil {
		return nil, nil, err
	}
	tag, _ := report.Attrs["asset_tag"].(string)
	if tag == "" {
		tag = report.Source
	}
	wo, err := s.CreateWorkOrder(ctx, WorkOrder{
		Num:      num,
		Title:    title,
		AssetTag: tag,
		WOType:   "breakdown",
		Priority: priority,
		Status:   "open",
		Assignee: in.Assignee,
		DueAt:    in.DueAt,
		Attrs: map[string]any{
			"fault_report_id": report.ID.String(),
			"description":     report.Attrs["description"],
			"reported_by":     report.Attrs["reported_by"],
			"raised_by":       actor,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	var a Alert
	var attrs []byte
	patch, _ := json.Marshal(map[string]any{"work_order": wo.Num, "triaged_by": actor})
	err = s.pool.QueryRow(ctx, `
		UPDATE mes_alerts SET status = 'investigating', acknowledged_at = COALESCE(acknowledged_at, $2),
		       attrs = COALESCE(attrs, '{}'::jsonb) || $3::jsonb
		WHERE id = $1
		RETURNING id, rule_id, severity, source, message, status, acknowledged_at, resolved_at, occurred_at, attrs`,
		id, now, patch).Scan(&a.ID, &a.RuleID, &a.Severity, &a.Source, &a.Message, &a.Status,
		&a.AcknowledgedAt, &a.ResolvedAt, &a.OccurredAt, &attrs)
	if err != nil {
		return wo, nil, err
	}
	a.Attrs = scanAttrs(attrs)
	return wo, &a, nil
}

// RejectFaultReport closes a report that needs no work, with the reason. Any
// downtime it opened is ended: the machine was not, in the end, down for
// maintenance.
func (s *Store) RejectFaultReport(ctx context.Context, id uuid.UUID, reason, actor string) (*Alert, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("%w: say why the report needs no work", ErrBadInput)
	}
	report, err := s.getFaultReport(ctx, id)
	if err != nil {
		return nil, err
	}
	if report.Status != "new" && report.Status != "ack" {
		return nil, fmt.Errorf("%w: this report is %s and cannot be rejected", ErrConflict, report.Status)
	}
	if dt, _ := report.Attrs["downtime_event_id"].(string); dt != "" {
		if dID, err := uuid.Parse(dt); err == nil {
			_, _ = s.EndDowntimeEvent(ctx, dID)
		}
	}
	patch, _ := json.Marshal(map[string]any{"rejected_reason": reason, "triaged_by": actor})
	var a Alert
	var attrs []byte
	err = s.pool.QueryRow(ctx, `
		UPDATE mes_alerts SET status = 'false_positive', resolved_at = NOW(),
		       attrs = COALESCE(attrs, '{}'::jsonb) || $2::jsonb
		WHERE id = $1
		RETURNING id, rule_id, severity, source, message, status, acknowledged_at, resolved_at, occurred_at, attrs`,
		id, patch).Scan(&a.ID, &a.RuleID, &a.Severity, &a.Source, &a.Message, &a.Status,
		&a.AcknowledgedAt, &a.ResolvedAt, &a.OccurredAt, &attrs)
	if err != nil {
		return nil, err
	}
	a.Attrs = scanAttrs(attrs)
	return &a, nil
}

// completeFaultReport runs after a work order raised from a report is
// completed: the report is resolved and the stop it opened is ended.
func (s *Store) completeFaultReport(ctx context.Context, wo *WorkOrder) {
	raw, _ := wo.Attrs["fault_report_id"].(string)
	id, err := uuid.Parse(raw)
	if err != nil {
		return
	}
	report, err := s.getFaultReport(ctx, id)
	if err != nil {
		return
	}
	if dt, _ := report.Attrs["downtime_event_id"].(string); dt != "" {
		if dID, err := uuid.Parse(dt); err == nil {
			_, _ = s.EndDowntimeEvent(ctx, dID)
		}
	}
	if report.Status != "resolved" && report.Status != "closed" {
		_, _ = s.ResolveAlert(ctx, id)
	}
}

/* ─────────────────────────────── checklists ─────────────────────────────── */

// ChecklistResults are what a step can be marked.
var ChecklistResults = []string{"pass", "fail", "na"}

type ChecklistEntry struct {
	Index   int      `json:"index"`
	Result  string   `json:"result"`
	Reading *float64 `json:"reading,omitempty"`
	Note    string   `json:"note,omitempty"`
	// By is who marked the step on the paper sheet, when someone else is
	// typing it in. Empty means the person signed in did it.
	By string `json:"by,omitempty"`
}

// checklistSteps reads a work order's checklist: strings, or objects with a
// "step"/"text"/"title" field (templates have been written both ways).
func checklistSteps(attrs map[string]any) []string {
	raw, _ := attrs["checklist"].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		switch v := item.(type) {
		case string:
			if t := strings.TrimSpace(v); t != "" {
				out = append(out, t)
			}
		case map[string]any:
			for _, k := range []string{"step", "text", "title", "name"} {
				if t, _ := v[k].(string); strings.TrimSpace(t) != "" {
					out = append(out, strings.TrimSpace(t))
					break
				}
			}
		}
	}
	return out
}

func checklistResultsOf(attrs map[string]any) map[int]map[string]any {
	out := map[int]map[string]any{}
	raw, _ := attrs["checklist_results"].([]any)
	for _, item := range raw {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		if idx, ok := m["index"].(float64); ok {
			out[int(idx)] = m
		}
	}
	return out
}

// RecordChecklist marks steps on a work order. Entries are merged by step:
// marking step 3 does not touch steps 1 and 2.
func (s *Store) RecordChecklist(ctx context.Context, num string, entries []ChecklistEntry, actor string) (*WorkOrder, error) {
	wo, err := s.GetWorkOrder(ctx, num)
	if err != nil {
		return nil, err
	}
	if wo.Status == "completed" || wo.Status == "cancelled" {
		return nil, fmt.Errorf("%w: work order %s is %s; its checklist is closed", ErrConflict, num, wo.Status)
	}
	steps := checklistSteps(wo.Attrs)
	if len(steps) == 0 {
		return nil, fmt.Errorf("%w: work order %s has no checklist", ErrBadInput, num)
	}
	results := checklistResultsOf(wo.Attrs)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, e := range entries {
		if e.Index < 0 || e.Index >= len(steps) {
			return nil, fmt.Errorf("%w: step %d does not exist (the checklist has %d)", ErrBadInput, e.Index+1, len(steps))
		}
		result := strings.ToLower(strings.TrimSpace(e.Result))
		if !slices.Contains(ChecklistResults, result) {
			return nil, fmt.Errorf("%w: a step is marked pass, fail or na", ErrBadInput)
		}
		if result == "fail" && strings.TrimSpace(e.Note) == "" {
			return nil, fmt.Errorf("%w: step %d failed — say what was found", ErrBadInput, e.Index+1)
		}
		// Acceptance limits (installation.go): a reading outside them is not
		// a pass, and a commissioning test is passed on a measurement.
		if err := checkAcceptance(wo, e, result); err != nil {
			return nil, err
		}
		entry := map[string]any{
			"index":  e.Index,
			"step":   steps[e.Index],
			"result": result,
			"by":     actor,
			"at":     now,
		}
		if by := strings.TrimSpace(e.By); by != "" && !strings.EqualFold(by, actor) {
			entry["by"] = by
			entry["entered_by"] = actor
		}
		if e.Reading != nil {
			entry["reading"] = *e.Reading
		}
		if n := strings.TrimSpace(e.Note); n != "" {
			entry["note"] = n
		}
		results[e.Index] = entry
	}
	list := make([]any, 0, len(results))
	for i := range steps {
		if r, ok := results[i]; ok {
			list = append(list, r)
		}
	}
	patch := map[string]any{"checklist_results": list}
	// A changed test result on a commissioning job is not the result its
	// acceptors signed for.
	if isType(wo, WOTypeCommissioning) {
		if handover, withdrawn := withdrawAcceptance(wo.Attrs); withdrawn {
			patch["handover"] = handover
		}
	}
	return s.PatchWorkOrder(ctx, num, WorkOrderPatch{Attrs: patch})
}

// PaperSignature is a signature on a printed sheet that someone else is
// typing in: who signed and the date they wrote. Recorded as the signer, with
// the person entering it kept as entered_by — otherwise a clerk transcribing
// a technician's sheet would appear to have done the work, and the "cannot
// check your own work" rule would compare the wrong names.
type PaperSignature struct {
	SignedBy string `json:"signed_by"`
	SignedOn string `json:"signed_on"`
}

func (p PaperSignature) entry(actor string, now time.Time) (map[string]any, error) {
	name := strings.TrimSpace(p.SignedBy)
	if name == "" {
		return map[string]any{"by": actor, "at": now.Format(time.RFC3339)}, nil
	}
	at := now
	if d := strings.TrimSpace(p.SignedOn); d != "" {
		parsed, err := time.Parse("2006-01-02", d)
		if err != nil {
			return nil, fmt.Errorf("%w: signed_on is a date (YYYY-MM-DD)", ErrBadInput)
		}
		if parsed.After(now) {
			return nil, fmt.Errorf("%w: a sheet cannot be signed in the future", ErrBadInput)
		}
		at = parsed
	}
	out := map[string]any{"by": name, "at": at.Format(time.RFC3339), "on_paper": true}
	if !strings.EqualFold(name, actor) {
		out["entered_by"] = actor
	}
	return out, nil
}

// Signoff records who did the work (technician, before completion) or who
// checked it (supervisor, after completion). Without a paper signature the
// signer is the person signed in.
func (s *Store) Signoff(ctx context.Context, num, role, actor string, paper ...PaperSignature) (*WorkOrder, error) {
	var sig PaperSignature
	if len(paper) > 0 {
		sig = paper[0]
	}
	wo, err := s.GetWorkOrder(ctx, num)
	if err != nil {
		return nil, err
	}
	signoff, _ := wo.Attrs["signoff"].(map[string]any)
	if signoff == nil {
		signoff = map[string]any{}
	}
	entry, err := sig.entry(actor, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	switch role {
	case "technician":
		if wo.Status == "completed" || wo.Status == "cancelled" {
			return nil, fmt.Errorf("%w: work order %s is already %s", ErrConflict, num, wo.Status)
		}
		signoff["technician"] = entry
	case "supervisor":
		if wo.Status != "completed" {
			return nil, fmt.Errorf("%w: a supervisor checks the work once it is completed", ErrConflict)
		}
		tech, _ := signoff["technician"].(map[string]any)
		if by, _ := tech["by"].(string); by != "" && strings.EqualFold(by, entry["by"].(string)) {
			return nil, fmt.Errorf("%w: the technician who did the work cannot also check it", ErrConflict)
		}
		signoff["supervisor"] = entry
	default:
		return nil, fmt.Errorf("%w: role must be technician or supervisor", ErrBadInput)
	}
	return s.PatchWorkOrder(ctx, num, WorkOrderPatch{Attrs: map[string]any{"signoff": signoff}})
}

/* ───────────────────────────── failure codes ───────────────────────────── */

// FailureCatalog is the problem / cause / remedy vocabulary a breakdown is
// closed with, after ISO 14224's failure-mode, failure-cause and
// maintenance-activity lists, cut to what a coffee mill's machines meet. It is
// fixed in code so every factory's reliability figures count the same things;
// "Other" plus the note covers the rest.
var FailureCatalog = struct {
	Problems []string `json:"problems"`
	Causes   []string `json:"causes"`
	Remedies []string `json:"remedies"`
}{
	Problems: []string{
		"Bearing failure", "Belt or chain failure", "Electrical fault", "Motor failure",
		"Overheating", "Abnormal vibration or noise", "Leak", "Blockage or jam",
		"Worn or broken part", "Misalignment", "Sensor or control fault", "Structural damage", "Other",
	},
	Causes: []string{
		"Normal wear", "Lack of lubrication", "Contamination or dust", "Overload",
		"Operating error", "Missed or late PM", "Poor installation or repair", "Power supply problem",
		"Foreign object in product", "Design or specification", "Unknown", "Other",
	},
	Remedies: []string{
		"Replaced part", "Repaired", "Adjusted or aligned", "Cleaned", "Lubricated",
		"Tightened", "Reset or restarted", "Modified", "Temporary fix — follow-up needed", "Other",
	},
}

// FailureCodedTypes are the work order types a failure code is required on:
// the ones that exist because something failed.
var FailureCodedTypes = []string{"breakdown", "corrective"}

type FailureInput struct {
	Problem string `json:"problem"`
	Cause   string `json:"cause"`
	Remedy  string `json:"remedy"`
	Note    string `json:"note"`
}

func canonicalIn(list []string, value string) (string, bool) {
	v := strings.TrimSpace(value)
	for _, item := range list {
		if strings.EqualFold(item, v) {
			return item, true
		}
	}
	return "", false
}

func (s *Store) SetFailure(ctx context.Context, num string, in FailureInput, actor string) (*WorkOrder, error) {
	problem, ok1 := canonicalIn(FailureCatalog.Problems, in.Problem)
	cause, ok2 := canonicalIn(FailureCatalog.Causes, in.Cause)
	remedy, ok3 := canonicalIn(FailureCatalog.Remedies, in.Remedy)
	if !ok1 || !ok2 || !ok3 {
		return nil, fmt.Errorf("%w: problem, cause and remedy must come from GET /failure-codes", ErrBadInput)
	}
	note := strings.TrimSpace(in.Note)
	if (problem == "Other" || cause == "Other" || remedy == "Other") && note == "" {
		return nil, fmt.Errorf("%w: \"Other\" needs a note saying what it was", ErrBadInput)
	}
	failure := map[string]any{"problem": problem, "cause": cause, "remedy": remedy, "by": actor}
	if note != "" {
		failure["note"] = note
	}
	return s.PatchWorkOrder(ctx, num, WorkOrderPatch{Attrs: map[string]any{"failure": failure}})
}

type FailureCount struct {
	Problem  string `json:"problem"`
	Cause    string `json:"cause"`
	Count    int    `json:"count"`
	Machines int    `json:"machines"`
}

// FailureCauses counts completed work orders by problem and cause since a
// date — the Pareto that says what to fix first.
func (s *Store) FailureCauses(ctx context.Context, since time.Time, plant string) ([]FailureCount, error) {
	q := `
		SELECT wo.attrs->'failure'->>'problem', wo.attrs->'failure'->>'cause',
		       COUNT(*), COUNT(DISTINCT wo.asset_tag)
		FROM mes_work_orders wo
		LEFT JOIN mes_assets a ON a.tag = wo.asset_tag
		LEFT JOIN mes_sections sec ON sec.id = a.section_id
		LEFT JOIN mes_plants p ON p.id = sec.plant_id
		WHERE wo.status = 'completed' AND wo.completed_at >= $1
		  AND wo.attrs ? 'failure'
		  AND ($2 = '' OR p.code = $2)
		GROUP BY 1, 2
		ORDER BY 3 DESC, 1, 2`
	rows, err := s.pool.Query(ctx, q, since, strings.TrimSpace(plant))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FailureCount{}
	for rows.Next() {
		var f FailureCount
		if err := rows.Scan(&f.Problem, &f.Cause, &f.Count, &f.Machines); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

/* ─────────────────────────────── approvals ─────────────────────────────── */

// approvalThresholdFor reads the factory's threshold (attrs.approval_threshold,
// set through PATCH /plants). Zero or absent means nothing needs approval.
func (s *Store) approvalThresholdFor(ctx context.Context, assetTag string) float64 {
	var raw *string
	_ = s.pool.QueryRow(ctx, `
		SELECT p.attrs->>'approval_threshold'
		FROM mes_assets a
		JOIN mes_sections sec ON sec.id = a.section_id
		JOIN mes_plants p ON p.id = sec.plant_id
		WHERE a.tag = $1`, assetTag).Scan(&raw)
	if raw == nil {
		return 0
	}
	var v float64
	if _, err := fmt.Sscan(*raw, &v); err != nil {
		return 0
	}
	return v
}

func estimatedCostOf(attrs map[string]any) float64 {
	switch v := attrs["estimatedCost"].(type) {
	case float64:
		return v
	case string:
		var f float64
		if _, err := fmt.Sscan(strings.ReplaceAll(v, ",", ""), &f); err == nil {
			return f
		}
	}
	return 0
}

// ApplyApprovalRule marks a new work order as needing approval when its
// estimate reaches the factory's threshold.
func (s *Store) ApplyApprovalRule(ctx context.Context, wo *WorkOrder, requestedBy string) {
	threshold := s.approvalThresholdFor(ctx, wo.AssetTag)
	if threshold <= 0 {
		return
	}
	estimate := estimatedCostOf(wo.Attrs)
	if estimate < threshold {
		return
	}
	if wo.Attrs == nil {
		wo.Attrs = map[string]any{}
	}
	wo.Attrs["approval"] = map[string]any{
		"state":        "pending",
		"requested_by": requestedBy,
		"estimate":     estimate,
		"threshold":    threshold,
	}
}

func approvalState(attrs map[string]any) string {
	a, _ := attrs["approval"].(map[string]any)
	state, _ := a["state"].(string)
	return state
}

// DecideApproval approves or rejects a pending work order. Four eyes: the
// person who raised it cannot approve it.
func (s *Store) DecideApproval(ctx context.Context, num string, approve bool, reason, actor string) (*WorkOrder, error) {
	wo, err := s.GetWorkOrder(ctx, num)
	if err != nil {
		return nil, err
	}
	a, _ := wo.Attrs["approval"].(map[string]any)
	if a == nil || a["state"] != "pending" {
		return nil, fmt.Errorf("%w: work order %s is not waiting for approval", ErrConflict, num)
	}
	if by, _ := a["requested_by"].(string); by != "" && strings.EqualFold(by, actor) {
		return nil, fmt.Errorf("%w: the person who raised a work order cannot approve it", ErrConflict)
	}
	reason = strings.TrimSpace(reason)
	if !approve && reason == "" {
		return nil, fmt.Errorf("%w: say why the work is not approved", ErrBadInput)
	}
	a["state"] = map[bool]string{true: "approved", false: "rejected"}[approve]
	a["decided_by"] = actor
	a["decided_at"] = time.Now().UTC().Format(time.RFC3339)
	if reason != "" {
		a["reason"] = reason
	}
	patch := WorkOrderPatch{Attrs: map[string]any{"approval": a}}
	if !approve {
		cancelled := "cancelled"
		patch.Status = &cancelled
	}
	return s.PatchWorkOrder(ctx, num, patch)
}

// CheckStart refuses to start work that is still waiting for approval.
func CheckStart(wo *WorkOrder) error {
	switch approvalState(wo.Attrs) {
	case "pending":
		return fmt.Errorf("%w: work order %s is waiting for approval", ErrConflict, wo.Num)
	case "rejected":
		return fmt.Errorf("%w: work order %s was not approved", ErrConflict, wo.Num)
	}
	return nil
}

// CheckCompletion says what a work order still needs before it can be
// completed: approval, every checklist step marked and the technician's
// sign-off, and for a breakdown or corrective job, its failure code. A
// commissioning job needs its acceptance tests passed and its handover signed
// instead.
func CheckCompletion(wo *WorkOrder) error {
	if err := CheckStart(wo); err != nil {
		return err
	}
	var missing []string
	if isType(wo, WOTypeCommissioning) {
		// Tests and a three-party handover stand in for the checklist and
		// the technician's sign-off (installation.go).
		missing = checkCommissioning(wo)
	} else if steps := checklistSteps(wo.Attrs); len(steps) > 0 {
		results := checklistResultsOf(wo.Attrs)
		open := 0
		for i := range steps {
			if r, ok := results[i]; !ok || r["result"] == nil || r["result"] == "" {
				open++
			}
		}
		if open > 0 {
			missing = append(missing, fmt.Sprintf("%d of %d checklist steps unmarked", open, len(steps)))
		}
		signoff, _ := wo.Attrs["signoff"].(map[string]any)
		if _, ok := signoff["technician"].(map[string]any); !ok {
			missing = append(missing, "the technician's sign-off")
		}
	}
	if slices.Contains(FailureCodedTypes, strings.ToLower(wo.WOType)) {
		if f, _ := wo.Attrs["failure"].(map[string]any); f == nil {
			missing = append(missing, "the failure code (problem, cause, remedy)")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: work order %s still needs %s", ErrConflict, wo.Num, strings.Join(missing, "; "))
	}
	return nil
}

/* ───────────────────────────── running hours ───────────────────────────── */

// MeterMetric is the telemetry metric a running-hours PM interval is measured
// in. Readings come from job cards, the meter-reading form, or a sensor.
const MeterMetric = "running_hours"

type MeterReadingInput struct {
	Value      float64    `json:"value"`
	RecordedAt *time.Time `json:"recorded_at"`
	// Reset accepts a reading below the last one: the meter was replaced or
	// rolled over. Without it a lower reading is refused as a typo.
	Reset bool `json:"reset"`
}

func (s *Store) latestMeter(ctx context.Context, tag string, at *time.Time) (*TelemetryPoint, error) {
	var p TelemetryPoint
	q := `SELECT asset_tag, metric, value, unit, recorded_at FROM mes_telemetry_timeseries
	      WHERE asset_tag = $1 AND metric = $2`
	args := []any{tag, MeterMetric}
	if at != nil {
		q += ` AND recorded_at <= $3`
		args = append(args, *at)
	}
	q += ` ORDER BY recorded_at DESC LIMIT 1`
	err := s.pool.QueryRow(ctx, q, args...).Scan(&p.AssetTag, &p.Metric, &p.Value, &p.Unit, &p.RecordedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) RecordMeterReading(ctx context.Context, tag string, in MeterReadingInput) (*TelemetryPoint, error) {
	if _, err := s.GetAssetByTag(ctx, tag); err != nil {
		return nil, err
	}
	if in.Value < 0 || math.IsNaN(in.Value) || math.IsInf(in.Value, 0) {
		return nil, fmt.Errorf("%w: a meter reading is zero or more", ErrBadInput)
	}
	at := time.Now().UTC()
	if in.RecordedAt != nil {
		at = in.RecordedAt.UTC()
	}
	if at.After(time.Now().Add(5 * time.Minute)) {
		return nil, fmt.Errorf("%w: a reading cannot be in the future", ErrBadInput)
	}
	prev, err := s.latestMeter(ctx, tag, &at)
	if err != nil {
		return nil, err
	}
	if prev != nil && in.Value < prev.Value && !in.Reset {
		return nil, fmt.Errorf("%w: %.1f h is below the last reading (%.1f h on %s); if the meter was replaced, send reset",
			ErrBadInput, in.Value, prev.Value, prev.RecordedAt.Format("2006-01-02"))
	}
	p := TelemetryPoint{AssetTag: tag, Metric: MeterMetric, Value: in.Value, Unit: "h", RecordedAt: at}
	if err := s.InsertTelemetryPoint(ctx, p); err != nil {
		return nil, err
	}
	return &p, nil
}

// MeterStatus is how far a schedule is through its running-hours interval.
type MeterStatus struct {
	ScheduleID    uuid.UUID  `json:"schedule_id"`
	AssetTag      string     `json:"asset_tag"`
	IntervalHours float64    `json:"interval_hours"`
	CurrentHours  *float64   `json:"current_hours,omitempty"`
	BaselineHours *float64   `json:"baseline_hours,omitempty"`
	UsedHours     *float64   `json:"used_hours,omitempty"`
	ReadAt        *time.Time `json:"read_at,omitempty"`
	// State: no_reading, ok, due_soon (≥ 90 %), due (≥ 100 %).
	State string `json:"state"`
}

func intervalHoursOf(t PMTemplate) float64 {
	switch v := t.Attrs["interval_hours"].(type) {
	case float64:
		return v
	case string:
		var f float64
		if _, err := fmt.Sscan(v, &f); err == nil {
			return f
		}
	}
	return 0
}

// MeterStatusFor measures a schedule against its template's running-hours
// interval: the hours run since the reading at (or just before) the last
// service. With no service yet, the first reading is the baseline.
func (s *Store) MeterStatusFor(ctx context.Context, sch PMSchedule, tpl PMTemplate) (*MeterStatus, error) {
	interval := intervalHoursOf(tpl)
	if interval <= 0 {
		return nil, nil
	}
	st := &MeterStatus{ScheduleID: sch.ID, AssetTag: sch.AssetTag, IntervalHours: interval, State: "no_reading"}
	cur, err := s.latestMeter(ctx, sch.AssetTag, nil)
	if err != nil || cur == nil {
		return st, err
	}
	st.CurrentHours = &cur.Value
	st.ReadAt = &cur.RecordedAt
	var base *TelemetryPoint
	if sch.LastDoneAt != nil {
		base, err = s.latestMeter(ctx, sch.AssetTag, sch.LastDoneAt)
		if err != nil {
			return st, err
		}
	}
	if base == nil {
		var p TelemetryPoint
		err := s.pool.QueryRow(ctx, `
			SELECT asset_tag, metric, value, unit, recorded_at FROM mes_telemetry_timeseries
			WHERE asset_tag = $1 AND metric = $2 AND ($3::timestamptz IS NULL OR recorded_at >= $3)
			ORDER BY recorded_at ASC LIMIT 1`, sch.AssetTag, MeterMetric, sch.LastDoneAt).Scan(
			&p.AssetTag, &p.Metric, &p.Value, &p.Unit, &p.RecordedAt)
		if err == nil {
			base = &p
		}
	}
	if base == nil {
		return st, nil
	}
	used := cur.Value - base.Value
	if used < 0 { // the meter was reset since; count from the reset
		used = cur.Value
	}
	st.BaselineHours = &base.Value
	st.UsedHours = &used
	switch {
	case used >= interval:
		st.State = "due"
	case used >= 0.9*interval:
		st.State = "due_soon"
	default:
		st.State = "ok"
	}
	return st, nil
}

// ListMeterStatus measures every schedule whose template has a running-hours
// interval.
func (s *Store) ListMeterStatus(ctx context.Context) ([]MeterStatus, error) {
	schedules, err := s.ListPMSchedules(ctx, "")
	if err != nil {
		return nil, err
	}
	templates, err := s.ListPMTemplates(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]PMTemplate{}
	for _, t := range templates {
		byID[t.ID] = t
	}
	out := []MeterStatus{}
	for _, sch := range schedules {
		tpl, ok := byID[sch.TemplateID]
		if !ok {
			continue
		}
		st, err := s.MeterStatusFor(ctx, sch, tpl)
		if err != nil {
			return nil, err
		}
		if st != nil {
			out = append(out, *st)
		}
	}
	return out, nil
}

// MarkScheduleOverdue flags a schedule whose meter interval has run out.
// SyncPMScheduleStatuses only looks at dates.
func (s *Store) MarkScheduleOverdue(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE mes_pm_schedules SET status = 'overdue', updated_at = NOW() WHERE id = $1 AND status = 'scheduled'`, id)
	return err
}
