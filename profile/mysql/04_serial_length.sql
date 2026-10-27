-- Missing serials, and serials whose length differs from inv_serial_length (0 = not set).
SELECT 'nav_inventory' AS source_table, inv_id, inv_sku, inv_serial, inv_serial_length,
       CHAR_LENGTH(TRIM(inv_serial)) AS actual_length,
       CASE WHEN NULLIF(TRIM(inv_serial), '') IS NULL THEN 'missing' ELSE 'wrong_length' END AS problem
FROM nav_inventory
WHERE publish <> 0
  AND (NULLIF(TRIM(inv_serial), '') IS NULL
       OR (inv_serial_length > 0 AND CHAR_LENGTH(TRIM(inv_serial)) <> inv_serial_length))
UNION ALL
SELECT 'nav_inventory__cookware', inv_id, inv_sku, inv_serial, inv_serial_length,
       CHAR_LENGTH(TRIM(inv_serial)),
       CASE WHEN NULLIF(TRIM(inv_serial), '') IS NULL THEN 'missing' ELSE 'wrong_length' END
FROM nav_inventory__cookware
WHERE publish <> 0
  AND (NULLIF(TRIM(inv_serial), '') IS NULL
       OR (inv_serial_length > 0 AND CHAR_LENGTH(TRIM(inv_serial)) <> inv_serial_length))
