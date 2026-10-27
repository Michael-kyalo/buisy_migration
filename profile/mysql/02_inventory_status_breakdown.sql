-- profile:all
-- Status breakdown of published inventory: inv_status (numeric code), inv_sub_status,
-- is_sold and whether a sales order is set.
SELECT 'nav_inventory' AS source_table, inv_status, inv_sub_status, is_sold,
       CASE WHEN NULLIF(TRIM(sales_order_no), '') IS NULL THEN 0 ELSE 1 END AS has_sales_order,
       COUNT(*) AS row_count
FROM nav_inventory
WHERE publish <> 0
GROUP BY inv_status, inv_sub_status, is_sold, has_sales_order
UNION ALL
SELECT 'nav_inventory__cookware', inv_status, inv_sub_status, is_sold,
       CASE WHEN NULLIF(TRIM(sales_order_no), '') IS NULL THEN 0 ELSE 1 END,
       COUNT(*)
FROM nav_inventory__cookware
WHERE publish <> 0
GROUP BY inv_status, inv_sub_status, is_sold, 5
ORDER BY source_table, row_count DESC
