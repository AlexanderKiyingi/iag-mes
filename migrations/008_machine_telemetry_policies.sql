-- Storage policies for the machine telemetry hypertable, and autovacuum tuning
-- for the latest-value table.
--
-- 006 created mes_machine_telemetry as a hypertable and then used none of what
-- a hypertable is for: no compression, no retention, no continuous aggregate.
-- Each row carries a `raw` JSONB payload, so it grows uncompressed and unbounded
-- on the Postgres every business service shares.

DO $mes_telemetry_policies$
DECLARE
    compress_after INTERVAL := INTERVAL '7 days';
    retain_after   INTERVAL := INTERVAL '180 days';
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        RAISE NOTICE 'timescaledb not installed — skipping machine telemetry policies';
        RETURN;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM timescaledb_information.hypertables
         WHERE hypertable_name = 'mes_machine_telemetry'
    ) THEN
        RAISE NOTICE 'mes_machine_telemetry is not a hypertable — skipping policies';
        RETURN;
    END IF;

    -- Segment by asset_tag: every read path filters on it, so a compressed
    -- chunk can be scanned for one machine without decompressing the rest.
    -- Seven days is well past the window the OEE rollup and the maintenance
    -- views work in, so nothing hot is ever compressed.
    BEGIN
        ALTER TABLE mes_machine_telemetry SET (
            timescaledb.compress,
            timescaledb.compress_segmentby = 'asset_tag',
            timescaledb.compress_orderby   = 'ts DESC'
        );
    EXCEPTION WHEN OTHERS THEN
        RAISE NOTICE 'compression not configured on mes_machine_telemetry: %', SQLERRM;
    END;

    BEGIN
        PERFORM add_compression_policy('mes_machine_telemetry', compress_after,
                                       if_not_exists => TRUE);
    EXCEPTION WHEN OTHERS THEN
        RAISE NOTICE 'add_compression_policy skipped: %', SQLERRM;
    END;

    -- Raw readings only. mes_machine_oee_daily is the durable record and is
    -- NOT covered by retention, so history stays available at day resolution
    -- after the readings behind it age out.
    BEGIN
        PERFORM add_retention_policy('mes_machine_telemetry', retain_after,
                                     if_not_exists => TRUE);
    EXCEPTION WHEN OTHERS THEN
        RAISE NOTICE 'add_retention_policy skipped: %', SQLERRM;
    END;
END
$mes_telemetry_policies$;

-- ── Autovacuum for the latest-value table ──────────────────────────────────
--
-- mes_asset_telemetry_latest holds one row per (asset_tag, metric) — a few
-- hundred rows total — and every one of them is UPDATEd on every reading from
-- its machine. Postgres makes a dead tuple per update, and the default
-- autovacuum_vacuum_scale_factor of 0.2 means a 300-row table waits for 60 dead
-- tuples before vacuuming. At nine metrics per reading that threshold is
-- crossed constantly, but on a small table the default naptime still lets bloat
-- outpace the collector — and this is the table the maintenance dashboard reads
-- live, so its bloat is felt directly.
--
-- A flat threshold with no scale factor vacuums on churn rather than on
-- proportion, which is what a small hot table needs.
DO $mes_latest_autovacuum$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'mes_asset_telemetry_latest') THEN
        EXECUTE 'ALTER TABLE mes_asset_telemetry_latest SET ('
              || 'autovacuum_vacuum_scale_factor = 0.0, '
              || 'autovacuum_vacuum_threshold = 200, '
              || 'autovacuum_analyze_scale_factor = 0.0, '
              || 'autovacuum_analyze_threshold = 500)';
    END IF;
END
$mes_latest_autovacuum$;
