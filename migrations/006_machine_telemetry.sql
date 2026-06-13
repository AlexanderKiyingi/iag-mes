-- Machine-telemetry subsystem (edge/machine-telemetry, github.com/iag/machine-telemetry).
-- Keyed on asset_tag — the platform-wide machine identity already shared by
-- mes_assets (canonical master), production (prod_production_runs.asset_tag,
-- prod_schedule_blocks.asset_tag), and the CMMS. This adds only what MES lacked:
-- a high-throughput wide readings hypertable and a daily OEE rollup. Hot-state
-- folds into the canonical mes_assets + mes_asset_telemetry_latest, and detected
-- downtime into the existing mes_downtime_events (so maintenance stays complete).
-- No parallel machine registry is created. Statements split on ";\n\n"; the
-- hypertable DO-block is a single chunk.

CREATE TABLE IF NOT EXISTS mes_iot_devices (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    serial       TEXT NOT NULL UNIQUE,
    label        TEXT,
    asset_tag    TEXT REFERENCES mes_assets (tag) ON DELETE SET NULL,
    api_key_hash TEXT UNIQUE,
    is_active    BOOLEAN NOT NULL DEFAULT TRUE,
    last_seen    TIMESTAMPTZ,
    last_ip      TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mes_iot_devices_asset_idx ON mes_iot_devices (asset_tag);

CREATE TABLE IF NOT EXISTS mes_machine_telemetry (
    asset_tag      TEXT        NOT NULL,
    device_id      BIGINT,
    ts             TIMESTAMPTZ NOT NULL,
    state          TEXT        NOT NULL DEFAULT 'unknown',
    spindle_rpm    DOUBLE PRECISION,
    feed_rate      DOUBLE PRECISION,
    temperature_c  DOUBLE PRECISION,
    vibration_mm_s DOUBLE PRECISION,
    pressure_bar   DOUBLE PRECISION,
    power_kw       DOUBLE PRECISION,
    good_count     BIGINT,
    reject_count   BIGINT,
    cycle_count    BIGINT,
    fault_code     TEXT,
    raw            JSONB NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (asset_tag, ts)
);

CREATE INDEX IF NOT EXISTS mes_machine_telemetry_state_idx ON mes_machine_telemetry (asset_tag, state, ts DESC);

DO $machine_iot$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        PERFORM create_hypertable('mes_machine_telemetry', 'ts', if_not_exists => TRUE, migrate_data => FALSE);
    ELSE
        RAISE NOTICE 'timescaledb extension not installed — mes_machine_telemetry remains a regular table';
    END IF;
END
$machine_iot$;

CREATE TABLE IF NOT EXISTS mes_machine_oee_daily (
    asset_tag         TEXT NOT NULL,
    day               DATE NOT NULL,
    reading_count     INT  NOT NULL DEFAULT 0,
    running_minutes   INT  NOT NULL DEFAULT 0,
    idle_minutes      INT  NOT NULL DEFAULT 0,
    down_minutes      INT  NOT NULL DEFAULT 0,
    setup_minutes     INT  NOT NULL DEFAULT 0,
    good_total        BIGINT NOT NULL DEFAULT 0,
    reject_total      BIGINT NOT NULL DEFAULT 0,
    cycles_total      BIGINT NOT NULL DEFAULT 0,
    availability      DOUBLE PRECISION,
    performance       DOUBLE PRECISION,
    quality           DOUBLE PRECISION,
    oee               DOUBLE PRECISION,
    max_temperature_c DOUBLE PRECISION,
    avg_spindle_rpm   DOUBLE PRECISION,
    first_reading     TIMESTAMPTZ,
    last_reading      TIMESTAMPTZ,
    PRIMARY KEY (asset_tag, day)
);

CREATE UNIQUE INDEX IF NOT EXISTS mes_downtime_auto_uniq ON mes_downtime_events (asset_tag, started_at) WHERE category = 'auto';
