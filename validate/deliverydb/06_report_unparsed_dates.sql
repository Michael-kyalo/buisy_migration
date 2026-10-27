-- Buisy dates (stored as text) that are filled in but couldn't be parsed by
-- parse_buisy_ts. They migrate as NULL; a high count means a format isn't handled.
SELECT x.source_table, x.date_column, count(*) AS unparsed_rows, min(x.raw_value) AS example
FROM (
  SELECT 'nav_inventory' AS source_table, 'inv_entry_date' AS date_column, inv_entry_date AS raw_value
  FROM buisy_nexus_staging.nav_inventory
  UNION ALL
  SELECT 'nav_inventory', 'inv_manufacture_date', inv_manufacture_date
  FROM buisy_nexus_staging.nav_inventory
  UNION ALL
  SELECT 'nav_inventory__cookware', 'inv_entry_date', inv_entry_date
  FROM buisy_nexus_staging.nav_inventory__cookware
  UNION ALL
  SELECT 'nav_inventory__cookware', 'inv_manufacture_date', inv_manufacture_date
  FROM buisy_nexus_staging.nav_inventory__cookware
  UNION ALL
  SELECT 'nav_sku', 'sku_entry_date', sku_entry_date
  FROM buisy_nexus_staging.nav_sku
) x
WHERE nullif(trim(x.raw_value), '') IS NOT NULL
  AND buisy_nexus_staging.parse_buisy_ts(x.raw_value) IS NULL
GROUP BY 1, 2
ORDER BY 1, 2;
