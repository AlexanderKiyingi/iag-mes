-- Where a factory is.
--
-- IAG runs more than one: kampala, mbale and mbarara today, and the register
-- held only a free-text `region` and a timezone. That is enough to stamp a
-- timestamp and not much else — it cannot address a delivery, place a factory
-- on a map beside the farms that supply it, or tell you which of three sites a
-- vehicle is closest to.
--
-- gps_lat / gps_lng rather than latitude / longitude, and NUMERIC(9,4) rather
-- than anything finer, because that is what the supply-chain service already
-- uses for farms, cooperatives and suppliers (SCM 000003, 000007, 000010).
-- Factories and the farms that supply them belong on one map, and a map is
-- the one place a second coordinate convention shows up immediately.
--
-- Nothing is required. The three existing factories keep working with these
-- blank, and a factory created without an address is still a factory.

ALTER TABLE mes_plants
    ADD COLUMN IF NOT EXISTS address  TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS city     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS district TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS country  TEXT NOT NULL DEFAULT 'UG',
    ADD COLUMN IF NOT EXISTS gps_lat  NUMERIC(9, 4),
    ADD COLUMN IF NOT EXISTS gps_lng  NUMERIC(9, 4);

-- A coordinate that is only half given is worse than none: it reads as a
-- point somewhere on the equator or the prime meridian.
ALTER TABLE mes_plants DROP CONSTRAINT IF EXISTS mes_plants_gps_pair_chk;
ALTER TABLE mes_plants
    ADD CONSTRAINT mes_plants_gps_pair_chk
    CHECK ((gps_lat IS NULL) = (gps_lng IS NULL));

ALTER TABLE mes_plants DROP CONSTRAINT IF EXISTS mes_plants_gps_range_chk;
ALTER TABLE mes_plants
    ADD CONSTRAINT mes_plants_gps_range_chk
    CHECK ((gps_lat IS NULL OR gps_lat BETWEEN -90 AND 90)
       AND (gps_lng IS NULL OR gps_lng BETWEEN -180 AND 180));
