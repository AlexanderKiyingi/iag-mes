-- 015: Finish the demo asset purge that 009 left incomplete.
--
-- 003_seed inserted 18 asset tags; 009 only deleted nine of them. The rest
-- (CMP1, H1, DS1, GR1, CV2, DP1, WS1, DR1, PMP1) still land on a fresh migrate.
-- Same soft-delete pattern: skip a tag if live work orders / telemetry reference it.

DO $$
DECLARE
    demo_tag TEXT;
    kept     INT := 0;
BEGIN
    FOREACH demo_tag IN ARRAY ARRAY[
        'CMP1', 'H1', 'DS1', 'GR1', 'CV2', 'DP1', 'WS1', 'DR1', 'PMP1'
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
        RAISE NOTICE 'purge: % remaining demo asset(s) retained because live rows reference them', kept;
    END IF;
END $$;
