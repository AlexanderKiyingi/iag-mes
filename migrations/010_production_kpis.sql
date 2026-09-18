-- 010: Production's KPIs become MES's read model; the tables MES copied
-- from production when the two were split are dropped if empty.
--
-- Shop-floor performance is computed by iag-production from its own
-- execution data and published as production.measures.rolled_up (plant,
-- asset and shift snapshots) and production.kpi.breached. MES stores those
-- snapshots here, next to its own reliability KPIs, so one KPI screen reads
-- one table. The scope columns are new: MES snapshots were plant- or
-- asset-keyed only, and production's shift KPIs need a place to land.
--
-- The dropped tables were created by 002 alongside their prod_* twins and
-- never had a routed handler here: mes_production_runs, mes_stage_events,
-- mes_ccp_readings, mes_schedule_blocks, mes_shift_logs,
-- mes_operator_assignments, mes_operators. Their only rows were demo seed,
-- purged in 009. Each is dropped only if it is empty, so a database that
-- somehow holds real rows keeps them and the migration says so.
-- mes_production_orders stays: the ERP order sync here still writes it.

ALTER TABLE mes_kpi_snapshots
    ADD COLUMN IF NOT EXISTS scope_type TEXT NOT NULL DEFAULT 'plant',
    ADD COLUMN IF NOT EXISTS scope_key  TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS period_end TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS target     NUMERIC(18, 4),
    ADD COLUMN IF NOT EXISTS status     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source     TEXT NOT NULL DEFAULT 'mes';

-- Existing rows: scope_key follows the legacy columns so old and new read
-- the same way.
UPDATE mes_kpi_snapshots
SET scope_type = CASE WHEN asset_tag IS NOT NULL AND asset_tag <> '' THEN 'asset' ELSE 'plant' END,
    scope_key  = CASE WHEN asset_tag IS NOT NULL AND asset_tag <> '' THEN asset_tag ELSE COALESCE(plant_code, '') END
WHERE scope_key = '';

-- Production re-publishes a plant-day every time it recomputes; the
-- projection must be an upsert, keyed by what identifies a snapshot.
CREATE UNIQUE INDEX IF NOT EXISTS mes_kpi_snapshots_scope_period_uniq
    ON mes_kpi_snapshots (kpi_code, scope_type, scope_key, recorded_at)
    WHERE source = 'production';

CREATE INDEX IF NOT EXISTS mes_kpi_snapshots_scope_idx
    ON mes_kpi_snapshots (scope_type, scope_key, recorded_at DESC);

-- Definitions arrive with the snapshots (name/unit/category); the FK from
-- snapshots to definitions stays, so the consumer inserts a definition it
-- has not seen before writing its first snapshot. category is free text in
-- 002 so production's categories fit as they are.

-- mes_qc_handoffs.run_id (004) pointed at mes_production_runs; the handoff
-- itself is keyed by batch and sample and never used the run.
ALTER TABLE mes_qc_handoffs DROP COLUMN IF EXISTS run_id;

DO $drop_dead$
DECLARE
    t TEXT;
    n BIGINT;
BEGIN
    -- Children before parents.
    FOREACH t IN ARRAY ARRAY[
        'mes_operator_assignments', 'mes_shift_logs',
        'mes_schedule_blocks',
        'mes_ccp_readings', 'mes_stage_events', 'mes_production_runs',
        'mes_operators'
    ] LOOP
        IF to_regclass(t) IS NULL THEN
            CONTINUE;
        END IF;
        EXECUTE format('SELECT COUNT(*) FROM %I', t) INTO n;
        IF n = 0 THEN
            EXECUTE format('DROP TABLE %I', t);
        ELSE
            RAISE NOTICE 'keeping % — it holds % row(s); production owns this data now', t, n;
        END IF;
    END LOOP;
END
$drop_dead$;
