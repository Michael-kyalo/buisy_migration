-- 20 random migrated serials: Buisy values (from the raw staging copy) beside Nexus values.
SELECT src.legacy_source,
       src.legacy_id,
       src.rec ->> 'inv_sku'        AS buisy_sku,
       s.code                       AS nexus_sku,
       src.rec ->> 'inv_serial'     AS buisy_serial,
       t.serial_number              AS nexus_serial,
       concat_ws(' / ', src.rec ->> 'inv_location', src.rec ->> 'inv_bin') AS buisy_location_bin,
       concat_ws(' / ', l.code, l.bin) AS nexus_location_bin,
       concat_ws(' / ',
         'is_sold=' || coalesce(src.rec ->> 'is_sold', 'null'),
         'so=' || coalesce(src.rec ->> 'sales_order_no', 'null'),
         'sub=' || coalesce(src.rec ->> 'inv_sub_status', 'null')) AS buisy_status,
       t.status                     AS nexus_status,
       src.rec ->> 'inv_entry_date' AS buisy_entry_date,
       t.scan_in_time               AS nexus_scan_in_time
FROM (
  SELECT 'buisy.nav_inventory' AS legacy_source, i.inv_id::text AS legacy_id, to_jsonb(i) AS rec
  FROM buisy_nexus_staging.nav_inventory i
  UNION ALL
  SELECT 'buisy.nav_inventory__cookware', c.inv_id::text, to_jsonb(c)
  FROM buisy_nexus_staging.nav_inventory__cookware c
) src
JOIN buisy_nexus_staging.id_map m
  ON m.entity = 'inventory_product' AND m.legacy_source = src.legacy_source AND m.legacy_id = src.legacy_id
JOIN inventory_products t ON t.id = m.new_id::bigint
LEFT JOIN skus s ON s.id = t.sku_id
LEFT JOIN locations l ON l.id = t.location_id
ORDER BY random()
LIMIT 20;
