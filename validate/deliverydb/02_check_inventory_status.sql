-- Inventory per status: the rule applied to the staged source rows (minus this
-- run's rejects) vs the status of the target rows they map to. The rule is restated
-- here on purpose, independently of the transform; keep it in sync with
-- transforms/deliverydb/05_inventory_products.sql.
WITH src AS (
  SELECT 'buisy.nav_inventory' AS legacy_source, 'nav_inventory' AS source_table,
         inv_id::text AS legacy_id, is_sold, sales_order_no, inv_sub_status
  FROM buisy_nexus_staging.nav_inventory
  UNION ALL
  SELECT 'buisy.nav_inventory__cookware', 'nav_inventory__cookware',
         inv_id::text, is_sold, sales_order_no, inv_sub_status
  FROM buisy_nexus_staging.nav_inventory__cookware
),
kept AS (
  SELECT src.*
  FROM src
  WHERE NOT EXISTS (
    SELECT 1 FROM buisy_nexus_staging.migration_rejects r
    WHERE r.run_id = current_setting('migrate.transform_run_id')::uuid
      AND r.source_table = src.source_table
      AND r.legacy_id = src.legacy_id
  )
),
expected AS (
  SELECT legacy_source,
         CASE
           WHEN is_sold = 1 OR nullif(trim(sales_order_no), '') IS NOT NULL THEN 'SOLD'
           WHEN lower(trim(inv_sub_status)) IN ('in transit', 'in_transit', 'intransit') THEN 'IN_TRANSIT'
           ELSE 'SCANNED_IN'
         END AS status,
         count(*) AS n
  FROM kept
  GROUP BY 1, 2
),
actual AS (
  SELECT k.legacy_source, t.status, count(*) AS n
  FROM kept k
  JOIN buisy_nexus_staging.id_map m
    ON m.entity = 'inventory_product' AND m.legacy_source = k.legacy_source AND m.legacy_id = k.legacy_id
  JOIN inventory_products t ON t.id = m.new_id::bigint
  GROUP BY 1, 2
)
SELECT coalesce(e.legacy_source, a.legacy_source) AS legacy_source,
       coalesce(e.status, a.status)               AS status,
       coalesce(e.n, 0)                           AS source_rule,
       coalesce(a.n, 0)                           AS target,
       coalesce(e.n, 0) = coalesce(a.n, 0)        AS ok
FROM expected e
FULL JOIN actual a ON a.legacy_source = e.legacy_source AND a.status = e.status
ORDER BY 1, 2;
