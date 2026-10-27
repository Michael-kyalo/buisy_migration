-- Every staged customer (sale) is either linked to a contact and an account, or rejected:
--   source_rows = target_rows + reject_rows
-- Customer-level rejects (nav_sale_customer) block both; account-level rejects
-- (nav_sale_customer.accounts) block only the account. Several sales can share
-- one contact (same phone), so contacts counts sales linked to a contact.
-- other_target_rows: rows created by the migration that no staged sale maps to.
WITH rej AS (
  SELECT source_table, count(DISTINCT legacy_id) AS n
  FROM buisy_nexus_staging.migration_rejects
  WHERE run_id = current_setting('migrate.transform_run_id')::uuid
  GROUP BY source_table
),
staged AS (
  SELECT scust_id::text AS legacy_id FROM buisy_nexus_staging.nav_sale_customer
),
mapped AS (
  SELECT s.legacy_id,
         mc.new_id::integer AS contact_id,
         ma.new_id::integer AS account_id,
         EXISTS (SELECT 1 FROM contacts t WHERE t.id = mc.new_id::integer) AS has_contact,
         EXISTS (SELECT 1 FROM accounts t WHERE t.id = ma.new_id::integer) AS has_account
  FROM staged s
  LEFT JOIN buisy_nexus_staging.id_map mc
    ON mc.entity = 'contact' AND mc.legacy_source = 'buisy.nav_sale_customer' AND mc.legacy_id = s.legacy_id
  LEFT JOIN buisy_nexus_staging.id_map ma
    ON ma.entity = 'account' AND ma.legacy_source = 'buisy.nav_sale_customer' AND ma.legacy_id = s.legacy_id
),
c (target_table, source_rows, target_rows, reject_rows, other_target_rows) AS (
  SELECT 'contacts',
         (SELECT count(*) FROM mapped),
         (SELECT count(*) FROM mapped WHERE has_contact),
         (SELECT coalesce(sum(n), 0) FROM rej WHERE source_table = 'nav_sale_customer'),
         (SELECT count(*) FROM contacts t
          WHERE t.created_by = 'buisy-migration'
            AND NOT EXISTS (SELECT 1 FROM mapped m WHERE m.contact_id = t.id))
  UNION ALL
  SELECT 'accounts',
         (SELECT count(*) FROM mapped),
         (SELECT count(*) FROM mapped WHERE has_account),
         (SELECT coalesce(sum(n), 0) FROM rej WHERE source_table IN ('nav_sale_customer', 'nav_sale_customer.accounts')),
         (SELECT count(*) FROM accounts t
          WHERE t.created_by = 'buisy-migration'
            AND NOT EXISTS (SELECT 1 FROM mapped m WHERE m.account_id = t.id))
)
SELECT target_table,
       'buisy.nav_sale_customer' AS legacy_source,
       source_rows,
       target_rows,
       reject_rows,
       other_target_rows,
       source_rows = target_rows + reject_rows AS ok
FROM c
ORDER BY target_table;
