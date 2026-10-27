-- nav_inventory + nav_inventory__cookware → inventory_products
-- Both sources load into one table. See 02_skus.sql for the insert / update /
-- reject pattern.
--
-- Natural key: serial_number, which is unique across the whole table (not per SKU).
-- metadata carries legacy_source / legacy_id, so a rerun recognises its own rows
-- even after buisy_nexus_staging has been dropped. A serial already in Nexus that
-- isn't ours is never overwritten: the Buisy row is rejected.

DROP TABLE IF EXISTS buisy_nexus_staging.w_inventory;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_inventory AS
WITH src AS (
  SELECT 'buisy.nav_inventory'::text AS legacy_source, 'nav_inventory'::text AS source_table, i.*
  FROM buisy_nexus_staging.nav_inventory i
  UNION ALL
  -- TODO(decision): nav_inventory__cookware's definition wasn't provided; it is
  -- assumed to have the same columns as nav_inventory.
  SELECT 'buisy.nav_inventory__cookware', 'nav_inventory__cookware', c.*
  FROM buisy_nexus_staging.nav_inventory__cookware c
),
resolved AS (
  SELECT src.*,
         src.inv_id::text                              AS legacy_id,
         nullif(trim(src.inv_serial), '')              AS serial,
         -- TODO(decision): serial uniqueness compares trimmed, case-insensitive values.
         upper(nullif(trim(src.inv_serial), ''))       AS serial_key,
         upper(nullif(trim(src.inv_bin), ''))          AS bin_key,
         upper(nullif(trim(src.inv_location), ''))     AS loc_key,
         sc.candidates AS sku_code_candidates, sc.new_id AS sku_by_code, si.new_id AS sku_by_id,
         lb.candidates AS bin_candidates,  lb.location_id AS loc_by_bin,
         lbc.candidates AS bin_code_candidates, lbc.location_id AS loc_by_bin_code,
         lc.candidates AS code_candidates, lc.location_id AS loc_by_code,
         co.company_id
  FROM src
  LEFT JOIN buisy_nexus_staging.w_sku_lookup sc ON sc.kind = 'code' AND sc.match_key = upper(trim(src.inv_sku))
  LEFT JOIN buisy_nexus_staging.w_sku_lookup si ON si.kind = 'id' AND si.match_key = trim(src.inv_sku)
  LEFT JOIN buisy_nexus_staging.w_location_lookup lb
    ON lb.kind = 'bin' AND lb.match_key = upper(nullif(trim(src.inv_bin), ''))
  LEFT JOIN buisy_nexus_staging.w_location_lookup lbc
    ON lbc.kind = 'bin_code' AND lbc.match_key = upper(nullif(trim(src.inv_bin), '')) || '|' || upper(nullif(trim(src.inv_location), ''))
  LEFT JOIN buisy_nexus_staging.w_location_lookup lc
    ON lc.kind = 'code' AND lc.match_key = upper(nullif(trim(src.inv_location), ''))
  LEFT JOIN buisy_nexus_staging.w_company_lookup co
    ON co.match_key = upper(trim(src.inv_company)) AND co.candidates = 1
)
SELECT r.legacy_source,
       r.source_table,
       r.legacy_id,
       r.serial,
       r.serial_key,
       CASE WHEN r.sku_code_candidates = 1 THEN r.sku_by_code
            WHEN r.sku_code_candidates IS NULL THEN r.sku_by_id
       END AS sku_id,
       -- TODO(decision): location rule. The bin if it identifies one location, else
       -- bin + location code, else the location code alone.
       CASE WHEN r.bin_candidates = 1 THEN r.loc_by_bin
            WHEN r.bin_code_candidates = 1 THEN r.loc_by_bin_code
            WHEN r.code_candidates = 1 THEN r.loc_by_code
       END AS location_id,
       r.company_id,
       -- Keep in sync with validate/deliverydb/02_check_inventory_status.sql.
       CASE
         WHEN r.is_sold = 1 OR nullif(trim(r.sales_order_no), '') IS NOT NULL THEN 'SOLD'
         -- TODO(decision): the exact inv_sub_status value(s) that mean "in transit".
         WHEN lower(trim(r.inv_sub_status)) IN ('in transit', 'in_transit', 'intransit') THEN 'IN_TRANSIT'
         -- Proposed: everything else is SCANNED_IN. inv_status (a numeric code) is kept
         -- in metadata. TODO(decision): SCANNED_OUT has no Buisy source yet.
         ELSE 'SCANNED_IN'
       END AS status,
       -- Proposed: scan_in_time from inv_entry_date (text, see parse_buisy_ts).
       -- TODO(decision): Buisy times are read as UTC; confirm the source time zone.
       buisy_nexus_staging.parse_buisy_ts(r.inv_entry_date) AT TIME ZONE 'UTC'       AS scan_in_time,
       buisy_nexus_staging.parse_buisy_ts(r.inv_manufacture_date) AT TIME ZONE 'UTC' AS manufacture_date,
       -- document_no is required in Nexus.
       -- TODO(decision): placeholder for rows without an inv_doc_no.
       coalesce(nullif(trim(r.inv_doc_no), ''), 'BUISY-MIGRATION') AS document_no,
       jsonb_build_object(
         'legacy_source', r.legacy_source,
         'legacy_id', r.legacy_id,
         'buisy', jsonb_strip_nulls(jsonb_build_object(
           'name', r.inv_name, 'sku', r.inv_sku, 'generation', r.inv_generation,
           'classification', r.inv_classification, 'color', r.inv_color, 'location', r.inv_location,
           'bin', r.inv_bin, 'base_unit', r.inv_base_unit, 'country', r.inv_country,
           'company', r.inv_company, 'main_department', r.inv_main_department,
           'sub_department', r.inv_sub_department, 'doc_no', r.inv_doc_no,
           'entry_date', r.inv_entry_date, 'manufacture_date', r.inv_manufacture_date,
           'inv_status', r.inv_status, 'sub_status', r.inv_sub_status, 'is_sold', r.is_sold,
           'sales_order_no', r.sales_order_no, 'product_name', r.inv_product_name,
           'cost_center', r.inv_cost_center, 'carbon_project', r.inv_carbon_project,
           'profit_center', r.inv_profit_center, 'product_center', r.inv_product_center,
           'rd_project', r.inv_rd_project, 'directors', r.inv_directors, 'category', r.inv_category))
       ) AS metadata,
       NULL::bigint AS existing_id,
       NULL::bigint AS new_id,
       NULL::text   AS action,
       CASE
         WHEN r.publish = 0 THEN 'unpublished_in_buisy'
         WHEN r.serial IS NULL THEN 'serial_missing'
         WHEN r.sku_code_candidates > 1 THEN 'sku_ambiguous'
         WHEN r.sku_code_candidates IS NULL AND r.sku_by_id IS NULL THEN 'sku_unresolved'
         WHEN NOT coalesce(r.bin_candidates = 1 OR r.bin_code_candidates = 1 OR r.code_candidates = 1, false)
              AND (r.bin_key IS NOT NULL OR r.loc_key IS NOT NULL) THEN 'location_unmatched'
         WHEN nullif(trim(r.inv_company), '') IS NOT NULL AND r.company_id IS NULL THEN 'company_unmatched'
       END AS reject_reason,
       to_jsonb(r) - 'sku_code_candidates' - 'sku_by_code' - 'sku_by_id' - 'bin_candidates' - 'loc_by_bin'
         - 'bin_code_candidates' - 'loc_by_bin_code' - 'code_candidates' - 'loc_by_code' AS payload
FROM resolved r;

-- TODO(decision): items with neither inv_bin nor inv_location load with location_id NULL.

-- Serial numbers are unique across all of inventory_products.
-- TODO(decision): which duplicate wins. Current rule: nav_inventory over cookware,
-- then the latest inv_entry_date, then the lowest inv_id.
UPDATE buisy_nexus_staging.w_inventory w
SET reject_reason = 'duplicate_serial'
FROM (
  SELECT legacy_source, legacy_id,
         row_number() OVER (
           PARTITION BY serial_key
           ORDER BY (legacy_source = 'buisy.nav_inventory') DESC,
                    scan_in_time DESC NULLS LAST,
                    legacy_id::bigint
         ) AS rn
  FROM buisy_nexus_staging.w_inventory
  WHERE reject_reason IS NULL
) d
WHERE d.legacy_source = w.legacy_source AND d.legacy_id = w.legacy_id AND d.rn > 1;

-- Match existing rows by serial; only rows this migration created for the same
-- Buisy row may be updated.
UPDATE buisy_nexus_staging.w_inventory w
SET existing_id = t.id,
    reject_reason = CASE
      WHEN t.created_by = 'buisy-migration'
       AND t.metadata ->> 'legacy_source' = w.legacy_source
       AND t.metadata ->> 'legacy_id' = w.legacy_id THEN NULL
      ELSE 'serial_exists_in_nexus'
    END
FROM inventory_products t
WHERE w.reject_reason IS NULL
  AND upper(trim(t.serial_number)) = w.serial_key;

UPDATE buisy_nexus_staging.w_inventory
SET action = CASE WHEN reject_reason IS NOT NULL THEN 'reject'
                  WHEN existing_id IS NULL THEN 'insert'
                  ELSE 'update' END;

INSERT INTO buisy_nexus_staging.migration_rejects (run_id, source_table, legacy_id, reason, payload)
SELECT current_setting('migrate.run_id')::uuid, source_table, legacy_id, reject_reason, payload
FROM buisy_nexus_staging.w_inventory
WHERE action = 'reject';

-- TODO(decision): once Nexus starts scanning migrated products, a rerun
-- overwrites status and location. Freeze them after go-live?
UPDATE inventory_products t
SET sku_id = w.sku_id, serial_number = w.serial, status = w.status, location_id = w.location_id,
    scan_in_time = w.scan_in_time, manufacture_date = w.manufacture_date, company_id = w.company_id,
    document_no = w.document_no, metadata = coalesce(t.metadata, '{}'::jsonb) || w.metadata,
    updated_at = now(), updated_by = 'buisy-migration'
FROM buisy_nexus_staging.w_inventory w
WHERE w.action = 'update' AND t.id = w.existing_id;

UPDATE buisy_nexus_staging.w_inventory SET new_id = existing_id WHERE action = 'update';

WITH ins AS (
  INSERT INTO inventory_products (sku_id, serial_number, document_no, location_id, status, scan_in_time,
                                  manufacture_date, company_id, metadata,
                                  created_at, created_by, updated_at, updated_by)
  SELECT sku_id, serial, document_no, location_id, status, scan_in_time,
         manufacture_date, company_id, metadata,
         now(), 'buisy-migration', now(), 'buisy-migration'
  FROM buisy_nexus_staging.w_inventory
  WHERE action = 'insert'
  RETURNING id, serial_number
)
UPDATE buisy_nexus_staging.w_inventory w
SET new_id = ins.id
FROM ins
WHERE w.action = 'insert' AND w.serial = ins.serial_number;

INSERT INTO buisy_nexus_staging.id_map (entity, legacy_source, legacy_id, new_id)
SELECT 'inventory_product', legacy_source, legacy_id, new_id::text
FROM buisy_nexus_staging.w_inventory
WHERE new_id IS NOT NULL
ON CONFLICT (entity, legacy_source, legacy_id) DO UPDATE SET new_id = EXCLUDED.new_id;
