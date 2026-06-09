-- CMMS gap closure: WO lifecycle link to PM, extended statuses

ALTER TABLE mes_work_orders
    ADD COLUMN IF NOT EXISTS pm_schedule_id UUID REFERENCES mes_pm_schedules(id);

CREATE INDEX IF NOT EXISTS mes_work_orders_pm_schedule_idx
    ON mes_work_orders (pm_schedule_id)
    WHERE pm_schedule_id IS NOT NULL;

ALTER TABLE mes_work_orders DROP CONSTRAINT IF EXISTS mes_work_orders_status_check;
ALTER TABLE mes_work_orders ADD CONSTRAINT mes_work_orders_status_check
    CHECK (status IN ('draft', 'scheduled', 'open', 'in_progress', 'completed', 'cancelled'));

INSERT INTO mes_kpi_definitions (code, name, category, unit, target, warn_threshold, crit_threshold, direction)
VALUES
    ('MTBF-01', 'Mean time between failures', 'reliability', 'hours', 500, 300, 150, 'up'),
    ('AVAIL-01', 'Availability', 'reliability', '%', 95, 90, 85, 'up'),
    ('DWT-01', 'Total downtime (six losses)', 'losses', 'min', 120, 180, 240, 'down')
ON CONFLICT (code) DO NOTHING;
