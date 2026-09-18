-- 011: Retire the ERP production-order sync.
--
-- MES synced ERP production orders into mes_production_orders (002) and
-- queued ERP webhooks in mes_erp_sync_queue (004). No handler was ever
-- routed to either, no job ran the sync, and iag-production carries the
-- same sync for the orders its runs are planned against. The code is
-- removed with this migration; the tables are dropped only if empty, as
-- 010 did for the copied run tables.

DO $retire_erp$
DECLARE
    t TEXT;
    n BIGINT;
BEGIN
    FOREACH t IN ARRAY ARRAY['mes_erp_sync_queue', 'mes_production_orders'] LOOP
        IF to_regclass(t) IS NULL THEN
            CONTINUE;
        END IF;
        -- 010 keeps mes_schedule_blocks when it holds rows, and that table
        -- references mes_production_orders; dropping the parent would then
        -- fail and, with AUTO_MIGRATE, stop the server on boot.
        IF t = 'mes_production_orders' AND to_regclass('mes_schedule_blocks') IS NOT NULL THEN
            RAISE NOTICE 'keeping mes_production_orders — mes_schedule_blocks still references it';
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
$retire_erp$;
