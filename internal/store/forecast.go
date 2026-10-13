package store

// The maintenance forecast: every preventive job due over a range of dates,
// for the annual calendar and for shutdown and parts planning.
//
// Nothing is stored. Each schedule is projected forward from its next due
// date:
//   - by days: one interval at a time;
//   - by running hours: at the machine's recent rate of use (the meter
//     readings of the last 30 days), so those dates are estimates;
//   - a template with both is due at whichever comes first, as the PM job
//     raises it.
//
// After the first occurrence the projection assumes each job is done on the
// day it falls due. A template with attrs.fixed_dates keeps its calendar
// whenever the job is actually done (AdvancePMScheduleAfterComplete), so its
// dates hold; one without drifts by however late each job is finished.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MaxForecastDays bounds a forecast: a year and a little, so a calendar year
// can be asked for from any day in it.
const MaxForecastDays = 400

// maxOccurrences bounds one schedule's projection, so a one-day interval over
// a year is still a sane answer.
const maxOccurrences = 400

const meterRateWindow = 30 * 24 * time.Hour

type ForecastItem struct {
	ScheduleID   uuid.UUID `json:"schedule_id"`
	TemplateID   uuid.UUID `json:"template_id"`
	TemplateCode string    `json:"template_code"`
	TemplateName string    `json:"template_name"`
	AssetTag     string    `json:"asset_tag"`
	AssetName    string    `json:"asset_name"`
	PlantCode    string    `json:"plant_code"`
	SectionCode  string    `json:"section_code"`
	DueOn        string    `json:"due_on"`
	// Basis is what made it due: days or hours.
	Basis string `json:"basis"`
	// Estimated marks a date projected from running hours.
	Estimated bool `json:"estimated"`
	// Overdue marks a job already past its due date at the start of the
	// range; it is shown on its due date so it is not lost.
	Overdue bool `json:"overdue"`
	// Occurrence counts from 0, the schedule's next job.
	Occurrence int `json:"occurrence"`
	// WorkOrder is the open job already raised for this occurrence.
	WorkOrder      string   `json:"work_order,omitempty"`
	EstimatedHours *float64 `json:"estimated_hours,omitempty"`
	CrewSize       *float64 `json:"crew_size,omitempty"`
	FixedDates     bool     `json:"fixed_dates"`
}

// ForecastGap is a schedule that could not be fully projected, and why.
type ForecastGap struct {
	ScheduleID   uuid.UUID `json:"schedule_id"`
	AssetTag     string    `json:"asset_tag"`
	TemplateCode string    `json:"template_code"`
	Reason       string    `json:"reason"`
}

type Forecast struct {
	From  string         `json:"from"`
	To    string         `json:"to"`
	Items []ForecastItem `json:"items"`
	Gaps  []ForecastGap  `json:"gaps"`
}

// projection is one projected due date.
type projection struct {
	At      time.Time
	Basis   string
	Overdue bool
}

// projectSchedule lists the due dates of one schedule from next onwards, up
// to and including `to`, dropping those before `from` except an overdue
// first one. intervalDays ≤ 0 means by hours only; hoursPerInterval is the
// days one running-hours interval lasts at the current rate (≤ 0 means not by
// hours), and hoursFirst the date the current hours interval runs out.
func projectSchedule(next time.Time, intervalDays int, fixed bool, hoursFirst time.Time, daysPerHoursInterval float64,
	now, from, to time.Time) []projection {
	byDays := intervalDays > 0
	byHours := daysPerHoursInterval > 0
	if !byDays && !byHours {
		return nil
	}
	out := []projection{}
	dayDue := next
	hourDue := hoursFirst
	for i := 0; i < maxOccurrences; i++ {
		var at time.Time
		basis := "days"
		switch {
		case byDays && byHours:
			at = dayDue
			if hourDue.Before(dayDue) {
				at, basis = hourDue, "hours"
			}
		case byDays:
			at = dayDue
		default:
			at, basis = hourDue, "hours"
		}
		if at.After(to) {
			break
		}
		overdue := i == 0 && at.Before(now)
		if !at.Before(from) || overdue {
			out = append(out, projection{At: at, Basis: basis, Overdue: overdue})
		}
		// The next job is counted from this one. An overdue job is assumed
		// done today.
		done := at
		if overdue {
			done = now
		}
		if byDays {
			if fixed && basis == "days" {
				dayDue = dayDue.AddDate(0, 0, intervalDays)
				for !dayDue.After(done) {
					dayDue = dayDue.AddDate(0, 0, intervalDays)
				}
			} else {
				dayDue = done.AddDate(0, 0, intervalDays)
			}
		}
		if byHours {
			hourDue = done.Add(time.Duration(daysPerHoursInterval * 24 * float64(time.Hour)))
		}
	}
	return out
}

// meterRate is a machine's running hours per day over the last 30 days, from
// the first and last readings in that window. ok is false without two
// readings a day apart, or when the meter went backwards (a reset).
func (s *Store) meterRate(ctx context.Context, tag string, now time.Time) (rate float64, ok bool, err error) {
	var first, last *TelemetryPoint
	rows, err := s.pool.Query(ctx, `
		(SELECT value, recorded_at FROM mes_telemetry_timeseries
		  WHERE asset_tag = $1 AND metric = $2 AND recorded_at >= $3 AND recorded_at <= $4
		  ORDER BY recorded_at ASC LIMIT 1)
		UNION ALL
		(SELECT value, recorded_at FROM mes_telemetry_timeseries
		  WHERE asset_tag = $1 AND metric = $2 AND recorded_at >= $3 AND recorded_at <= $4
		  ORDER BY recorded_at DESC LIMIT 1)`, tag, MeterMetric, now.Add(-meterRateWindow), now)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var p TelemetryPoint
		if err := rows.Scan(&p.Value, &p.RecordedAt); err != nil {
			return 0, false, err
		}
		if first == nil {
			first = &p
		} else {
			last = &p
		}
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	if first == nil || last == nil {
		return 0, false, nil
	}
	days := last.RecordedAt.Sub(first.RecordedAt).Hours() / 24
	used := last.Value - first.Value
	if days < 1 || used <= 0 {
		return 0, false, nil
	}
	return used / days, true, nil
}

func templateFloat(t PMTemplate, key string) *float64 {
	v := numberAttr(t.Attrs[key])
	if v == nil || *v <= 0 {
		return nil
	}
	return v
}

// FixedDates says whether a template keeps its calendar when a job is done
// late.
func FixedDates(t PMTemplate) bool {
	v, _ := t.Attrs["fixed_dates"].(bool)
	return v
}

// MaintenanceForecast projects every preventive schedule over [from, to],
// optionally for one factory. Retired machines are left out.
func (s *Store) MaintenanceForecast(ctx context.Context, from, to time.Time, plant string) (*Forecast, error) {
	from = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	to = time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, 0, time.UTC)
	if to.Before(from) {
		return nil, fmt.Errorf("%w: the range ends before it starts", ErrBadInput)
	}
	if to.Sub(from) > MaxForecastDays*24*time.Hour {
		return nil, fmt.Errorf("%w: a forecast covers at most %d days", ErrBadInput, MaxForecastDays)
	}
	now := time.Now().UTC()
	schedules, err := s.ListPMSchedules(ctx, "")
	if err != nil {
		return nil, err
	}
	templates, err := s.ListPMTemplates(ctx)
	if err != nil {
		return nil, err
	}
	tplByID := map[uuid.UUID]PMTemplate{}
	for _, t := range templates {
		tplByID[t.ID] = t
	}
	assets, err := s.ListAssets(ctx, AssetFilter{PlantCode: strings.TrimSpace(plant)})
	if err != nil {
		return nil, err
	}
	assetByTag := map[string]Asset{}
	for _, a := range assets {
		assetByTag[a.Tag] = a
	}
	openWO := map[uuid.UUID]string{}
	rows, err := s.pool.Query(ctx, `
		SELECT pm_schedule_id, num FROM mes_work_orders
		WHERE pm_schedule_id IS NOT NULL AND status NOT IN ('completed', 'cancelled')
		ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		var num string
		if err := rows.Scan(&id, &num); err != nil {
			rows.Close()
			return nil, err
		}
		openWO[id] = num
	}
	rows.Close()

	out := &Forecast{From: from.Format("2006-01-02"), To: to.Format("2006-01-02"), Items: []ForecastItem{}, Gaps: []ForecastGap{}}
	rates := map[string]*float64{}
	for _, sch := range schedules {
		tpl, ok := tplByID[sch.TemplateID]
		if !ok || IsCommissioningTemplate(tpl) {
			continue
		}
		asset, ok := assetByTag[sch.AssetTag]
		if !ok || asset.Status == "retired" {
			continue
		}
		gap := func(reason string) {
			out.Gaps = append(out.Gaps, ForecastGap{ScheduleID: sch.ID, AssetTag: sch.AssetTag, TemplateCode: tpl.Code, Reason: reason})
		}
		var hoursFirst time.Time
		var daysPerInterval float64
		if interval := intervalHoursOf(tpl); interval > 0 {
			if _, seen := rates[sch.AssetTag]; !seen {
				rate, ok, err := s.meterRate(ctx, sch.AssetTag, now)
				if err != nil {
					return nil, err
				}
				if ok {
					rates[sch.AssetTag] = &rate
				} else {
					rates[sch.AssetTag] = nil
				}
			}
			status, err := s.MeterStatusFor(ctx, sch, tpl)
			if err != nil {
				return nil, err
			}
			switch rate := rates[sch.AssetTag]; {
			case rate == nil:
				gap("running hours not forecast — fewer than two meter readings a day apart in the last 30 days")
			case status == nil || status.UsedHours == nil || status.ReadAt == nil:
				gap("running hours not forecast — no reading to count from")
			default:
				left := interval - *status.UsedHours
				if left < 0 {
					left = 0
				}
				hoursFirst = status.ReadAt.Add(time.Duration(left / *rate * 24 * float64(time.Hour)))
				daysPerInterval = interval / *rate
			}
		}
		dates := projectSchedule(sch.NextDueAt.UTC(), tpl.IntervalDays, FixedDates(tpl), hoursFirst, daysPerInterval, now, from, to)
		for i, p := range dates {
			item := ForecastItem{
				ScheduleID:     sch.ID,
				TemplateID:     tpl.ID,
				TemplateCode:   tpl.Code,
				TemplateName:   tpl.Name,
				AssetTag:       asset.Tag,
				AssetName:      asset.Name,
				PlantCode:      asset.PlantCode,
				SectionCode:    asset.SectionCode,
				DueOn:          p.At.Format("2006-01-02"),
				Basis:          p.Basis,
				Estimated:      p.Basis == "hours",
				Overdue:        p.Overdue,
				Occurrence:     i,
				EstimatedHours: templateFloat(tpl, "estimated_hours"),
				CrewSize:       templateFloat(tpl, "crew_size"),
				FixedDates:     FixedDates(tpl),
			}
			if i == 0 {
				item.WorkOrder = openWO[sch.ID]
			}
			out.Items = append(out.Items, item)
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.DueOn != b.DueOn {
			return a.DueOn < b.DueOn
		}
		return a.AssetTag < b.AssetTag
	})
	return out, nil
}
