-- Distinct inv_location / inv_bin pairs of published inventory, loaded into the
-- session temp table profile_bins.
SELECT 'nav_inventory' AS source_table, inv_location, inv_bin, COUNT(*) AS row_count
FROM nav_inventory WHERE publish <> 0 GROUP BY inv_location, inv_bin
UNION ALL
SELECT 'nav_inventory__cookware', inv_location, inv_bin, COUNT(*)
FROM nav_inventory__cookware WHERE publish <> 0 GROUP BY inv_location, inv_bin
