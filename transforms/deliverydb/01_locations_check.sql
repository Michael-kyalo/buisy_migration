-- Reference data already in Nexus: checks it is loaded and builds the lookups
-- later transforms resolve Buisy text against (locations, companies).
--
-- Every insert in these transforms sets created_by = 'buisy-migration'. With no
-- legacy columns on the target tables, that marker is how a rerun tells rows it
-- created (update them) from rows created in Nexus (never modified).

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM locations) THEN
    RAISE EXCEPTION 'locations is empty: load Nexus locations before migrating Buisy data';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM dimensions) THEN
    RAISE EXCEPTION 'dimensions is empty: dimensions must be migrated before Buisy data';
  END IF;
END
$$;

-- Location lookups, trimmed and case-insensitive. A key matching more than one
-- location is ambiguous; transforms only use keys with candidates = 1.
--   bin       locations.bin                      ← users.locations entries, nav_inventory.inv_bin
--   bin_code  locations.bin + locations.code     ← inv_bin + inv_location (narrows a shared bin)
--   code      locations.code                     ← inv_location
-- TODO(decision): confirm inv_bin ↔ locations.bin and inv_location ↔ locations.code.
DROP TABLE IF EXISTS buisy_nexus_staging.w_location_lookup;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_location_lookup AS
WITH l AS (
  SELECT id,
         upper(nullif(trim(bin), ''))  AS bin_key,
         upper(nullif(trim(code), '')) AS code_key
  FROM locations
),
keys (kind, match_key, id) AS (
  SELECT 'bin', bin_key, id FROM l WHERE bin_key IS NOT NULL
  UNION ALL
  SELECT 'bin_code', bin_key || '|' || code_key, id FROM l WHERE bin_key IS NOT NULL AND code_key IS NOT NULL
  UNION ALL
  SELECT 'code', code_key, id FROM l WHERE code_key IS NOT NULL
)
SELECT kind, match_key, min(id) AS location_id, count(*) AS candidates
FROM keys
GROUP BY kind, match_key;

-- Company lookup for the company text Buisy stores (nav_sku.company, inv_company):
-- matched to companies.name, else companies.code (both unique in Nexus), trimmed
-- and case-insensitive. The company also supplies the SKU currency.
DROP TABLE IF EXISTS buisy_nexus_staging.w_company_lookup;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_company_lookup AS
WITH by_name AS (
  SELECT upper(trim(c."name")) AS match_key, min(c.id) AS company_id, count(*) AS candidates
  FROM companies c
  WHERE nullif(trim(c."name"), '') IS NOT NULL
  GROUP BY 1
),
by_code AS (
  SELECT upper(trim(c.code)) AS match_key, min(c.id) AS company_id, count(*) AS candidates
  FROM companies c
  WHERE nullif(trim(c.code), '') IS NOT NULL
  GROUP BY 1
)
SELECT coalesce(n.match_key, k.match_key) AS match_key,
       CASE WHEN n.candidates = 1 THEN n.company_id
            WHEN n.match_key IS NULL AND k.candidates = 1 THEN k.company_id
       END AS company_id,
       CASE WHEN n.match_key IS NOT NULL THEN n.candidates ELSE k.candidates END AS candidates
FROM by_name n
FULL JOIN by_code k ON k.match_key = n.match_key;

ALTER TABLE buisy_nexus_staging.w_company_lookup ADD COLUMN currency_code text;
UPDATE buisy_nexus_staging.w_company_lookup l
SET currency_code = nullif(trim(c.currency_code), '')
FROM companies c
WHERE c.id = l.company_id;
