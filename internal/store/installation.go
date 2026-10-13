package store

// Installation and commissioning: putting a machine into service.
//
// Two work order types, kept as wo_type like any other (the column is free
// text, so no migration):
//   - installation — the machine is set in place, connected and run up. On
//     completion the machine's installedOn is set and a commissioning job is
//     raised for it.
//   - commissioning — acceptance tests against the specification, then a
//     handover signed by three parties. On completion the machine is in
//     service and its warranty starts.
//
// An acceptance test is a checklist step that may carry limits
// ({"step", "min", "max", "unit"}), so the paper sheet, the Enter results
// screen and RecordChecklist all handle it the way they handle any checklist.
// What commissioning adds is that a reading outside its limits cannot be
// marked a pass, no test may be left failed, and the handover replaces the
// technician's sign-off.
//
// Commissioning checklists come from templates with attrs.kind
// "commissioning", matched to the machine's category. They have no interval
// and cannot be scheduled.
//
// The machine's state lives in its attrs (serviceState: installing →
// commissioning → in_service) because mes_assets.status is CHECK-constrained
// and says what the machine is doing, not whether it has been accepted.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"iag-mes/backend/internal/events"
)

const (
	WOTypeInstallation  = "installation"
	WOTypeCommissioning = "commissioning"

	// TemplateKindCommissioning marks a PM template that is a commissioning
	// checklist rather than recurring maintenance.
	TemplateKindCommissioning = "commissioning"

	// AudienceHandovers hears when a machine is accepted into service.
	AudienceHandovers = "maintenance.handovers"
)

// HandoverParties sign a commissioning handover, in the order they usually
// do: whoever installed it, the engineer who accepts it, and production, who
// will run it.
var HandoverParties = []string{"installer", "engineering", "production"}

// acceptingParties are the two who accept the result. They sign only once
// every test is marked and none has failed.
var acceptingParties = []string{"engineering", "production"}

func isType(wo *WorkOrder, t string) bool {
	return strings.EqualFold(strings.TrimSpace(wo.WOType), t)
}

// IsCommissioningTemplate says whether a template is a commissioning
// checklist.
func IsCommissioningTemplate(t PMTemplate) bool {
	kind, _ := t.Attrs["kind"].(string)
	return strings.EqualFold(strings.TrimSpace(kind), TemplateKindCommissioning)
}

/* ───────────────────────────── acceptance limits ───────────────────────────── */

type stepLimits struct {
	Min, Max *float64
	Unit     string
}

func (l stepLimits) any() bool { return l.Min != nil || l.Max != nil }

func (l stepLimits) holds(v float64) bool {
	return (l.Min == nil || v >= *l.Min) && (l.Max == nil || v <= *l.Max)
}

func (l stepLimits) String() string {
	unit := ""
	if l.Unit != "" {
		unit = " " + l.Unit
	}
	switch {
	case l.Min != nil && l.Max != nil:
		return fmt.Sprintf("%g–%g%s", *l.Min, *l.Max, unit)
	case l.Min != nil:
		return fmt.Sprintf("at least %g%s", *l.Min, unit)
	case l.Max != nil:
		return fmt.Sprintf("at most %g%s", *l.Max, unit)
	}
	return ""
}

func numberAttr(v any) *float64 {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil
		}
		return &n
	case string:
		s := strings.TrimSpace(strings.ReplaceAll(n, ",", ""))
		if s == "" {
			return nil
		}
		var f float64
		if _, err := fmt.Sscan(s, &f); err == nil {
			return &f
		}
	}
	return nil
}

// checklistLimits reads the limits of each step, in the order checklistSteps
// returns them. A plain-string step has none.
func checklistLimits(attrs map[string]any) []stepLimits {
	raw, _ := attrs["checklist"].([]any)
	out := make([]stepLimits, 0, len(raw))
	for _, item := range raw {
		switch v := item.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				out = append(out, stepLimits{})
			}
		case map[string]any:
			text := ""
			for _, k := range []string{"step", "text", "title", "name"} {
				if t, _ := v[k].(string); strings.TrimSpace(t) != "" {
					text = t
					break
				}
			}
			if text == "" {
				continue
			}
			unit, _ := v["unit"].(string)
			out = append(out, stepLimits{Min: numberAttr(v["min"]), Max: numberAttr(v["max"]), Unit: strings.TrimSpace(unit)})
		}
	}
	return out
}

// checkAcceptance refuses a pass the reading contradicts, and on a
// commissioning job a pass with no reading where the step has limits: the
// test is the measurement.
func checkAcceptance(wo *WorkOrder, e ChecklistEntry, result string) error {
	limits := checklistLimits(wo.Attrs)
	if e.Index < 0 || e.Index >= len(limits) || !limits[e.Index].any() {
		return nil
	}
	l := limits[e.Index]
	if e.Reading == nil {
		if result == "pass" && isType(wo, WOTypeCommissioning) {
			return fmt.Errorf("%w: test %d needs the measured value (%s)", ErrBadInput, e.Index+1, l)
		}
		return nil
	}
	if result == "pass" && !l.holds(*e.Reading) {
		return fmt.Errorf("%w: test %d read %g, outside %s — mark it fail", ErrBadInput, e.Index+1, *e.Reading, l)
	}
	return nil
}

/* ─────────────────────────────── handover ─────────────────────────────── */

type HandoverInput struct {
	PaperSignature
	// Company is the installer's firm, when a contractor did the work.
	Company string `json:"company"`
}

func handoverOf(attrs map[string]any) map[string]any {
	h, _ := attrs["handover"].(map[string]any)
	if h == nil {
		h = map[string]any{}
	}
	return h
}

// failedOrOpen counts checklist steps not yet marked and steps marked fail.
func failedOrOpen(attrs map[string]any) (open, failed int) {
	steps := checklistSteps(attrs)
	results := checklistResultsOf(attrs)
	for i := range steps {
		r, ok := results[i]
		switch {
		case !ok || r["result"] == nil || r["result"] == "":
			open++
		case r["result"] == "fail":
			failed++
		}
	}
	return open, failed
}

// SignHandover records one party's signature on a commissioning handover.
// The three signers must be three different people; engineering and
// production accept a result, so they sign only once every test has passed
// or been marked not applicable.
func (s *Store) SignHandover(ctx context.Context, num, party, actor string, in HandoverInput) (*WorkOrder, error) {
	party = strings.ToLower(strings.TrimSpace(party))
	if !slices.Contains(HandoverParties, party) {
		return nil, fmt.Errorf("%w: a handover is signed by %s", ErrBadInput, strings.Join(HandoverParties, ", "))
	}
	wo, err := s.GetWorkOrder(ctx, num)
	if err != nil {
		return nil, err
	}
	if !isType(wo, WOTypeCommissioning) {
		return nil, fmt.Errorf("%w: %s is not a commissioning job; only commissioning has a handover", ErrBadInput, num)
	}
	if wo.Status == "completed" || wo.Status == "cancelled" {
		return nil, fmt.Errorf("%w: work order %s is already %s", ErrConflict, num, wo.Status)
	}
	if slices.Contains(acceptingParties, party) {
		steps := checklistSteps(wo.Attrs)
		if len(steps) == 0 {
			return nil, fmt.Errorf("%w: %s has no acceptance tests to accept", ErrConflict, num)
		}
		open, failed := failedOrOpen(wo.Attrs)
		if open > 0 {
			return nil, fmt.Errorf("%w: %d of %d acceptance tests are not marked yet", ErrConflict, open, len(steps))
		}
		if failed > 0 {
			return nil, fmt.Errorf("%w: %d acceptance test(s) failed — fix and re-test before accepting the machine", ErrConflict, failed)
		}
	}
	entry, err := in.PaperSignature.entry(actor, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	signer, _ := entry["by"].(string)
	handover := handoverOf(wo.Attrs)
	for _, other := range HandoverParties {
		if other == party {
			continue
		}
		prev, _ := handover[other].(map[string]any)
		if by, _ := prev["by"].(string); by != "" && strings.EqualFold(by, signer) {
			return nil, fmt.Errorf("%w: %s already signed as %s — each party is a different person", ErrConflict, signer, other)
		}
	}
	if c := strings.TrimSpace(in.Company); c != "" {
		entry["company"] = c
	}
	handover[party] = entry
	return s.PatchWorkOrder(ctx, num, WorkOrderPatch{Attrs: map[string]any{"handover": handover}})
}

// withdrawAcceptance drops the engineering and production signatures. A test
// result that changes after they signed is not the result they accepted.
func withdrawAcceptance(attrs map[string]any) (map[string]any, bool) {
	handover := handoverOf(attrs)
	changed := false
	for _, p := range acceptingParties {
		if _, ok := handover[p]; ok {
			delete(handover, p)
			changed = true
		}
	}
	return handover, changed
}

// checkCommissioning is what a commissioning job needs before completion, on
// top of approval: tests to run, all marked, none failed, and the handover
// signed by all three.
func checkCommissioning(wo *WorkOrder) []string {
	var missing []string
	steps := checklistSteps(wo.Attrs)
	if len(steps) == 0 {
		missing = append(missing, "acceptance tests (add them to the checklist)")
	} else {
		open, failed := failedOrOpen(wo.Attrs)
		if open > 0 {
			missing = append(missing, fmt.Sprintf("%d of %d acceptance tests unmarked", open, len(steps)))
		}
		if failed > 0 {
			missing = append(missing, fmt.Sprintf("%d failed acceptance test(s) re-tested", failed))
		}
	}
	handover := handoverOf(wo.Attrs)
	var unsigned []string
	for _, p := range HandoverParties {
		if _, ok := handover[p].(map[string]any); !ok {
			unsigned = append(unsigned, p)
		}
	}
	if len(unsigned) > 0 {
		missing = append(missing, "the handover signed by "+strings.Join(unsigned, ", "))
	}
	return missing
}

/* ───────────────────────── raising and completing ───────────────────────── */

// commissioningTemplateFor finds the commissioning checklist for a machine
// category: an exact category match first, then one with no category.
func (s *Store) commissioningTemplateFor(ctx context.Context, category string) (*PMTemplate, error) {
	templates, err := s.ListPMTemplates(ctx)
	if err != nil {
		return nil, err
	}
	var general *PMTemplate
	for i := range templates {
		t := templates[i]
		if !IsCommissioningTemplate(t) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(t.AssetCategory), strings.TrimSpace(category)) && category != "" {
			return &t, nil
		}
		if strings.TrimSpace(t.AssetCategory) == "" && general == nil {
			general = &t
		}
	}
	return general, nil
}

// PrepareInstallationWork fills in a new installation or commissioning job
// before it is saved: a commissioning job with no tests takes the checklist
// of the template for its machine's category (or the one named in
// attrs.commissioning_template).
func (s *Store) PrepareInstallationWork(ctx context.Context, wo *WorkOrder) error {
	if !isType(wo, WOTypeCommissioning) {
		return nil
	}
	if wo.Attrs == nil {
		wo.Attrs = map[string]any{}
	}
	if steps := checklistSteps(wo.Attrs); len(steps) > 0 {
		return nil
	}
	var tpl *PMTemplate
	if code, _ := wo.Attrs["commissioning_template"].(string); strings.TrimSpace(code) != "" {
		templates, err := s.ListPMTemplates(ctx)
		if err != nil {
			return err
		}
		for i := range templates {
			if strings.EqualFold(templates[i].Code, strings.TrimSpace(code)) {
				tpl = &templates[i]
				break
			}
		}
		if tpl == nil || !IsCommissioningTemplate(*tpl) {
			return fmt.Errorf("%w: no commissioning template %q", ErrBadInput, code)
		}
	} else {
		asset, err := s.GetAssetByTag(ctx, wo.AssetTag)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("%w: no machine %q", ErrBadInput, wo.AssetTag)
			}
			return err
		}
		if tpl, err = s.commissioningTemplateFor(ctx, asset.Category); err != nil {
			return err
		}
	}
	if tpl != nil {
		wo.Attrs["checklist"] = tpl.Checklist
		wo.Attrs["commissioning_template"] = tpl.Code
	}
	return nil
}

// mergeAssetAttrs sets keys on a machine's attrs without touching the rest.
func (s *Store) mergeAssetAttrs(ctx context.Context, tag string, patch map[string]any) error {
	raw, _ := json.Marshal(patch)
	tagged, err := s.pool.Exec(ctx, `UPDATE mes_assets SET attrs = COALESCE(attrs, '{}'::jsonb) || $2::jsonb, updated_at = NOW() WHERE tag = $1`, tag, raw)
	if err != nil {
		return err
	}
	if tagged.RowsAffected() == 0 {
		return ErrNotFound
	}
	if updated, err := s.GetAssetByTag(ctx, tag); err == nil {
		s.publishAsset(ctx, events.TypeAssetUpdated, updated)
	}
	return nil
}

// MarkInstallationStarted records that a machine is being put in, when an
// installation or commissioning job is raised for a machine not yet in
// service.
func (s *Store) MarkInstallationStarted(ctx context.Context, wo *WorkOrder) {
	var state string
	switch {
	case isType(wo, WOTypeInstallation):
		state = "installing"
	case isType(wo, WOTypeCommissioning):
		state = "commissioning"
	default:
		return
	}
	asset, err := s.GetAssetByTag(ctx, wo.AssetTag)
	if err != nil {
		return
	}
	if cur, _ := asset.Attrs["serviceState"].(string); cur == "in_service" && state == "commissioning" {
		// Re-commissioning a machine already in service (after a rebuild, say)
		// does not take it out of service on paper until the job says so.
		return
	}
	_ = s.mergeAssetAttrs(ctx, wo.AssetTag, map[string]any{"serviceState": state})
}

func (s *Store) openWorkOrderOfType(ctx context.Context, tag, woType string) (string, error) {
	var num string
	err := s.pool.QueryRow(ctx, `
		SELECT num FROM mes_work_orders
		WHERE asset_tag = $1 AND lower(wo_type) = $2 AND status NOT IN ('completed', 'cancelled')
		ORDER BY created_at DESC LIMIT 1`, tag, woType).Scan(&num)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return num, err
}

// afterInstallationWork runs once an installation or commissioning job is
// completed. It is safe to run twice: each step checks before it writes.
func (s *Store) afterInstallationWork(ctx context.Context, wo *WorkOrder) {
	done := time.Now().UTC()
	if wo.CompletedAt != nil {
		done = wo.CompletedAt.UTC()
	}
	day := done.Format("2006-01-02")
	asset, err := s.GetAssetByTag(ctx, wo.AssetTag)
	if err != nil {
		return
	}
	switch {
	case isType(wo, WOTypeInstallation):
		patch := map[string]any{"installationWO": wo.Num}
		if v, _ := asset.Attrs["installedOn"].(string); strings.TrimSpace(v) == "" {
			patch["installedOn"] = day
		}
		if v, _ := asset.Attrs["serviceState"].(string); v != "in_service" {
			patch["serviceState"] = "commissioning"
		}
		_ = s.mergeAssetAttrs(ctx, wo.AssetTag, patch)
		if open, err := s.openWorkOrderOfType(ctx, wo.AssetTag, WOTypeCommissioning); err != nil || open != "" {
			return
		}
		num, err := s.NextWorkOrderNum(ctx)
		if err != nil {
			return
		}
		attrs := map[string]any{"installation_wo": wo.Num, "raised_by": "installation " + wo.Num}
		for _, k := range []string{"project_ref", "contractor", "warranty_months"} {
			if v, ok := wo.Attrs[k]; ok {
				attrs[k] = v
			}
		}
		next := &WorkOrder{
			Num:      num,
			Title:    "Commission " + asset.Name,
			AssetTag: wo.AssetTag,
			WOType:   WOTypeCommissioning,
			Priority: wo.Priority,
			Status:   "open",
			Assignee: wo.Assignee,
			Attrs:    attrs,
		}
		if err := s.PrepareInstallationWork(ctx, next); err != nil {
			return
		}
		_, _ = s.CreateWorkOrder(ctx, *next)
	case isType(wo, WOTypeCommissioning):
		patch := map[string]any{"serviceState": "in_service", "commissionedOn": day, "commissioningWO": wo.Num}
		if v, _ := asset.Attrs["installedOn"].(string); strings.TrimSpace(v) == "" {
			patch["installedOn"] = day
		}
		if months := numberAttr(wo.Attrs["warranty_months"]); months != nil && *months > 0 {
			if v, _ := asset.Attrs["warrantyUntil"].(string); strings.TrimSpace(v) == "" {
				patch["warrantyUntil"] = done.AddDate(0, int(*months), 0).Format("2006-01-02")
			}
		}
		_ = s.mergeAssetAttrs(ctx, wo.AssetTag, patch)
		handover := handoverOf(wo.Attrs)
		names := []string{}
		for _, p := range HandoverParties {
			if e, _ := handover[p].(map[string]any); e != nil {
				if by, _ := e["by"].(string); by != "" {
					names = append(names, fmt.Sprintf("%s (%s)", by, p))
				}
			}
		}
		s.Notify(ctx, Notice{
			Audience: AudienceHandovers,
			Title:    fmt.Sprintf("%s is in service", asset.Name),
			Message:  fmt.Sprintf("%s (%s) was commissioned on %s under %s. Handover: %s.", asset.Name, asset.Tag, day, wo.Num, strings.Join(names, ", ")),
			Key:      wo.AssetTag,
		})
	}
}
