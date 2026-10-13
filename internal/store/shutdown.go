package store

// Shutdowns: a planned stoppage of some of a factory's machines, and the
// maintenance done while they are stopped.
//
// Nothing needs a migration. A shutdown is three things that already exist:
//   - a header, kept in the factory's attrs (attrs.shutdowns.<code>): the
//     window, who leads it, the machines, production's agreement, its state;
//   - one planned stop per machine (mes_downtime_events, state scheduled —
//     013), tagged attrs.shutdown = code, so production sees the stop as
//     planned before it happens and the downtime log records it as it does;
//   - the work orders done in it, tagged attrs.shutdown = code.
//
// planned → in_progress → completed, or planned → cancelled. Starting it
// starts every stop (the machines go down); finishing it ends them. Jobs not
// finished stay open, still tagged, and the finish says how many.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AudienceShutdowns hears when a shutdown is planned, starts and ends —
// maintenance and production both need it.
const AudienceShutdowns = "maintenance.shutdowns"

// ShutdownCategory is the downtime category a shutdown's stops are logged
// under: planned, not a breakdown.
const ShutdownCategory = "planned_maintenance"

// MaxShutdownDays bounds one shutdown's window.
const MaxShutdownDays = 60

var ShutdownStates = []string{"planned", "in_progress", "completed", "cancelled"}

type Shutdown struct {
	Code             string         `json:"code"`
	PlantCode        string         `json:"plant_code"`
	Title            string         `json:"title"`
	StartsAt         time.Time      `json:"starts_at"`
	EndsAt           time.Time      `json:"ends_at"`
	Lead             string         `json:"lead"`
	Notes            string         `json:"notes,omitempty"`
	Status           string         `json:"status"`
	Machines         []string       `json:"machines"`
	ProductionAgreed map[string]any `json:"production_agreed,omitempty"`
	CreatedBy        string         `json:"created_by"`
	CreatedAt        time.Time      `json:"created_at"`
	StartedAt        *time.Time     `json:"started_at,omitempty"`
	FinishedAt       *time.Time     `json:"finished_at,omitempty"`
	// Counts, filled on reads.
	Jobs     int `json:"jobs"`
	OpenJobs int `json:"open_jobs"`
}

type ShutdownDetail struct {
	Shutdown
	Stops      []DowntimeEvent `json:"stops"`
	WorkOrders []WorkOrder     `json:"work_orders"`
}

type ShutdownInput struct {
	Plant    string    `json:"plant"`
	Title    string    `json:"title"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Lead     string    `json:"lead"`
	Notes    string    `json:"notes"`
	Machines []string  `json:"machines"`
}

type ShutdownPatch struct {
	Title    *string    `json:"title"`
	StartsAt *time.Time `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`
	Lead     *string    `json:"lead"`
	Notes    *string    `json:"notes"`
	Machines *[]string  `json:"machines"`
}

/* ─────────────────────────────── storage ─────────────────────────────── */

func (s *Store) saveShutdown(ctx context.Context, sd *Shutdown) error {
	stored := *sd
	stored.Jobs, stored.OpenJobs = 0, 0
	raw, _ := json.Marshal(stored)
	tag, err := s.pool.Exec(ctx, `
		UPDATE mes_plants
		   SET attrs = jsonb_set(COALESCE(attrs, '{}'::jsonb), '{shutdowns}',
		               COALESCE(attrs->'shutdowns', '{}'::jsonb) || jsonb_build_object($2::text, $3::jsonb)),
		       updated_at = NOW()
		 WHERE code = $1`, sd.PlantCode, sd.Code, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: no factory %q", ErrBadInput, sd.PlantCode)
	}
	return nil
}

func (s *Store) getShutdown(ctx context.Context, code string) (*Shutdown, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT attrs->'shutdowns'->$1 FROM mes_plants
		 WHERE attrs->'shutdowns' ? $1 LIMIT 1`, code).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var sd Shutdown
	if err := json.Unmarshal(raw, &sd); err != nil {
		return nil, err
	}
	return &sd, nil
}

func (s *Store) listShutdowns(ctx context.Context, plant string) ([]Shutdown, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT v FROM mes_plants p, jsonb_each(COALESCE(p.attrs->'shutdowns', '{}'::jsonb)) AS e(k, v)
		 WHERE ($1 = '' OR p.code = $1)`, strings.TrimSpace(plant))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Shutdown{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var sd Shutdown
		if json.Unmarshal(raw, &sd) == nil && sd.Code != "" {
			out = append(out, sd)
		}
	}
	return out, rows.Err()
}

func (s *Store) shutdownStops(ctx context.Context, code string) ([]DowntimeEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+downtimeColumns+` FROM mes_downtime_events
		 WHERE attrs->>'shutdown' = $1 ORDER BY asset_tag, created_at`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DowntimeEvent{}
	for rows.Next() {
		var d DowntimeEvent
		var attrs []byte
		if err := rows.Scan(&d.ID, &d.AssetTag, &d.Category, &d.Reason, &d.State, &d.StartedAt, &d.EndedAt,
			&d.PlannedStart, &d.PlannedEnd, &d.KgLost, &d.OperatorRef, &attrs, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Attrs = scanAttrs(attrs)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) shutdownWorkOrders(ctx context.Context, code string) ([]WorkOrder, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, num, title, asset_tag, wo_type, priority, status, assignee, due_at, completed_at, attrs, created_at, updated_at
		  FROM mes_work_orders WHERE attrs->>'shutdown' = $1 ORDER BY asset_tag, num`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWorkOrders(rows)
}

func isLive(wo WorkOrder) bool { return wo.Status != "completed" && wo.Status != "cancelled" }

/* ─────────────────────────────── reads ─────────────────────────────── */

// ListShutdowns is every shutdown, newest window first, with its job counts.
func (s *Store) ListShutdowns(ctx context.Context, plant string) ([]Shutdown, error) {
	list, err := s.listShutdowns(ctx, plant)
	if err != nil {
		return nil, err
	}
	counts := map[string][2]int{}
	rows, err := s.pool.Query(ctx, `
		SELECT attrs->>'shutdown', COUNT(*), COUNT(*) FILTER (WHERE status NOT IN ('completed', 'cancelled'))
		  FROM mes_work_orders WHERE COALESCE(attrs->>'shutdown', '') <> '' GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var code string
		var all, open int
		if err := rows.Scan(&code, &all, &open); err != nil {
			rows.Close()
			return nil, err
		}
		counts[code] = [2]int{all, open}
	}
	rows.Close()
	for i := range list {
		c := counts[list[i].Code]
		list[i].Jobs, list[i].OpenJobs = c[0], c[1]
	}
	slices.SortStableFunc(list, func(a, b Shutdown) int { return b.StartsAt.Compare(a.StartsAt) })
	return list, nil
}

func (s *Store) GetShutdown(ctx context.Context, code string) (*ShutdownDetail, error) {
	sd, err := s.getShutdown(ctx, code)
	if err != nil {
		return nil, err
	}
	stops, err := s.shutdownStops(ctx, code)
	if err != nil {
		return nil, err
	}
	wos, err := s.shutdownWorkOrders(ctx, code)
	if err != nil {
		return nil, err
	}
	for _, wo := range wos {
		sd.Jobs++
		if isLive(wo) {
			sd.OpenJobs++
		}
	}
	return &ShutdownDetail{Shutdown: *sd, Stops: stops, WorkOrders: wos}, nil
}

/* ─────────────────────────────── planning ─────────────────────────────── */

func checkWindow(start, end time.Time) error {
	if start.IsZero() || end.IsZero() {
		return fmt.Errorf("%w: a shutdown needs a start and an end (starts_at, ends_at)", ErrBadInput)
	}
	if !end.After(start) {
		return fmt.Errorf("%w: the shutdown must end after it starts", ErrBadInput)
	}
	if end.Sub(start) > MaxShutdownDays*24*time.Hour {
		return fmt.Errorf("%w: a shutdown is at most %d days", ErrBadInput, MaxShutdownDays)
	}
	return nil
}

// checkMachines resolves the machines at the factory, refusing an unknown or
// retired one, one at another factory, and one already in another shutdown
// whose window overlaps.
func (s *Store) checkMachines(ctx context.Context, plant string, tags []string, start, end time.Time, self string) ([]string, error) {
	if len(tags) == 0 {
		return nil, fmt.Errorf("%w: pick the machines the shutdown stops", ErrBadInput)
	}
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range tags {
		tag := strings.TrimSpace(raw)
		if tag == "" || seen[strings.ToUpper(tag)] {
			continue
		}
		seen[strings.ToUpper(tag)] = true
		a, err := s.GetAssetByTag(ctx, tag)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("%w: no machine %q", ErrBadInput, tag)
			}
			return nil, err
		}
		if a.PlantCode != plant {
			return nil, fmt.Errorf("%w: %s is at %s, not %s", ErrBadInput, a.Tag, a.PlantCode, plant)
		}
		if a.Status == "retired" {
			return nil, fmt.Errorf("%w: %s is retired", ErrBadInput, a.Tag)
		}
		out = append(out, a.Tag)
	}
	others, err := s.listShutdowns(ctx, plant)
	if err != nil {
		return nil, err
	}
	for _, o := range others {
		if o.Code == self || (o.Status != "planned" && o.Status != "in_progress") {
			continue
		}
		if !o.StartsAt.Before(end) || !start.Before(o.EndsAt) {
			continue
		}
		for _, tag := range out {
			if slices.Contains(o.Machines, tag) {
				return nil, fmt.Errorf("%w: %s is already stopped then for %s (%s)", ErrConflict, tag, o.Code, o.Title)
			}
		}
	}
	return out, nil
}

func (s *Store) nextShutdownCode(ctx context.Context, plant string, start time.Time) (string, error) {
	base := fmt.Sprintf("SD-%s-%s", strings.ToUpper(plant), start.UTC().Format("20060102"))
	code := base
	for i := 2; ; i++ {
		if _, err := s.getShutdown(ctx, code); errors.Is(err, ErrNotFound) {
			return code, nil
		} else if err != nil {
			return "", err
		}
		code = fmt.Sprintf("%s-%d", base, i)
	}
}

func (s *Store) planStop(ctx context.Context, sd *Shutdown, tag string) error {
	start, end := sd.StartsAt, sd.EndsAt
	_, err := s.CreateDowntimeEvent(ctx, DowntimeEvent{
		AssetTag:     tag,
		Category:     ShutdownCategory,
		Reason:       fmt.Sprintf("Shutdown %s: %s", sd.Code, sd.Title),
		State:        "scheduled",
		PlannedStart: &start,
		PlannedEnd:   &end,
		Attrs:        map[string]any{"shutdown": sd.Code},
	})
	return err
}

func shutdownWhen(sd *Shutdown) string {
	return fmt.Sprintf("%s to %s", sd.StartsAt.UTC().Format("2 Jan 15:04"), sd.EndsAt.UTC().Format("2 Jan 15:04 2006"))
}

// CreateShutdown plans a shutdown: the header, and a planned stop on each
// machine for the window.
func (s *Store) CreateShutdown(ctx context.Context, in ShutdownInput, actor string) (*ShutdownDetail, error) {
	plant := strings.TrimSpace(in.Plant)
	if plant == "" {
		return nil, fmt.Errorf("%w: say which factory (plant)", ErrBadInput)
	}
	if _, err := s.GetPlantByCode(ctx, plant); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: no factory %q", ErrBadInput, plant)
		}
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" || len(title) > 200 {
		return nil, fmt.Errorf("%w: give the shutdown a title of 1–200 characters", ErrBadInput)
	}
	start, end := in.StartsAt.UTC(), in.EndsAt.UTC()
	if err := checkWindow(start, end); err != nil {
		return nil, err
	}
	if end.Before(time.Now().UTC()) {
		return nil, fmt.Errorf("%w: that window is already over", ErrBadInput)
	}
	machines, err := s.checkMachines(ctx, plant, in.Machines, start, end, "")
	if err != nil {
		return nil, err
	}
	code, err := s.nextShutdownCode(ctx, plant, start)
	if err != nil {
		return nil, err
	}
	sd := &Shutdown{
		Code: code, PlantCode: plant, Title: title, StartsAt: start, EndsAt: end,
		Lead: strings.TrimSpace(in.Lead), Notes: strings.TrimSpace(in.Notes), Status: "planned",
		Machines: machines, CreatedBy: actor, CreatedAt: time.Now().UTC(),
	}
	if err := s.saveShutdown(ctx, sd); err != nil {
		return nil, err
	}
	for _, tag := range machines {
		if err := s.planStop(ctx, sd, tag); err != nil {
			return nil, fmt.Errorf("shutdown %s saved, but the stop on %s could not be planned: %w", code, tag, err)
		}
	}
	s.Notify(ctx, Notice{
		Audience: AudienceShutdowns,
		Title:    fmt.Sprintf("Shutdown planned at %s: %s", plant, title),
		Message:  fmt.Sprintf("%s, %s. Machines: %s. Lead: %s.", code, shutdownWhen(sd), strings.Join(machines, ", "), orNobody(sd.Lead)),
		Key:      code,
	})
	return s.GetShutdown(ctx, code)
}

func orNobody(v string) string {
	if v == "" {
		return "not named yet"
	}
	return v
}

// UpdateShutdown changes a planned shutdown. Moving the window moves every
// planned stop; adding a machine plans a stop on it; removing one cancels
// its stop and takes its open jobs out of the shutdown.
func (s *Store) UpdateShutdown(ctx context.Context, code string, patch ShutdownPatch) (*ShutdownDetail, error) {
	sd, err := s.getShutdown(ctx, code)
	if err != nil {
		return nil, err
	}
	if sd.Status != "planned" {
		return nil, fmt.Errorf("%w: shutdown %s is %s; only a planned one can change", ErrConflict, code, strings.ReplaceAll(sd.Status, "_", " "))
	}
	if patch.Title != nil {
		t := strings.TrimSpace(*patch.Title)
		if t == "" || len(t) > 200 {
			return nil, fmt.Errorf("%w: give the shutdown a title of 1–200 characters", ErrBadInput)
		}
		sd.Title = t
	}
	if patch.Lead != nil {
		sd.Lead = strings.TrimSpace(*patch.Lead)
	}
	if patch.Notes != nil {
		sd.Notes = strings.TrimSpace(*patch.Notes)
	}
	moved := false
	if patch.StartsAt != nil && !patch.StartsAt.UTC().Equal(sd.StartsAt) {
		sd.StartsAt, moved = patch.StartsAt.UTC(), true
	}
	if patch.EndsAt != nil && !patch.EndsAt.UTC().Equal(sd.EndsAt) {
		sd.EndsAt, moved = patch.EndsAt.UTC(), true
	}
	if err := checkWindow(sd.StartsAt, sd.EndsAt); err != nil {
		return nil, err
	}
	machines := sd.Machines
	if patch.Machines != nil {
		machines = *patch.Machines
	}
	machines, err = s.checkMachines(ctx, sd.PlantCode, machines, sd.StartsAt, sd.EndsAt, code)
	if err != nil {
		return nil, err
	}
	var added, removed []string
	for _, m := range machines {
		if !slices.Contains(sd.Machines, m) {
			added = append(added, m)
		}
	}
	for _, m := range sd.Machines {
		if !slices.Contains(machines, m) {
			removed = append(removed, m)
		}
	}
	sd.Machines = machines
	if err := s.saveShutdown(ctx, sd); err != nil {
		return nil, err
	}
	if moved {
		if _, err := s.pool.Exec(ctx, `
			UPDATE mes_downtime_events
			   SET planned_start = $2, planned_end = $3, started_at = $2, reason = $4
			 WHERE attrs->>'shutdown' = $1 AND state = 'scheduled'`,
			code, sd.StartsAt, sd.EndsAt, fmt.Sprintf("Shutdown %s: %s", code, sd.Title)); err != nil {
			return nil, err
		}
	}
	stops, err := s.shutdownStops(ctx, code)
	if err != nil {
		return nil, err
	}
	for _, d := range stops {
		if d.State == "scheduled" && slices.Contains(removed, d.AssetTag) {
			if _, err := s.CancelDowntimeEvent(ctx, d.ID); err != nil {
				return nil, err
			}
		}
	}
	for _, tag := range added {
		if err := s.planStop(ctx, sd, tag); err != nil {
			return nil, err
		}
	}
	if len(removed) > 0 {
		wos, err := s.shutdownWorkOrders(ctx, code)
		if err != nil {
			return nil, err
		}
		for _, wo := range wos {
			if isLive(wo) && slices.Contains(removed, wo.AssetTag) {
				if _, err := s.PatchWorkOrder(ctx, wo.Num, WorkOrderPatch{Attrs: map[string]any{"shutdown": nil}}); err != nil {
					return nil, err
				}
			}
		}
	}
	return s.GetShutdown(ctx, code)
}

// ShutdownJobs is which jobs to put into a shutdown or take out of it:
// existing work orders by number, and preventive schedules whose next job is
// brought forward into the window.
type ShutdownJobs struct {
	Add       []string    `json:"add"`
	Remove    []string    `json:"remove"`
	Schedules []uuid.UUID `json:"schedules"`
}

// SetShutdownJobs puts jobs into a planned shutdown or takes them out. A job
// must be on one of its machines. A schedule's next job is raised now, due at
// the start of the window — or, if it is already raised, that job is used.
func (s *Store) SetShutdownJobs(ctx context.Context, code string, in ShutdownJobs) (*ShutdownDetail, error) {
	sd, err := s.getShutdown(ctx, code)
	if err != nil {
		return nil, err
	}
	if sd.Status != "planned" && sd.Status != "in_progress" {
		return nil, fmt.Errorf("%w: shutdown %s is %s", ErrConflict, code, sd.Status)
	}
	day := sd.StartsAt.UTC().Format("2006-01-02")
	tag := func(num string) error {
		wo, err := s.GetWorkOrder(ctx, num)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("%w: no work order %s", ErrBadInput, num)
			}
			return err
		}
		if !slices.Contains(sd.Machines, wo.AssetTag) {
			return fmt.Errorf("%w: %s is on %s, which this shutdown does not stop", ErrBadInput, num, wo.AssetTag)
		}
		if !isLive(*wo) {
			return fmt.Errorf("%w: %s is already %s", ErrBadInput, num, wo.Status)
		}
		if other, _ := wo.Attrs["shutdown"].(string); other != "" && other != code {
			return fmt.Errorf("%w: %s is already in shutdown %s", ErrConflict, num, other)
		}
		attrs := map[string]any{"shutdown": code}
		if p, _ := wo.Attrs["plannedStart"].(string); p == "" || p < day {
			attrs["plannedStart"] = day
		}
		_, err = s.PatchWorkOrder(ctx, num, WorkOrderPatch{Attrs: attrs})
		return err
	}
	for _, num := range in.Add {
		if err := tag(strings.TrimSpace(num)); err != nil {
			return nil, err
		}
	}
	for _, id := range in.Schedules {
		sch, err := s.pmScheduleByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(sd.Machines, sch.AssetTag) {
			return nil, fmt.Errorf("%w: that schedule is on %s, which this shutdown does not stop", ErrBadInput, sch.AssetTag)
		}
		var open string
		_ = s.pool.QueryRow(ctx, `
			SELECT num FROM mes_work_orders WHERE pm_schedule_id = $1 AND status NOT IN ('completed', 'cancelled')
			 ORDER BY created_at DESC LIMIT 1`, id).Scan(&open)
		if open == "" {
			tpl, err := s.GetPMTemplate(ctx, sch.TemplateID)
			if err != nil {
				return nil, err
			}
			brought := *sch
			brought.NextDueAt = sd.StartsAt
			wo, err := s.CreateWorkOrderFromPM(ctx, brought, *tpl)
			if err != nil {
				return nil, err
			}
			open = wo.Num
		}
		if err := tag(open); err != nil {
			return nil, err
		}
	}
	for _, raw := range in.Remove {
		num := strings.TrimSpace(raw)
		wo, err := s.GetWorkOrder(ctx, num)
		if err != nil {
			return nil, err
		}
		if c, _ := wo.Attrs["shutdown"].(string); c != code {
			continue
		}
		if _, err := s.PatchWorkOrder(ctx, num, WorkOrderPatch{Attrs: map[string]any{"shutdown": nil}}); err != nil {
			return nil, err
		}
	}
	return s.GetShutdown(ctx, code)
}

func (s *Store) pmScheduleByID(ctx context.Context, id uuid.UUID) (*PMSchedule, error) {
	var sch PMSchedule
	err := s.pool.QueryRow(ctx, `
		SELECT id, template_id, asset_tag, next_due_at, last_done_at, status FROM mes_pm_schedules WHERE id = $1`, id).Scan(
		&sch.ID, &sch.TemplateID, &sch.AssetTag, &sch.NextDueAt, &sch.LastDoneAt, &sch.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: no preventive schedule %s", ErrBadInput, id)
	}
	return &sch, err
}

// AgreeShutdown records production's agreement to the window. It is a record,
// not a gate: a shutdown can start without it, and the plan says it did.
func (s *Store) AgreeShutdown(ctx context.Context, code, actor string, sig PaperSignature) (*ShutdownDetail, error) {
	sd, err := s.getShutdown(ctx, code)
	if err != nil {
		return nil, err
	}
	if sd.Status != "planned" {
		return nil, fmt.Errorf("%w: shutdown %s is %s", ErrConflict, code, sd.Status)
	}
	entry, err := sig.entry(actor, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	sd.ProductionAgreed = entry
	if err := s.saveShutdown(ctx, sd); err != nil {
		return nil, err
	}
	return s.GetShutdown(ctx, code)
}

/* ─────────────────────────────── running it ─────────────────────────────── */

// StartShutdown starts every planned stop: the machines go down now.
func (s *Store) StartShutdown(ctx context.Context, code, actor string) (*ShutdownDetail, error) {
	sd, err := s.getShutdown(ctx, code)
	if err != nil {
		return nil, err
	}
	if sd.Status != "planned" {
		return nil, fmt.Errorf("%w: shutdown %s is %s; only a planned one can start", ErrConflict, code, sd.Status)
	}
	stops, err := s.shutdownStops(ctx, code)
	if err != nil {
		return nil, err
	}
	for _, d := range stops {
		if d.State == "scheduled" {
			if _, err := s.StartDowntimeEvent(ctx, d.ID); err != nil {
				return nil, err
			}
		}
	}
	now := time.Now().UTC()
	sd.Status, sd.StartedAt = "in_progress", &now
	if err := s.saveShutdown(ctx, sd); err != nil {
		return nil, err
	}
	detail, err := s.GetShutdown(ctx, code)
	if err != nil {
		return nil, err
	}
	s.Notify(ctx, Notice{
		Audience: AudienceShutdowns,
		Title:    fmt.Sprintf("Shutdown started: %s", sd.Title),
		Message:  fmt.Sprintf("%s started by %s. %d machine(s) stopped, %d job(s) to do.", code, actor, len(sd.Machines), detail.OpenJobs),
		Key:      code,
	})
	return detail, nil
}

// FinishShutdown ends every stop that is still open. Jobs not finished stay
// open, and the notice says how many.
func (s *Store) FinishShutdown(ctx context.Context, code, actor string) (*ShutdownDetail, error) {
	sd, err := s.getShutdown(ctx, code)
	if err != nil {
		return nil, err
	}
	if sd.Status != "in_progress" {
		return nil, fmt.Errorf("%w: shutdown %s is %s; start it before finishing it", ErrConflict, code, sd.Status)
	}
	stops, err := s.shutdownStops(ctx, code)
	if err != nil {
		return nil, err
	}
	for _, d := range stops {
		if d.State == "open" {
			if _, err := s.EndDowntimeEvent(ctx, d.ID); err != nil {
				return nil, err
			}
		}
	}
	now := time.Now().UTC()
	sd.Status, sd.FinishedAt = "completed", &now
	if err := s.saveShutdown(ctx, sd); err != nil {
		return nil, err
	}
	detail, err := s.GetShutdown(ctx, code)
	if err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("%s finished by %s. %d of %d job(s) done.", code, actor, detail.Jobs-detail.OpenJobs, detail.Jobs)
	if detail.OpenJobs > 0 {
		msg += fmt.Sprintf(" %d still open.", detail.OpenJobs)
	}
	s.Notify(ctx, Notice{Audience: AudienceShutdowns, Title: fmt.Sprintf("Shutdown finished: %s", sd.Title), Message: msg, Key: code})
	return detail, nil
}

// CancelShutdown drops a planned shutdown: its stops are cancelled and its
// open jobs are taken out of it (they stay open, unplanned).
func (s *Store) CancelShutdown(ctx context.Context, code, actor string) (*ShutdownDetail, error) {
	sd, err := s.getShutdown(ctx, code)
	if err != nil {
		return nil, err
	}
	if sd.Status != "planned" {
		return nil, fmt.Errorf("%w: shutdown %s is %s; only a planned one can be cancelled", ErrConflict, code, sd.Status)
	}
	stops, err := s.shutdownStops(ctx, code)
	if err != nil {
		return nil, err
	}
	for _, d := range stops {
		if d.State == "scheduled" {
			if _, err := s.CancelDowntimeEvent(ctx, d.ID); err != nil {
				return nil, err
			}
		}
	}
	wos, err := s.shutdownWorkOrders(ctx, code)
	if err != nil {
		return nil, err
	}
	for _, wo := range wos {
		if isLive(wo) {
			if _, err := s.PatchWorkOrder(ctx, wo.Num, WorkOrderPatch{Attrs: map[string]any{"shutdown": nil}}); err != nil {
				return nil, err
			}
		}
	}
	sd.Status = "cancelled"
	if err := s.saveShutdown(ctx, sd); err != nil {
		return nil, err
	}
	s.Notify(ctx, Notice{
		Audience: AudienceShutdowns,
		Title:    fmt.Sprintf("Shutdown cancelled: %s", sd.Title),
		Message:  fmt.Sprintf("%s (%s) was cancelled by %s. The machines are not stopping.", code, shutdownWhen(sd), actor),
		Key:      code,
	})
	return s.GetShutdown(ctx, code)
}
