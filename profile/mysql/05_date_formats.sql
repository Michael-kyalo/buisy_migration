-- profile:all
-- Buisy dates are varchar. For each date column: how many values are empty, parse
-- with the accepted formats (YYYY-MM-DD[ HH:MM[:SS]], DD/MM/YYYY[ HH:MM[:SS]]), or
-- don't parse, with an example of each. Unparsed values migrate as NULL; if many
-- don't parse, a format is missing from parse_buisy_ts (sql/bootstrap.sql) and
-- copy.buisyDateExpr (internal/copy/schema.go).
SELECT source_table, date_column,
       SUM(state = 'empty') AS empty_values,
       SUM(state = 'parsed') AS parsed,
       SUM(state = 'unparsed') AS unparsed,
       MIN(CASE WHEN state = 'parsed' THEN raw_value END) AS parsed_example,
       MIN(CASE WHEN state = 'unparsed' THEN raw_value END) AS unparsed_example
FROM (
  SELECT source_table, date_column, raw_value,
         CASE
           WHEN NULLIF(TRIM(raw_value), '') IS NULL THEN 'empty'
           WHEN COALESCE(
                  STR_TO_DATE(LEFT(TRIM(raw_value), 19), '%Y-%m-%d %H:%i:%s'),
                  STR_TO_DATE(LEFT(TRIM(raw_value), 16), '%Y-%m-%d %H:%i'),
                  STR_TO_DATE(LEFT(TRIM(raw_value), 10), '%Y-%m-%d'),
                  STR_TO_DATE(TRIM(raw_value), '%d/%m/%Y %H:%i:%s'),
                  STR_TO_DATE(TRIM(raw_value), '%d/%m/%Y %H:%i'),
                  STR_TO_DATE(TRIM(raw_value), '%d/%m/%Y')) IS NULL THEN 'unparsed'
           ELSE 'parsed'
         END AS state
  FROM (
    SELECT 'nav_inventory' AS source_table, 'inv_entry_date' AS date_column, inv_entry_date AS raw_value FROM nav_inventory
    UNION ALL SELECT 'nav_inventory', 'inv_manufacture_date', inv_manufacture_date FROM nav_inventory
    UNION ALL SELECT 'nav_inventory__cookware', 'inv_entry_date', inv_entry_date FROM nav_inventory__cookware
    UNION ALL SELECT 'nav_inventory__cookware', 'inv_manufacture_date', inv_manufacture_date FROM nav_inventory__cookware
    UNION ALL SELECT 'nav_sale_customer', 'scust_entry_date', scust_entry_date FROM nav_sale_customer
    UNION ALL SELECT 'nav_sku', 'sku_entry_date', sku_entry_date FROM nav_sku
  ) v
) s
GROUP BY source_table, date_column
ORDER BY source_table, date_column
