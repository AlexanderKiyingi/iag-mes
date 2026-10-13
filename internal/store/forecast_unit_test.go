package store

import (
	"errors"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func dates(ps []projection) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.At.Format("2006-01-02")
		if p.Overdue {
			out[i] += "!"
		}
		if p.Basis == "hours" {
			out[i] += "h"
		}
	}
	return out
}

func same(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", label, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", label, got, want)
		}
	}
}

func TestProjectionByDays(t *testing.T) {
	now := day("2026-01-10")
	got := projectSchedule(day("2026-01-20"), 30, false, time.Time{}, 0, now, day("2026-01-01"), day("2026-04-30"))
	same(t, "monthly", dates(got), []string{"2026-01-20", "2026-02-19", "2026-03-21", "2026-04-20"})

	// The range starts later: earlier dates are projected but not listed.
	got = projectSchedule(day("2026-01-20"), 30, false, time.Time{}, 0, now, day("2026-03-01"), day("2026-04-30"))
	same(t, "from March", dates(got), []string{"2026-03-21", "2026-04-20"})
}

// An overdue job is shown on its due date, then assumed done today. A
// floating template counts on from today; a fixed one keeps its calendar.
func TestOverdueFloatingAndFixed(t *testing.T) {
	now := day("2026-01-10")
	floating := projectSchedule(day("2025-12-25"), 30, false, time.Time{}, 0, now, day("2026-01-01"), day("2026-03-31"))
	same(t, "floating", dates(floating), []string{"2025-12-25!", "2026-02-09", "2026-03-11"})
	fixed := projectSchedule(day("2025-12-25"), 30, true, time.Time{}, 0, now, day("2026-01-01"), day("2026-03-31"))
	same(t, "fixed", dates(fixed), []string{"2025-12-25!", "2026-01-24", "2026-02-23", "2026-03-25"})
}

// Days and hours both: whichever comes first, and the other is counted again
// from that job.
func TestProjectionWhicheverFirst(t *testing.T) {
	now := day("2026-01-01")
	// 90-day backstop; the hours interval lasts 20 days at the current rate
	// and runs out on 5 Jan.
	got := projectSchedule(day("2026-03-01"), 90, false, day("2026-01-05"), 20, now, day("2026-01-01"), day("2026-02-28"))
	same(t, "hours first", dates(got), []string{"2026-01-05h", "2026-01-25h", "2026-02-14h"})

	// Hours alone, no day interval.
	got = projectSchedule(time.Time{}, 0, false, day("2026-01-05"), 40, now, day("2026-01-01"), day("2026-03-31"))
	same(t, "hours only", dates(got), []string{"2026-01-05h", "2026-02-14h", "2026-03-26h"})

	// Neither: nothing to project.
	if got := projectSchedule(day("2026-01-05"), 0, false, time.Time{}, 0, now, day("2026-01-01"), day("2026-03-31")); len(got) != 0 {
		t.Fatalf("no interval: %v", dates(got))
	}
}

func TestProjectionIsBounded(t *testing.T) {
	now := day("2026-01-01")
	got := projectSchedule(day("2026-01-01"), 1, false, time.Time{}, 0, now, day("2026-01-01"), day("2027-12-31"))
	if len(got) != maxOccurrences {
		t.Fatalf("daily over two years: %d occurrences, want the cap %d", len(got), maxOccurrences)
	}
}

func TestNextDueAfterACompletedJob(t *testing.T) {
	// Done ten days late.
	done := day("2026-02-10")
	if got := nextDueAfter(day("2026-01-31"), 30, false, done).Format("2006-01-02"); got != "2026-03-12" {
		t.Fatalf("floating: %s", got)
	}
	if got := nextDueAfter(day("2026-01-31"), 30, true, done).Format("2006-01-02"); got != "2026-03-02" {
		t.Fatalf("fixed: %s", got)
	}
	// Fixed, and so late that a whole interval has gone by: the next date is
	// still on the calendar and still ahead.
	if got := nextDueAfter(day("2026-01-31"), 30, true, day("2026-03-15")).Format("2006-01-02"); got != "2026-04-01" {
		t.Fatalf("fixed, very late: %s", got)
	}
	// Fixed and done early: the next date is the next on the calendar.
	if got := nextDueAfter(day("2026-01-31"), 30, true, day("2026-01-25")).Format("2006-01-02"); got != "2026-03-02" {
		t.Fatalf("fixed, early: %s", got)
	}
}

func TestAcceptanceLimits(t *testing.T) {
	wo := &WorkOrder{Num: "WO-1", WOType: "Commissioning", Attrs: map[string]any{"checklist": []any{
		"Guards fitted",
		map[string]any{"step": "Motor current", "min": 10.0, "max": "14", "unit": "A"},
		map[string]any{"step": "Bearing temperature", "max": 70.0, "unit": "°C"},
	}}}
	reading := func(v float64) *float64 { return &v }
	cases := []struct {
		label  string
		entry  ChecklistEntry
		result string
		ok     bool
	}{
		{"plain step needs no reading", ChecklistEntry{Index: 0}, "pass", true},
		{"in range", ChecklistEntry{Index: 1, Reading: reading(12)}, "pass", true},
		{"below range as a pass", ChecklistEntry{Index: 1, Reading: reading(9)}, "pass", false},
		{"below range as a fail", ChecklistEntry{Index: 1, Reading: reading(9)}, "fail", true},
		{"a commissioning pass needs the measurement", ChecklistEntry{Index: 2}, "pass", false},
		{"max only", ChecklistEntry{Index: 2, Reading: reading(71)}, "pass", false},
		{"not applicable needs no reading", ChecklistEntry{Index: 2}, "na", true},
	}
	for _, c := range cases {
		err := checkAcceptance(wo, c.entry, c.result)
		if (err == nil) != c.ok {
			t.Fatalf("%s: %v", c.label, err)
		}
		if err != nil && !errors.Is(err, ErrBadInput) {
			t.Fatalf("%s: %v is not bad input", c.label, err)
		}
	}
	// On an ordinary job a limit still refuses a contradictory pass, but a
	// pass with no reading is the technician's call.
	wo.WOType = "preventive"
	if err := checkAcceptance(wo, ChecklistEntry{Index: 2}, "pass"); err != nil {
		t.Fatalf("preventive pass without a reading: %v", err)
	}
	if err := checkAcceptance(wo, ChecklistEntry{Index: 2, Reading: reading(90)}, "pass"); err == nil {
		t.Fatal("preventive pass at 90 °C against a 70 °C limit")
	}
}

func TestCommissioningCompletionNeeds(t *testing.T) {
	wo := &WorkOrder{Num: "WO-2", WOType: "commissioning", Attrs: map[string]any{}}
	if err := CheckCompletion(wo); err == nil {
		t.Fatal("a commissioning job with no tests completed")
	}
	wo.Attrs["checklist"] = []any{"Runs at speed"}
	wo.Attrs["checklist_results"] = []any{map[string]any{"index": 0.0, "result": "pass"}}
	err := CheckCompletion(wo)
	if err == nil {
		t.Fatal("completed without a handover")
	}
	wo.Attrs["handover"] = map[string]any{
		"installer":   map[string]any{"by": "A"},
		"engineering": map[string]any{"by": "B"},
		"production":  map[string]any{"by": "C"},
	}
	// No technician sign-off: the handover stands in for it.
	if err := CheckCompletion(wo); err != nil {
		t.Fatalf("tests passed and handover signed: %v", err)
	}
	wo.Attrs["checklist_results"] = []any{map[string]any{"index": 0.0, "result": "fail"}}
	if err := CheckCompletion(wo); err == nil {
		t.Fatal("completed with a failed test")
	}
}
