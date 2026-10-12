package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"iag-mes/backend/internal/events"
	"iag-mes/backend/internal/outbox"
)

func TestParseCondition(t *testing.T) {
	for cond, want := range map[string][3]string{
		"vibration_rms > 4.0": {"vibration_rms", ">", "4"},
		" temp_c >= 85 ":      {"temp_c", ">=", "85"},
		"pressure_bar < -0.5": {"pressure_bar", "<", "-0.5"},
	} {
		m, op, v, ok := ParseCondition(cond)
		if !ok || m != want[0] || op != want[1] {
			t.Errorf("%q → %s %s %v %v", cond, m, op, v, ok)
		}
	}
	for _, bad := range []string{"", "oee_pct", "vibration_rms ~ 4", "4 > vibration_rms", "x > abc"} {
		if _, _, _, ok := ParseCondition(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

// outboxed returns the payloads MES queued for Kafka of one event type.
func outboxed(t *testing.T, st *Store, ctx context.Context, eventType, key string) []map[string]any {
	t.Helper()
	rows, err := st.Pool().Query(ctx, `SELECT kafka_topic, payload FROM mes_event_outbox WHERE event_type = $1 AND event_key = $2 ORDER BY id`, eventType, key)
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var topic string
		var raw []byte
		if err := rows.Scan(&topic, &raw); err != nil {
			t.Fatalf("scan: %v", err)
		}
		var evt map[string]any
		_ = json.Unmarshal(raw, &evt)
		data, _ := evt["data"].(map[string]any)
		data["_topic"] = topic
		out = append(out, data)
	}
	return out
}

func busStore(t *testing.T) (*Store, context.Context) {
	st, ctx := phase2Store(t)
	bus := events.New(events.Config{Enabled: true, Brokers: []string{"127.0.0.1:1"}})
	bus.SetOutbox(outbox.NewStore(st.Pool()))
	st.SetEventBus(bus)
	return st, ctx
}

func TestAStoppedMachineIsAnnouncedToBothAudiences(t *testing.T) {
	st, ctx := busStore(t)
	_, tag := machine(t, st, ctx)
	if _, err := st.CreateFaultReport(ctx, FaultReportInput{AssetTag: tag, Title: "Huller seized", MachineStopped: true}, "Operator A"); err != nil {
		t.Fatalf("report: %v", err)
	}
	got := outboxed(t, st, ctx, events.TypeAlertRaised, tag)
	audiences := map[string][]string{}
	for _, n := range got {
		if n["_topic"] != events.TopicOperations {
			t.Fatalf("notice on %v, want %s", n["_topic"], events.TopicOperations)
		}
		audiences[n["audience"].(string)] = append(audiences[n["audience"].(string)], n["channel"].(string)+"/"+n["templateId"].(string))
	}
	for _, a := range []string{AudienceFaultReports, AudienceMachineStopped} {
		if strings.Join(audiences[a], ",") != "in_app/alert-in-app,email/mes.alert" {
			t.Fatalf("%s got %v (all: %v)", a, audiences[a], audiences)
		}
	}
	if got[len(got)-1]["priority"] != "critical" {
		t.Fatalf("a stopped machine is critical: %v", got[len(got)-1])
	}
}

func TestAssigningAJobTellsTheTechnician(t *testing.T) {
	st, ctx := busStore(t)
	_, tag := machine(t, st, ctx)
	user := uuid.New()
	tech := "Okello " + tag
	if _, err := st.Pool().Exec(ctx, `INSERT INTO mes_technicians (name, role, user_id) VALUES ($1, 'Fitter', $2)`, tech, user); err != nil {
		t.Fatalf("technician: %v", err)
	}
	num, _ := st.NextWorkOrderNum(ctx)
	if _, err := st.CreateWorkOrder(ctx, WorkOrder{Num: num, Title: "Belt", AssetTag: tag, WOType: "corrective"}); err != nil {
		t.Fatalf("wo: %v", err)
	}
	if n := outboxed(t, st, ctx, events.TypeAlertRaised, tag); len(n) != 0 {
		t.Fatalf("an unassigned job told someone: %v", n)
	}
	who := strings.ToLower(tech) // matched case-insensitively
	if _, err := st.PatchWorkOrder(ctx, num, WorkOrderPatch{Assignee: &who}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	got := outboxed(t, st, ctx, events.TypeAlertRaised, tag)
	if len(got) != 2 || got[0]["recipient"] != user.String() || got[0]["channel"] != "in_app" || got[1]["audience"] != AudienceAssignments {
		t.Fatalf("assignment notices: %v", got)
	}
	// Re-saving with the same assignee says nothing new.
	if _, err := st.PatchWorkOrder(ctx, num, WorkOrderPatch{Assignee: &who}); err != nil {
		t.Fatalf("resave: %v", err)
	}
	if again := outboxed(t, st, ctx, events.TypeAlertRaised, tag); len(again) != 2 {
		t.Fatalf("re-save notified again: %d", len(again))
	}
}

func TestSensorRulesRaiseOneReportPerMachineAndRule(t *testing.T) {
	st, ctx := busStore(t)
	_, tag := machine(t, st, ctx)
	code := "VIB-" + tag
	if _, err := st.Pool().Exec(ctx, `INSERT INTO mes_alert_rules (code, name, condition, severity) VALUES ($1, 'High vibration', 'vibration_rms > 4.0', 'crit')`, code); err != nil {
		t.Fatalf("rule: %v", err)
	}
	now := time.Now().UTC()
	if err := st.InsertTelemetryPoint(ctx, TelemetryPoint{AssetTag: tag, Metric: "vibration_rms", Value: 6.3, Unit: "mm/s", RecordedAt: now.Add(-10 * time.Minute)}); err != nil {
		t.Fatalf("reading: %v", err)
	}
	countMine := func() int {
		var n int
		_ = st.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM mes_alerts WHERE attrs->>'rule_code' = $1 AND attrs->>'asset_tag' = $2`, code, tag).Scan(&n)
		return n
	}
	if _, err := st.EvaluateConditionRules(ctx, now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if countMine() != 1 {
		t.Fatalf("want one report, got %d", countMine())
	}
	if _, err := st.EvaluateConditionRules(ctx, now); err != nil {
		t.Fatalf("evaluate again: %v", err)
	}
	if countMine() != 1 {
		t.Fatalf("a reading that stays high raised another report: %d", countMine())
	}
	var by string
	_ = st.Pool().QueryRow(ctx, `SELECT attrs->>'reported_by' FROM mes_alerts WHERE attrs->>'rule_code' = $1 AND attrs->>'asset_tag' = $2`, code, tag).Scan(&by)
	if by != "Sensor rule "+code {
		t.Fatalf("reported by %q", by)
	}
	// A day-old reading is not acted on.
	if _, err := st.EvaluateConditionRules(ctx, now.Add(48*time.Hour)); err != nil {
		t.Fatalf("evaluate later: %v", err)
	}
}

func TestPurchaseRequestsGoToProcurement(t *testing.T) {
	st, ctx := busStore(t)
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	sku := "BRG-" + uuid.NewString()[:6]
	cost := 450000.0
	ref, err := st.RequestPurchase(ctx, PurchaseRequestInput{SKU: sku, Name: "Bearing 6205", Qty: 10, UOM: "ea", EstimatedCost: &cost, Department: "Maintenance"}, "Planner D", now)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if ref != sku+"-20261013" && ref != "SP-"+sku+"-20261013" {
		t.Fatalf("ref %q", ref)
	}
	got := outboxed(t, st, ctx, events.TypeProcurementRequested, ref)
	if len(got) != 1 {
		t.Fatalf("want one event, got %v", got)
	}
	e := got[0]
	if e["_topic"] != events.TopicOperations || e["sourceService"] != "maintenance" || e["requestId"] != ref ||
		e["department"] != "Maintenance" || e["amount"] != "450000.00" || e["requestedBy"] != "Planner D" {
		t.Fatalf("event: %v", e)
	}
	for _, bad := range []PurchaseRequestInput{{Qty: 1, Department: "x"}, {SKU: "a", Department: "x"}, {SKU: "a", Qty: 1}} {
		if _, err := st.RequestPurchase(ctx, bad, "x", now); !errors.Is(err, ErrBadInput) {
			t.Fatalf("accepted %+v: %v", bad, err)
		}
	}
}
