-- Purge the demo shop-floor roster and asset register seeded by 003_seed.sql.
--
-- Deliberately preserved:
--   * mes_plants, mes_sections, mes_shift_definitions (003_seed.sql) — plant
--     topology and shift patterns, which are configuration rather than demo data.
--   * mes_pm_templates (003_seed.sql) — preventive-maintenance checklists keyed by
--     asset category, reusable against any asset an operator creates.
--   * mes_alert_rules (003_seed.sql) and mes_kpi_definitions (005_cmms_gaps.sql) —
--     generic thresholds, not tied to the demo assets.
--
-- mes_assets is referenced by work orders, downtime events, schedule blocks and IoT
-- devices. Each asset is deleted in its own subtransaction and skipped if it is still
-- referenced, so an asset an operator has since built history against survives and
-- the migration cannot fail on a foreign key.

-- The migration runner already wraps every file in a single transaction holding an
-- advisory lock, so this file must not open one of its own: a COMMIT here would end
-- that outer transaction early and release the lock mid-run.

DELETE FROM mes_operators WHERE ref IN ('OP-001', 'OP-002', 'OP-003', 'OP-004');

DELETE FROM mes_technicians
WHERE (name, plant_code) IN (
    ('James Mukasa',    'kampala'),
    ('Faith Nansubuga', 'kampala'),
    ('Samuel Okello',   'mbale')
);

DO $$
DECLARE
    demo_tag TEXT;
    kept     INT := 0;
BEGIN
    FOREACH demo_tag IN ARRAY ARRAY[
        'R1', 'R2', 'CL1', 'GR3', 'PK1', 'PK2', 'S1', 'CV1', 'BLR1'
    ]
    LOOP
        BEGIN
            DELETE FROM mes_assets WHERE tag = demo_tag;
        EXCEPTION WHEN foreign_key_violation THEN
            kept := kept + 1;
            RAISE NOTICE 'mes_assets % still has work orders or telemetry — kept', demo_tag;
        END;
    END LOOP;
    IF kept > 0 THEN
        RAISE NOTICE 'purge: % demo asset(s) retained because live rows reference them', kept;
    END IF;
END $$;
