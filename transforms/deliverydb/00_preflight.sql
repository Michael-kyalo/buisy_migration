-- deliverydb: fails before any data moves if the database doesn't have the
-- columns the transforms (common/ and deliverydb/) use. Target columns come from
-- schema/deliverydb.sql, staging columns from schema/buisy.sql. Keep this list in
-- sync when the transforms change.
DO $$
DECLARE
  target_cols text[] := ARRAY[
    'locations.id', 'locations.code', 'locations.bin', 'locations.name',
    'dimensions.id',
    'companies.code', 'companies.currency_code',
    'companies.id', 'companies.name',
    'skus.id', 'skus.code', 'skus.name', 'skus.generation', 'skus.classification', 'skus.color',
    'skus.base_unit', 'skus.category_type', 'skus.serial_number_length', 'skus.stock',
    'skus.has_warranty', 'skus.warranty_period', 'skus.status', 'skus.currency', 'skus.company_id',
    'skus.created_at', 'skus.created_by', 'skus.updated_at', 'skus.updated_by',
    'agent_skus.id', 'agent_skus.agent_email', 'agent_skus.sku_id',
    'agent_skus.created_at', 'agent_skus.created_by', 'agent_skus.updated_at', 'agent_skus.updated_by',
    'location_users.id', 'location_users.location_id', 'location_users.external_id', 'location_users.email',
    'location_users.created_at', 'location_users.createdby', 'location_users.updated_at', 'location_users.updatedby',
    'inventory_products.id', 'inventory_products.sku_id', 'inventory_products.serial_number',
    'inventory_products.document_no', 'inventory_products.location_id', 'inventory_products.status',
    'inventory_products.scan_in_time', 'inventory_products.manufacture_date', 'inventory_products.company_id',
    'inventory_products.metadata', 'inventory_products.created_at', 'inventory_products.created_by',
    'inventory_products.updated_at', 'inventory_products.updated_by'
  ];
  staging_cols text[] := ARRAY[
    'nav_sku.sku_id', 'nav_sku.sku_sku', 'nav_sku.sku_itemname', 'nav_sku.sku_productgeneration',
    'nav_sku.sku_productionclassification', 'nav_sku.sku_color', 'nav_sku.sku_serialnumberlength',
    'nav_sku.sku_baseunit', 'nav_sku.sku_stock', 'nav_sku.sku_entry_date', 'nav_sku.sku_status',
    'nav_sku.sku_category', 'nav_sku.sku_has_warranty', 'nav_sku.sku_warranty_period',
    'nav_sku.company', 'nav_sku.publish',
    'users.id', 'users.user_email', 'users.agent_code_buisy', 'users.skus', 'users.locations', 'users.publish',
    'nav_inventory.inv_id', 'nav_inventory.inv_sku', 'nav_inventory.inv_serial', 'nav_inventory.inv_bin',
    'nav_inventory.inv_location', 'nav_inventory.inv_doc_no', 'nav_inventory.inv_company',
    'nav_inventory.inv_entry_date', 'nav_inventory.inv_manufacture_date', 'nav_inventory.is_sold',
    'nav_inventory.sales_order_no', 'nav_inventory.inv_sub_status', 'nav_inventory.publish',
    'nav_inventory__cookware.inv_id', 'nav_inventory__cookware.inv_sku', 'nav_inventory__cookware.inv_serial',
    'nav_inventory__cookware.inv_bin', 'nav_inventory__cookware.inv_location',
    'nav_inventory__cookware.inv_doc_no', 'nav_inventory__cookware.inv_company',
    'nav_inventory__cookware.inv_entry_date', 'nav_inventory__cookware.inv_manufacture_date',
    'nav_inventory__cookware.is_sold', 'nav_inventory__cookware.sales_order_no',
    'nav_inventory__cookware.inv_sub_status', 'nav_inventory__cookware.publish',
    'keycloak_users.id', 'keycloak_users.email'
  ];
  missing text[];
BEGIN
  SELECT array_agg(r ORDER BY r) INTO missing
  FROM unnest(target_cols) AS r
  WHERE NOT EXISTS (
    SELECT 1 FROM information_schema.columns c
    WHERE c.table_schema = current_schema()
      AND c.table_name || '.' || c.column_name = r
  );
  IF missing IS NOT NULL THEN
    RAISE EXCEPTION 'preflight: columns in database % assumed by transforms are missing: %', current_database(),
      array_to_string(missing, ', ')
      USING HINT = 'Compare with schema/deliverydb.sql and fix the transforms or the schema file.';
  END IF;

  SELECT array_agg(r ORDER BY r) INTO missing
  FROM unnest(staging_cols) AS r
  WHERE NOT EXISTS (
    SELECT 1 FROM information_schema.columns c
    WHERE c.table_schema = 'buisy_nexus_staging'
      AND c.table_name || '.' || c.column_name = r
  );
  IF missing IS NOT NULL THEN
    RAISE EXCEPTION 'preflight: staging columns assumed by transforms are missing: %',
      array_to_string(missing, ', ')
      USING HINT = 'Run the copy step first. If it has run, the Buisy columns differ from schema/buisy.sql.';
  END IF;
END
$$;
