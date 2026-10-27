-- Serials used more than once among published inventory, within and across the two
-- inventory tables. Serial numbers are unique across all of inventory_products, so
-- only one of each survives (see transforms/deliverydb/05_inventory_products.sql).
-- Compared trimmed and upper-cased, like the transform.
SELECT UPPER(TRIM(inv_serial)) AS serial,
       COUNT(*) AS copies,
       GROUP_CONCAT(CONCAT(source_table, ':', inv_id, ' (', COALESCE(inv_sku, ''), ')') ORDER BY source_table, inv_id) AS rows_using_it
FROM (
  SELECT 'nav_inventory' AS source_table, inv_id, inv_sku, inv_serial FROM nav_inventory WHERE publish <> 0
  UNION ALL
  SELECT 'cookware', inv_id, inv_sku, inv_serial FROM nav_inventory__cookware WHERE publish <> 0
) i
WHERE NULLIF(TRIM(inv_serial), '') IS NOT NULL
GROUP BY UPPER(TRIM(inv_serial))
HAVING COUNT(*) > 1
ORDER BY copies DESC
