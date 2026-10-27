-- Inventory SKUs with no published row in nav_sku (matched on nav_sku.sku_sku).
SELECT 'nav_inventory' AS source_table, i.inv_sku, COUNT(*) AS rows_affected
FROM nav_inventory i
LEFT JOIN nav_sku s ON UPPER(TRIM(s.sku_sku)) = UPPER(TRIM(i.inv_sku)) AND s.publish <> 0
WHERE i.publish <> 0 AND s.sku_id IS NULL
GROUP BY i.inv_sku
UNION ALL
SELECT 'nav_inventory__cookware', c.inv_sku, COUNT(*)
FROM nav_inventory__cookware c
LEFT JOIN nav_sku s ON UPPER(TRIM(s.sku_sku)) = UPPER(TRIM(c.inv_sku)) AND s.publish <> 0
WHERE c.publish <> 0 AND s.sku_id IS NULL
GROUP BY c.inv_sku
ORDER BY rows_affected DESC
