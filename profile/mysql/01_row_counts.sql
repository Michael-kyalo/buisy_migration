-- profile:all
-- Row counts per source table, and how many are unpublished (publish = 0, not migrated).
SELECT 'nav_sku' AS source_table, COUNT(*) AS row_count, SUM(publish = 0) AS unpublished FROM nav_sku
UNION ALL SELECT 'users', COUNT(*), SUM(publish = 0) FROM users
UNION ALL SELECT 'nav_sale_customer', COUNT(*), SUM(publish = 0) FROM nav_sale_customer
UNION ALL SELECT 'nav_inventory', COUNT(*), SUM(publish = 0) FROM nav_inventory
UNION ALL SELECT 'nav_inventory__cookware', COUNT(*), SUM(publish = 0) FROM nav_inventory__cookware
UNION ALL SELECT 'nav_dimension', COUNT(*), SUM(publish = 0) FROM nav_dimension
