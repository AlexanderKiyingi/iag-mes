-- A machine can be taken out of service.
--
-- mes_assets.status was running | idle | down | pm | maint — five words for
-- what a machine is doing right now, and none for "this machine is gone". The
-- table has no DELETE either, so a decommissioned pulper, a mistyped tag or a
-- row created by a probe stayed in the register for good, and every client
-- picking a machine kept offering it.
--
-- `retired` is deliberately not a sixth operating state. The other five answer
-- "what is it doing"; this one answers "is it still ours", which is why
-- nothing derives utilisation or downtime from it and why clients filter it
-- out of their pickers rather than showing it greyed.
--
-- Widening a CHECK is safe in both directions here: no existing row can hold
-- the new value, so nothing needs backfilling, and a rollback only fails if
-- something has already been retired.
ALTER TABLE mes_assets DROP CONSTRAINT IF EXISTS mes_assets_status_check;
ALTER TABLE mes_assets ADD CONSTRAINT mes_assets_status_check
    CHECK (status IN ('running', 'idle', 'down', 'pm', 'maint', 'retired'));

-- Retired machines are excluded from every list that asks "what can I pick",
-- so the common read is "not retired" rather than "status = X".
CREATE INDEX IF NOT EXISTS mes_assets_live_idx
    ON mes_assets (section_id)
    WHERE status <> 'retired';
