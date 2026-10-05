-- Planned downtime: a stop that is agreed before it happens.
--
-- Until now a downtime event could only be written once the machine had
-- already stopped — `started_at` defaults to NOW() and creating one marks the
-- asset `down`. There was nowhere to say "line 2 is down Thursday 14:00–16:00
-- for the belt change", which is the thing a plant actually plans around:
-- it is what makes the stop expected rather than a surprise, and it is the
-- difference between planned and unplanned downtime in any OEE conversation.
--
-- ── the model ──
-- One table, not two. A scheduled stop that happens becomes the very event
-- that records it, so the plan and the actual stay on one row and nothing has
-- to be reconciled afterwards. `state` says which phase the row is in:
--
--   scheduled → open → closed      the stop was planned, started, finished
--   scheduled → cancelled          the plan was dropped
--   open      → closed             an unplanned stop, exactly as before
--
-- `planned_start`/`planned_end` keep the agreed window even after the stop
-- actually begins, so "started 40 minutes late and ran an hour over" is a
-- question the data can answer.
--
-- `started_at` stays the one timeline everything orders by, including the
-- existing (asset_tag, started_at DESC) index: for a scheduled row it holds
-- the planned start until the stop begins, and the actual start after.
--
-- ── what this does NOT change ──
-- Nothing reads these rows for OEE. The KPI rollup takes `down_min` from
-- `prod_run_time_log` in iag-production, not from here, so a future-dated row
-- cannot move an availability number.
--
-- Existing rows keep behaving exactly as they did: the backfill derives their
-- state from `ended_at`, and `ListDowntimeEvents` leaves scheduled rows out
-- unless a caller asks for them, so every existing client sees the same live
-- downtime log it saw before.

ALTER TABLE mes_downtime_events
    ADD COLUMN IF NOT EXISTS state         TEXT NOT NULL DEFAULT 'open',
    ADD COLUMN IF NOT EXISTS planned_start TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS planned_end   TIMESTAMPTZ;

-- Every pre-existing row arrives here as 'open' from the default. A row that
-- already has an end was over before this migration ran.
UPDATE mes_downtime_events
   SET state = 'closed'
 WHERE state = 'open'
   AND ended_at IS NOT NULL;

ALTER TABLE mes_downtime_events
    DROP CONSTRAINT IF EXISTS mes_downtime_state_chk;
ALTER TABLE mes_downtime_events
    ADD CONSTRAINT mes_downtime_state_chk
    CHECK (state IN ('scheduled', 'open', 'closed', 'cancelled'));

-- The planned board reads one state over a date window; the live log reads
-- "everything except scheduled". Both are this index.
CREATE INDEX IF NOT EXISTS mes_downtime_state_idx
    ON mes_downtime_events (state, started_at DESC);
