-- Seed reference data from inspire-cmms-v6 prototype (idempotent).

INSERT INTO mes_plants (code, name, region, timezone, status)
VALUES
    ('kampala', 'Kampala roastery', 'Central Uganda', 'Africa/Kampala', 'active'),
    ('mbale', 'Mbale processing', 'Eastern Uganda', 'Africa/Kampala', 'active'),
    ('mbarara', 'Mbarara washing station', 'Western Uganda', 'Africa/Kampala', 'active')
ON CONFLICT (code) DO NOTHING;

INSERT INTO mes_sections (plant_id, code, name, line_type)
SELECT p.id, s.code, s.name, s.line_type
FROM mes_plants p
JOIN (VALUES
    ('kampala', 'roasting', 'Roasting line', 'roasting'),
    ('kampala', 'grinding', 'Grinding line', 'grinding'),
    ('kampala', 'packaging', 'Packaging line', 'packaging'),
    ('kampala', 'sorting', 'Sorting line', 'sorting'),
    ('kampala', 'utilities', 'Utilities / support', 'utilities'),
    ('mbale', 'hulling', 'Hulling line', 'hulling'),
    ('mbarara', 'wet', 'Wet processing', 'wet_processing')
) AS s(plant_code, code, name, line_type) ON p.code = s.plant_code
ON CONFLICT (plant_id, code) DO NOTHING;

INSERT INTO mes_assets (section_id, tag, name, category, criticality, status, oee_pct, mtbf_hours, sensor_count)
SELECT sec.id, a.tag, a.name, a.category, a.criticality, a.status, a.oee_pct, a.mtbf_hours, a.sensor_count
FROM (
    VALUES
    ('kampala', 'roasting', 'R1', 'Probat G45 roaster', 'Roasting', 'A', 'running', 81, 54, 4),
    ('kampala', 'roasting', 'R2', 'Probat G60 roaster', 'Roasting', 'A', 'running', 78, 48, 4),
    ('kampala', 'roasting', 'CL1', 'Bean cooler', 'Roasting', 'B', 'running', 85, 65, 1),
    ('kampala', 'grinding', 'GR3', 'Mahlkönig DK27 grinder', 'Grinding', 'A', 'down', 62, 21, 6),
    ('kampala', 'packaging', 'PK1', 'IMA C21 packager', 'Packaging', 'B', 'idle', 71, 38, 3),
    ('kampala', 'packaging', 'PK2', 'Secondary packager', 'Packaging', 'C', 'running', 68, 41, 2),
    ('kampala', 'sorting', 'S1', 'Bühler Sortex A2', 'Sorting', 'B', 'running', 86, 72, 3),
    ('kampala', 'utilities', 'CV1', 'Conveyor line', 'Utilities', 'D', 'running', 91, 88, 1),
    ('kampala', 'utilities', 'BLR1', 'Steam boiler', 'Utilities', 'A', 'running', 54, 96, 4),
    ('kampala', 'utilities', 'CMP1', 'Air compressor', 'Utilities', 'C', 'running', 62, 124, 2),
    ('mbale', 'hulling', 'H1', 'Pinhalense huller', 'Hulling', 'A', 'pm', 74, 56, 2),
    ('mbale', 'hulling', 'DS1', 'Pinhalense destoner', 'Cleaning', 'B', 'running', 73, 61, 0),
    ('mbale', 'hulling', 'GR1', 'Mbale grader', 'Cleaning', 'B', 'running', 76, 58, 0),
    ('mbale', 'hulling', 'CV2', 'Mbale conveyor', 'Utilities', 'D', 'running', 88, 92, 0),
    ('mbarara', 'wet', 'DP1', 'Penagos depulper', 'Wet processing', 'A', 'running', 79, 44, 0),
    ('mbarara', 'wet', 'WS1', 'Washing channel', 'Wet processing', 'B', 'running', 82, 70, 0),
    ('mbarara', 'wet', 'DR1', 'Mechanical dryer', 'Wet processing', 'B', 'running', 71, 52, 0),
    ('mbarara', 'wet', 'PMP1', 'Water pump', 'Utilities', 'C', 'running', 84, 110, 0)
) AS a(plant_code, section_code, tag, name, category, criticality, status, oee_pct, mtbf_hours, sensor_count)
JOIN mes_plants p ON p.code = a.plant_code
JOIN mes_sections sec ON sec.plant_id = p.id AND sec.code = a.section_code
ON CONFLICT (tag) DO NOTHING;

INSERT INTO mes_shift_definitions (plant_id, name, start_time, end_time)
SELECT p.id, s.name, s.start_time::time, s.end_time::time
FROM mes_plants p
JOIN (VALUES
    ('kampala', 'Day', '06:00', '14:00'),
    ('kampala', 'Evening', '14:00', '22:00'),
    ('kampala', 'Night', '22:00', '06:00')
) AS s(plant_code, name, start_time, end_time) ON p.code = s.plant_code
ON CONFLICT (plant_id, name) DO NOTHING;

INSERT INTO mes_kpi_definitions (code, name, category, unit, target, warn_threshold, crit_threshold, direction)
VALUES
    ('OEE-01', 'OEE', 'oee', '%', 85, 75, 65, 'up'),
    ('SCH-01', 'Schedule attainment', 'schedule', '%', 95, 90, 80, 'up'),
    ('SCH-02', 'OTIF', 'schedule', '%', 95, 90, 85, 'up'),
    ('THR-01', 'Throughput roastery', 'throughput', 'kg/h', 1200, 1100, 1000, 'up'),
    ('YLD-01', 'First pass yield', 'yield', '%', 96, 92, 88, 'up'),
    ('LOSS-01', 'Breakdowns', 'losses', 'min', 30, 45, 60, 'down')
ON CONFLICT (code) DO NOTHING;

INSERT INTO mes_operators (ref, name, role, shift, station)
VALUES
    ('OP-001', 'James Mukasa', 'Maintenance lead', 'Day', 'Roasting line'),
    ('OP-002', 'Sarah Akello', 'Roaster operator', 'Day', 'R1 / R2'),
    ('OP-003', 'Faith Nansubuga', 'Technician', 'Evening', 'Utilities'),
    ('OP-004', 'David Wamala', 'Operator', 'Night', 'Packaging')
ON CONFLICT (ref) DO NOTHING;

INSERT INTO mes_technicians (name, role, plant_code)
SELECT v.name, v.role, v.plant_code
FROM (VALUES
    ('James Mukasa', 'Maintenance lead', 'kampala'),
    ('Faith Nansubuga', 'Senior technician', 'kampala'),
    ('Samuel Okello', 'Hulling specialist', 'mbale')
) AS v(name, role, plant_code)
WHERE NOT EXISTS (
    SELECT 1 FROM mes_technicians t WHERE t.name = v.name AND t.plant_code = v.plant_code
);

INSERT INTO mes_pm_templates (code, name, asset_category, interval_days, checklist)
VALUES
    ('TPL-ROAST-PM', 'Roaster preventive maintenance', 'Roasting', 30, '["Inspect burners","Check drum alignment","Lubricate bearings"]'::jsonb),
    ('TPL-GRIND-PM', 'Grinder preventive maintenance', 'Grinding', 21, '["Check burr wear","Inspect seals","Vibration baseline"]'::jsonb)
ON CONFLICT (code) DO NOTHING;

INSERT INTO mes_alert_rules (code, name, condition, severity)
VALUES
    ('VIB-HIGH', 'High vibration threshold', 'vibration_rms > 4.0', 'crit'),
    ('OEE-LOW', 'OEE below target', 'oee_pct < 75', 'warn'),
    ('PM-OVERDUE', 'PM schedule overdue', 'pm_due_days > 0', 'info')
ON CONFLICT (code) DO NOTHING;
