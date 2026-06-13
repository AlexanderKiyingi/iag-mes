-- Machine-telemetry parallel subsystem (edge/machine-telemetry, module
-- github.com/iag/machine-telemetry). Adds the high-throughput readings
-- hypertable, daily OEE rollup, downtime, and a device registry that MES (and
-- production) lacked. Intentionally a PARALLEL subsystem keyed on machine_id —
-- to be reconciled later with mes_assets (asset_tag) / mes_asset_telemetry_latest
-- / mes_downtime_events. Created in the `mes` schema via the connection
-- search_path. Statements are separated by a blank line (the migrate runner
-- splits on ";\n\n"); the hypertable DO-block is a single chunk on purpose.

CREATE TABLE IF NOT EXISTS machine_iot_devices (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    serial       TEXT NOT NULL UNIQUE,
    label        TEXT,
    machine_id   TEXT,
    api_key_hash TEXT UNIQUE,
    is_active    BOOLEAN NOT NULL DEFAULT TRUE,
    last_seen    TIMESTAMPTZ,
    last_ip      TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS machine_iot_devices_machine_idx ON machine_iot_devices (machine_id);

CREATE TABLE IF NOT EXISTS machines (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL DEFAULT '',
    last_state        TEXT,
    last_seen_at      TIMESTAMPTZ,
    last_good_count   BIGINT,
    last_reject_count BIGINT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS machine_telemetry_timeseries (
    machine_id     TEXT        NOT NULL,
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
    PRIMARY KEY (machine_id, ts)
);

CREATE INDEX IF NOT EXISTS machine_ts_state_idx ON machine_telemetry_timeseries (machine_id, state, ts DESC);

DO $machine_iot$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        PERFORM create_hypertable('machine_telemetry_timeseries', 'ts', if_not_exists => TRUE, migrate_data => FALSE);
    ELSE
        RAISE NOTICE 'timescaledb extension not installed — machine_telemetry_timeseries remains a regular table';
    END IF;
END
$machine_iot$;

CREATE TABLE IF NOT EXISTS machine_telemetry_daily (
    machine_id        TEXT NOT NULL,
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
    PRIMARY KEY (machine_id, day)
);

CREATE TABLE IF NOT EXISTS machine_downtime_events (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    machine_id   TEXT NOT NULL,
    started_at   TIMESTAMPTZ NOT NULL,
    ended_at     TIMESTAMPTZ,
    duration_min DOUBLE PRECISION NOT NULL DEFAULT 0,
    fault_code   TEXT,
    reason       TEXT,
    confidence   TEXT NOT NULL DEFAULT 'low',
    notes        TEXT,
    UNIQUE (machine_id, started_at)
);
