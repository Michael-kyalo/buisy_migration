-- nav_sku → skus
--
-- Pattern used by every transform (the target tables have no legacy columns):
--   1. Build a buisy_nexus_staging.w_* work table: one row per source row, with
--      reject_reason, the natural key, and the existing target row it matches.
--   2. Classify each row's action:
--        insert  no target row has the key
--        update  the target row has created_by = 'buisy-migration' (we created it)
--        adopt   the target row was created in Nexus: map to it, never modify it
--        reject  a rule failed; written to migration_rejects, never stops the run
--   3. Update, insert, then record old → new ids in buisy_nexus_staging.id_map.
--
-- Natural key: (company_id, code), matching skus' unique constraint. Codes are
-- compared trimmed and case-insensitive.

DROP TABLE IF EXISTS buisy_nexus_staging.w_skus;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_skus AS
SELECT s.sku_id::text                                              AS legacy_id,
       nullif(trim(s.sku_sku), '')                                 AS code,
       upper(nullif(trim(s.sku_sku), ''))                          AS match_key,
       nullif(trim(s.sku_itemname), '')                            AS name,
       nullif(trim(s.sku_productgeneration), '')                   AS generation,
       nullif(trim(s.sku_productionclassification), '')            AS classification,
       nullif(trim(s.sku_color), '')                               AS color,
       nullif(trim(s.sku_baseunit), '')                            AS base_unit,
       nullif(trim(s.sku_category), '')                            AS category_type,
       buisy_nexus_staging.parse_buisy_int(s.sku_serialnumberlength) AS serial_number_length,
       buisy_nexus_staging.parse_buisy_int(s.sku_stock)            AS stock,
       s.sku_has_warranty = 1                                      AS has_warranty,
       -- TODO(decision): sku_warranty_period is free text; its leading number is taken
       -- as warranty_period. Confirm the unit Nexus expects (months?).
       buisy_nexus_staging.parse_buisy_int(s.sku_warranty_period) AS warranty_period,
       -- TODO(decision): sku_status codes and the matching Nexus status values.
       CASE s.sku_status WHEN 1 THEN 'ACTIVE' ELSE 'INACTIVE' END  AS status,
       c.company_id,
       c.currency_code,
       buisy_nexus_staging.parse_buisy_ts(s.sku_entry_date) AT TIME ZONE 'UTC' AS created_at,
       NULL::bigint                                                AS existing_id,
       NULL::text                                                  AS existing_created_by,
       NULL::bigint                                                AS new_id,
       NULL::text                                                  AS action,
       CASE
         -- TODO(decision): publish = 0 rows are treated as deleted in Buisy and not migrated.
         WHEN s.publish = 0 THEN 'unpublished_in_buisy'
         WHEN nullif(trim(s.sku_sku), '') IS NULL THEN 'sku_code_missing'
         -- TODO(decision): a SKU whose company name has no Nexus company is rejected;
         -- a blank company loads with company_id NULL.
         WHEN nullif(trim(s.company), '') IS NOT NULL AND c.company_id IS NULL THEN 'company_unmatched'
       END                                                         AS reject_reason,
       to_jsonb(s)                                                 AS payload
FROM buisy_nexus_staging.nav_sku s
LEFT JOIN buisy_nexus_staging.w_company_lookup c
  ON c.match_key = upper(trim(s.company)) AND c.candidates = 1;

-- TODO(decision): duplicate codes within a company in Buisy; the lowest sku_id wins.
UPDATE buisy_nexus_staging.w_skus w
SET reject_reason = 'sku_code_duplicate'
FROM (
  SELECT legacy_id,
         row_number() OVER (PARTITION BY company_id, match_key ORDER BY legacy_id::bigint) AS rn
  FROM buisy_nexus_staging.w_skus
  WHERE reject_reason IS NULL
) d
WHERE d.legacy_id = w.legacy_id AND d.rn > 1;

UPDATE buisy_nexus_staging.w_skus w
SET existing_id = e.id, existing_created_by = e.created_by
FROM (
  SELECT DISTINCT ON (company_id, upper(trim(code))) id, company_id, upper(trim(code)) AS match_key, created_by
  FROM skus
  WHERE nullif(trim(code), '') IS NOT NULL
  ORDER BY company_id, upper(trim(code)), id
) e
WHERE w.reject_reason IS NULL
  AND e.company_id IS NOT DISTINCT FROM w.company_id
  AND e.match_key = w.match_key;

-- TODO(decision): a SKU that already exists in Nexus is adopted: Buisy inventory and
-- agents link to it, and its Nexus attributes are left as they are.
UPDATE buisy_nexus_staging.w_skus
SET action = CASE
  WHEN reject_reason IS NOT NULL THEN 'reject'
  WHEN existing_id IS NULL THEN 'insert'
  WHEN existing_created_by = 'buisy-migration' THEN 'update'
  ELSE 'adopt'
END;

INSERT INTO buisy_nexus_staging.migration_rejects (run_id, source_table, legacy_id, reason, payload)
SELECT current_setting('migrate.run_id')::uuid, 'nav_sku', legacy_id, reject_reason, payload
FROM buisy_nexus_staging.w_skus
WHERE action = 'reject';

-- currency is only set on insert, so a value corrected in Nexus survives reruns.
UPDATE skus t
SET code = w.code, "name" = w.name, generation = w.generation, classification = w.classification,
    color = w.color, base_unit = w.base_unit, category_type = w.category_type,
    serial_number_length = w.serial_number_length, stock = w.stock, has_warranty = w.has_warranty,
    warranty_period = w.warranty_period, status = w.status,
    updated_at = now(), updated_by = 'buisy-migration'
FROM buisy_nexus_staging.w_skus w
WHERE w.action = 'update' AND t.id = w.existing_id;

UPDATE buisy_nexus_staging.w_skus SET new_id = existing_id WHERE action IN ('update', 'adopt');

WITH ins AS (
  INSERT INTO skus (code, "name", generation, classification, color, base_unit, category_type,
                    serial_number_length, stock, has_warranty, warranty_period, status, company_id,
                    currency, created_at, created_by, updated_at, updated_by)
  SELECT code, name, generation, classification, color, base_unit, category_type,
         serial_number_length, stock, has_warranty, warranty_period, status, company_id,
         currency_code, -- Buisy has no currency: the company's currency (NULL without a company)
         coalesce(created_at, now()), 'buisy-migration', now(), 'buisy-migration'
  FROM buisy_nexus_staging.w_skus
  WHERE action = 'insert'
  RETURNING id, company_id, code
)
UPDATE buisy_nexus_staging.w_skus w
SET new_id = ins.id
FROM ins
WHERE w.action = 'insert' AND w.company_id IS NOT DISTINCT FROM ins.company_id AND w.code = ins.code;

INSERT INTO buisy_nexus_staging.id_map (entity, legacy_source, legacy_id, new_id)
SELECT 'sku', 'buisy.nav_sku', legacy_id, new_id::text
FROM buisy_nexus_staging.w_skus
WHERE new_id IS NOT NULL
ON CONFLICT (entity, legacy_source, legacy_id) DO UPDATE SET new_id = EXCLUDED.new_id;

-- SKU references elsewhere in Buisy (users.skus, inv_sku) are resolved through
-- this lookup: by code (case-insensitive; ambiguous across companies when
-- candidates > 1), or by Buisy sku_id when a reference is numeric.
-- users.skus is a comma-separated list of SKU codes. TODO(decision): confirm inv_sku
-- holds the SKU code too.
DROP TABLE IF EXISTS buisy_nexus_staging.w_sku_lookup;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_sku_lookup AS
SELECT 'code'::text AS kind, match_key, min(new_id) AS new_id, count(*) AS candidates
FROM buisy_nexus_staging.w_skus
WHERE new_id IS NOT NULL
GROUP BY match_key
UNION ALL
SELECT 'id', legacy_id, new_id, 1
FROM buisy_nexus_staging.w_skus
WHERE new_id IS NOT NULL;
