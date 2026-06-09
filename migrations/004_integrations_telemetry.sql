-- Phase 5/6: telemetry history, integration audit, energy, ERP sync queue.

CREATE TABLE IF NOT EXISTS mes_telemetry_timeseries (
    id         BIGSERIAL PRIMARY KEY,
    asset_tag  TEXT NOT NULL,
    metric     TEXT NOT NULL,
    value      NUMERIC(18, 4) NOT NULL,
    unit       TEXT NOT NULL DEFAULT '',
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mes_telemetry_ts_asset_metric_idx
    ON mes_telemetry_timeseries (asset_tag, metric, recorded_at DESC);

CREATE TABLE IF NOT EXISTS mes_integration_calls (
    id            BIGSERIAL PRIMARY KEY,
    target        TEXT NOT NULL,
    operation     TEXT NOT NULL,
    correlation   TEXT,
    status        TEXT NOT NULL CHECK (status IN ('ok', 'error', 'skipped')),
    request_body  JSONB,
    response_body JSONB,
    error_message TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mes_integration_calls_target_idx ON mes_integration_calls (target, created_at DESC);

CREATE TABLE IF NOT EXISTS mes_energy_readings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plant_code  TEXT NOT NULL,
    asset_tag   TEXT,
    kwh         NUMERIC(18, 3) NOT NULL,
    tariff_band TEXT NOT NULL DEFAULT 'standard' CHECK (tariff_band IN ('off_peak', 'standard', 'peak')),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attrs       JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS mes_energy_readings_plant_idx ON mes_energy_readings (plant_code, recorded_at DESC);

CREATE TABLE IF NOT EXISTS mes_erp_sync_queue (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    po_num     TEXT NOT NULL,
    payload    JSONB NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'applied', 'failed')),
    applied_at TIMESTAMPTZ,
    error      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (po_num)
);

CREATE TABLE IF NOT EXISTS mes_qc_handoffs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_business_id   TEXT NOT NULL,
    sample_id         TEXT NOT NULL,
    run_id            UUID REFERENCES mes_production_runs(id),
    status            TEXT NOT NULL DEFAULT 'submitted',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (batch_business_id, sample_id)
);

CREATE TABLE IF NOT EXISTS mes_warehouse_handoffs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_business_id TEXT NOT NULL,
    operation         TEXT NOT NULL CHECK (operation IN ('consume', 'output')),
    payload           JSONB NOT NULL,
    status            TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ok', 'failed')),
    response          JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
