-- MES core schema: plant hierarchy, production execution, CMMS, scheduling, performance.

CREATE TABLE IF NOT EXISTS mes_plants (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    region     TEXT NOT NULL DEFAULT '',
    timezone   TEXT NOT NULL DEFAULT 'Africa/Kampala',
    status     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    attrs      JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mes_sections (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plant_id   UUID NOT NULL REFERENCES mes_plants(id),
    code       TEXT NOT NULL,
    name       TEXT NOT NULL,
    line_type  TEXT NOT NULL DEFAULT 'general',
    attrs      JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (plant_id, code)
);

CREATE INDEX IF NOT EXISTS mes_sections_plant_idx ON mes_sections (plant_id);

CREATE TABLE IF NOT EXISTS mes_assets (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    section_id          UUID NOT NULL REFERENCES mes_sections(id),
    tag                 TEXT NOT NULL UNIQUE,
    name                TEXT NOT NULL,
    category            TEXT NOT NULL DEFAULT '',
    criticality         TEXT NOT NULL DEFAULT 'C' CHECK (criticality IN ('A', 'B', 'C', 'D')),
    status              TEXT NOT NULL DEFAULT 'idle' CHECK (status IN ('running', 'idle', 'down', 'pm', 'maint')),
    vendor_party_id     TEXT,
    warehouse_asset_tag TEXT,
    oee_pct             NUMERIC(5, 2),
    mtbf_hours          NUMERIC(10, 2),
    sensor_count        INT NOT NULL DEFAULT 0,
    attrs               JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mes_assets_section_idx ON mes_assets (section_id);
CREATE INDEX IF NOT EXISTS mes_assets_status_idx ON mes_assets (status);

CREATE TABLE IF NOT EXISTS mes_shift_definitions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plant_id   UUID NOT NULL REFERENCES mes_plants(id),
    name       TEXT NOT NULL,
    start_time TIME NOT NULL,
    end_time   TIME NOT NULL,
    attrs      JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (plant_id, name)
);

CREATE TABLE IF NOT EXISTS mes_production_runs (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    business_id        TEXT NOT NULL UNIQUE,
    batch_business_id  TEXT NOT NULL,
    process            TEXT NOT NULL CHECK (process IN ('wet', 'dry', 'wetmill', 'drying', 'drymill', 'roast', 'p1', 'p2')),
    stage              TEXT NOT NULL DEFAULT '',
    stage_idx          INT NOT NULL DEFAULT 0,
    asset_tag          TEXT,
    operator_ref       TEXT,
    status             TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'awaiting_decision', 'awaiting_flag', 'flagged', 'completed', 'cancelled')),
    kg_in              NUMERIC(18, 3),
    kg_out             NUMERIC(18, 3),
    facility           TEXT,
    moisture           NUMERIC(8, 3),
    bed_id             TEXT,
    grade_prelim       TEXT,
    started_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at       TIMESTAMPTZ,
    attrs              JSONB NOT NULL DEFAULT '{}',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mes_production_runs_batch_idx ON mes_production_runs (batch_business_id);
CREATE INDEX IF NOT EXISTS mes_production_runs_status_idx ON mes_production_runs (status);

CREATE TABLE IF NOT EXISTS mes_stage_events (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id       UUID NOT NULL REFERENCES mes_production_runs(id),
    from_stage   TEXT NOT NULL DEFAULT '',
    to_stage     TEXT NOT NULL,
    stage_idx    INT NOT NULL DEFAULT 0,
    operator_ref TEXT,
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attrs        JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS mes_stage_events_run_idx ON mes_stage_events (run_id, occurred_at DESC);

CREATE TABLE IF NOT EXISTS mes_ccp_readings (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id       UUID NOT NULL REFERENCES mes_production_runs(id),
    ccp_code     TEXT NOT NULL,
    value        TEXT NOT NULL,
    target       TEXT NOT NULL DEFAULT '',
    pass         BOOLEAN NOT NULL DEFAULT true,
    operator_ref TEXT,
    recorded_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mes_ccp_readings_run_idx ON mes_ccp_readings (run_id);

CREATE TABLE IF NOT EXISTS mes_work_orders (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    num          TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    asset_tag    TEXT NOT NULL,
    wo_type      TEXT NOT NULL DEFAULT 'preventive',
    priority     TEXT NOT NULL DEFAULT 'medium' CHECK (priority IN ('critical', 'high', 'medium', 'low')),
    status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'in_progress', 'completed', 'cancelled')),
    assignee     TEXT,
    due_at       TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    attrs        JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mes_work_orders_asset_idx ON mes_work_orders (asset_tag);
CREATE INDEX IF NOT EXISTS mes_work_orders_status_idx ON mes_work_orders (status);

CREATE TABLE IF NOT EXISTS mes_pm_templates (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code           TEXT NOT NULL UNIQUE,
    name           TEXT NOT NULL,
    asset_category TEXT NOT NULL DEFAULT '',
    checklist      JSONB NOT NULL DEFAULT '[]',
    interval_days  INT NOT NULL DEFAULT 30,
    attrs          JSONB NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mes_pm_schedules (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id  UUID NOT NULL REFERENCES mes_pm_templates(id),
    asset_tag    TEXT NOT NULL,
    next_due_at  TIMESTAMPTZ NOT NULL,
    last_done_at TIMESTAMPTZ,
    status       TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'overdue', 'completed')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (template_id, asset_tag)
);

CREATE TABLE IF NOT EXISTS mes_downtime_events (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_tag    TEXT NOT NULL,
    category     TEXT NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at     TIMESTAMPTZ,
    kg_lost      NUMERIC(18, 3),
    operator_ref TEXT,
    attrs        JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mes_downtime_asset_idx ON mes_downtime_events (asset_tag, started_at DESC);

CREATE TABLE IF NOT EXISTS mes_technicians (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID,
    name       TEXT NOT NULL,
    role       TEXT NOT NULL DEFAULT '',
    plant_code TEXT,
    active     BOOLEAN NOT NULL DEFAULT true,
    attrs      JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mes_operators (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ref        TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    role       TEXT NOT NULL DEFAULT 'operator',
    shift      TEXT,
    station    TEXT,
    active     BOOLEAN NOT NULL DEFAULT true,
    attrs      JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mes_production_orders (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    po_num      TEXT NOT NULL UNIQUE,
    customer    TEXT NOT NULL DEFAULT '',
    product     TEXT NOT NULL DEFAULT '',
    qty_kg      NUMERIC(18, 3) NOT NULL DEFAULT 0,
    origin_lot  TEXT,
    asset_tag   TEXT,
    status      TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'awaiting', 'roasting', 'grinding', 'packaging', 'completed', 'cancelled')),
    due_at      TIMESTAMPTZ,
    erp_ref     TEXT,
    attrs       JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mes_production_orders_status_idx ON mes_production_orders (status);

CREATE TABLE IF NOT EXISTS mes_schedule_blocks (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_tag           TEXT NOT NULL,
    block_type          TEXT NOT NULL CHECK (block_type IN ('production', 'pm', 'major', 'safety', 'inspection')),
    label               TEXT NOT NULL DEFAULT '',
    starts_at           TIMESTAMPTZ NOT NULL,
    ends_at             TIMESTAMPTZ NOT NULL,
    production_order_id UUID REFERENCES mes_production_orders(id),
    work_order_id       UUID REFERENCES mes_work_orders(id),
    attrs               JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mes_schedule_blocks_asset_idx ON mes_schedule_blocks (asset_tag, starts_at);

CREATE TABLE IF NOT EXISTS mes_shift_logs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plant_code     TEXT NOT NULL,
    shift_name     TEXT NOT NULL,
    shift_date     DATE NOT NULL,
    handover_notes TEXT,
    output_kg      NUMERIC(18, 3),
    attrs          JSONB NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (plant_code, shift_name, shift_date)
);

CREATE TABLE IF NOT EXISTS mes_operator_assignments (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shift_log_id UUID NOT NULL REFERENCES mes_shift_logs(id) ON DELETE CASCADE,
    operator_ref TEXT NOT NULL,
    station      TEXT,
    asset_tag    TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mes_kpi_definitions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code           TEXT NOT NULL UNIQUE,
    name           TEXT NOT NULL,
    category       TEXT NOT NULL DEFAULT 'general',
    unit           TEXT NOT NULL DEFAULT '',
    target         NUMERIC(18, 4) NOT NULL DEFAULT 0,
    warn_threshold NUMERIC(18, 4),
    crit_threshold NUMERIC(18, 4),
    direction      TEXT NOT NULL DEFAULT 'up' CHECK (direction IN ('up', 'down')),
    active         BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mes_kpi_snapshots (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kpi_code    TEXT NOT NULL REFERENCES mes_kpi_definitions(code),
    plant_code  TEXT,
    asset_tag   TEXT,
    value       NUMERIC(18, 4) NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mes_kpi_snapshots_code_idx ON mes_kpi_snapshots (kpi_code, recorded_at DESC);

CREATE TABLE IF NOT EXISTS mes_alert_rules (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    condition  TEXT NOT NULL DEFAULT '',
    severity   TEXT NOT NULL DEFAULT 'warn' CHECK (severity IN ('crit', 'warn', 'info')),
    active     BOOLEAN NOT NULL DEFAULT true,
    attrs      JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mes_alerts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id         UUID REFERENCES mes_alert_rules(id),
    severity        TEXT NOT NULL DEFAULT 'warn' CHECK (severity IN ('crit', 'warn', 'info')),
    source          TEXT NOT NULL DEFAULT '',
    message         TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'ack', 'investigating', 'resolved', 'closed', 'false_positive')),
    acknowledged_at TIMESTAMPTZ,
    resolved_at     TIMESTAMPTZ,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attrs           JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS mes_alerts_status_idx ON mes_alerts (status, occurred_at DESC);

CREATE TABLE IF NOT EXISTS mes_asset_telemetry_latest (
    asset_tag   TEXT NOT NULL,
    metric      TEXT NOT NULL,
    value       NUMERIC(18, 4) NOT NULL,
    unit        TEXT NOT NULL DEFAULT '',
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (asset_tag, metric)
);

CREATE TABLE IF NOT EXISTS mes_ai_recommendations (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind       TEXT NOT NULL DEFAULT 'maintenance',
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    confidence NUMERIC(5, 2),
    asset_tag  TEXT,
    status     TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'accepted', 'dismissed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mes_batch_refs (
    batch_business_id TEXT PRIMARY KEY,
    validated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source            TEXT NOT NULL DEFAULT 'manual'
);

CREATE TABLE IF NOT EXISTS mes_event_outbox (
    id            BIGSERIAL PRIMARY KEY,
    kafka_topic   TEXT NOT NULL DEFAULT 'iag.production',
    event_type    TEXT NOT NULL,
    event_key     TEXT,
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    available_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    dispatched_at TIMESTAMPTZ,
    attempts      INT NOT NULL DEFAULT 0,
    last_error    TEXT
);

CREATE INDEX IF NOT EXISTS mes_event_outbox_due_idx ON mes_event_outbox (available_at) WHERE dispatched_at IS NULL;
