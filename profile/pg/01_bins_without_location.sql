-- inv_location / inv_bin pairs that don't resolve to exactly one Nexus location.
-- Same rule as transforms/deliverydb/05_inventory_products.sql: the bin alone,
-- else bin + location code, else the location code alone.
WITH l AS (
  SELECT id, upper(nullif(trim(bin), '')) AS bin_key, upper(nullif(trim(code), '')) AS code_key FROM locations
),
b AS (
  SELECT source_table, inv_location, inv_bin, row_count::bigint AS inventory_rows,
         upper(nullif(trim(inv_bin), '')) AS bin_key, upper(nullif(trim(inv_location), '')) AS code_key
  FROM profile_bins
)
SELECT b.source_table, b.inv_location, b.inv_bin, b.inventory_rows,
       (SELECT count(*) FROM l WHERE l.bin_key = b.bin_key) AS by_bin,
       (SELECT count(*) FROM l WHERE l.bin_key = b.bin_key AND l.code_key = b.code_key) AS by_bin_and_code,
       (SELECT count(*) FROM l WHERE l.code_key = b.code_key) AS by_code
FROM b
WHERE (b.bin_key IS NOT NULL OR b.code_key IS NOT NULL)
  AND (SELECT count(*) FROM l WHERE l.bin_key = b.bin_key) <> 1
  AND (SELECT count(*) FROM l WHERE l.bin_key = b.bin_key AND l.code_key = b.code_key) <> 1
  AND (SELECT count(*) FROM l WHERE l.code_key = b.code_key) <> 1
ORDER BY b.inventory_rows DESC
