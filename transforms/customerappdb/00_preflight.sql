-- customerappdb: fails before any data moves if the database doesn't have the
-- columns the transforms (common/ and customerappdb/) use. Target columns come
-- from schema/customerappdb.sql, staging columns from schema/buisy.sql.
DO $$
DECLARE
  target_cols text[] := ARRAY[
    'contacts.id', 'contacts.first_name', 'contacts.last_name', 'contacts.phone', 'contacts.alt_msisdn',
    'contacts.identification_number', 'contacts.gender', 'contacts.region', 'contacts.sub_region',
    'contacts.address', 'contacts.location', 'contacts.countryname', 'contacts.salesperson_id',
    'contacts.metadata', 'contacts.created_at', 'contacts.created_by', 'contacts.updated_at', 'contacts.updated_by',
    'accounts.id', 'accounts.contact_id', 'accounts.external_id', 'accounts.serial_number',
    'accounts.sales_order_no', 'accounts.sales_model', 'accounts.sales_agent_id', 'accounts.metadata',
    'accounts.created_at', 'accounts.created_by', 'accounts.updated_at', 'accounts.updated_by'
  ];
  staging_cols text[] := ARRAY[
    'users.id', 'users.user_email', 'users.agent_code_buisy', 'users.publish',
    'nav_sale_customer.scust_id', 'nav_sale_customer.scust_customer_name',
    'nav_sale_customer.scust_customer_main_tel', 'nav_sale_customer.scust_customer_alt_tel',
    'nav_sale_customer.scust_id_number', 'nav_sale_customer.gender', 'nav_sale_customer.scust_country',
    'nav_sale_customer.scust_county_name', 'nav_sale_customer.scust_sub_county',
    'nav_sale_customer.scust_village', 'nav_sale_customer.scust_home_address',
    'nav_sale_customer.scust_sales_agent', 'nav_sale_customer.scust_sales_agent_buisy_code',
    'nav_sale_customer.scust_entry_date', 'nav_sale_customer.scust_scanned_out_serial',
    'nav_sale_customer.scust_stove_serials', 'nav_sale_customer.sales_order_no',
    'nav_sale_customer.external_sale_id', 'nav_sale_customer.delivery_code', 'nav_sale_customer.scust_sales_model', 'nav_sale_customer.publish',
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
      USING HINT = 'Compare with schema/customerappdb.sql and fix the transforms or the schema file.';
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
