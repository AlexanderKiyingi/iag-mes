-- Extend the canonical machine/asset master (mes_assets) with procurement and
-- siting fields requested for production + maintenance: purchase date, physical
-- location on the floor, rated capacity, and a direct plant (factory) reference.
-- The machine name is the existing `name`; the shared identity is `tag`;
-- free-form extras still go in `attrs`. plant_id is denormalized from the
-- section hierarchy (mes_assets -> mes_sections -> mes_plants) for direct
-- plant-level querying and backfilled from each asset's section. All additive
-- and idempotent. Statements split on ";\n\n" for the migrate runner.

ALTER TABLE mes_assets
    ADD COLUMN IF NOT EXISTS purchased_on  DATE,
    ADD COLUMN IF NOT EXISTS location      TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS capacity      NUMERIC(18, 2),
    ADD COLUMN IF NOT EXISTS capacity_unit TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS plant_id      UUID REFERENCES mes_plants (id);

UPDATE mes_assets a
   SET plant_id = s.plant_id
  FROM mes_sections s
 WHERE a.section_id = s.id
   AND a.plant_id IS NULL;

CREATE INDEX IF NOT EXISTS mes_assets_plant_idx ON mes_assets (plant_id);
