-- Every staged source row is either in the target or rejected:
--   source_rows = target_rows + reject_rows
-- target_rows: staged rows whose id_map entry points at an existing target row
-- (inserted, updated or adopted). Scoped to what is staged, so it also holds for
-- partial (--since) runs.
-- other_target_rows: rows created by the migration (created_by = 'buisy-migration')
-- that no staged row maps to, e.g. earlier --since windows or rows since deleted in
-- Buisy. Reported, not checked.
WITH rej AS (
  SELECT source_table, count(DISTINCT legacy_id) AS n
  FROM buisy_nexus_staging.migration_rejects
  WHERE run_id = current_setting('migrate.transform_run_id')::uuid
  GROUP BY source_table
),
staged (target_table, entity, legacy_source, reject_source, legacy_id) AS (
  SELECT 'skus', 'sku', 'buisy.nav_sku', 'nav_sku', sku_id::text
  FROM buisy_nexus_staging.nav_sku
  UNION ALL
  -- One source row per (user, SKU) and (user, location) pair, as split by 03_users_*.
  SELECT 'agent_skus', 'agent_sku', 'buisy.users', 'users.agent_skus', legacy_id
  FROM buisy_nexus_staging.w_agent_skus
  UNION ALL
  SELECT 'location_users', 'location_user', 'buisy.users', 'users.location_users', legacy_id
  FROM buisy_nexus_staging.w_location_users
  UNION ALL
  SELECT 'inventory_products', 'inventory_product', 'buisy.nav_inventory', 'nav_inventory', inv_id::text
  FROM buisy_nexus_staging.nav_inventory
  UNION ALL
  SELECT 'inventory_products', 'inventory_product', 'buisy.nav_inventory__cookware', 'nav_inventory__cookware', inv_id::text
  FROM buisy_nexus_staging.nav_inventory__cookware
),
mapped AS (
  SELECT s.*, m.new_id::bigint AS target_id,
         CASE s.target_table
           WHEN 'skus'               THEN EXISTS (SELECT 1 FROM skus t WHERE t.id = m.new_id::bigint)
           WHEN 'agent_skus'         THEN EXISTS (SELECT 1 FROM agent_skus t WHERE t.id = m.new_id::bigint)
           WHEN 'location_users'     THEN EXISTS (SELECT 1 FROM location_users t WHERE t.id = m.new_id::bigint)
           WHEN 'inventory_products' THEN EXISTS (SELECT 1 FROM inventory_products t WHERE t.id = m.new_id::bigint)
         END AS in_target
  FROM staged s
  LEFT JOIN buisy_nexus_staging.id_map m
    ON m.entity = s.entity AND m.legacy_source = s.legacy_source AND m.legacy_id = s.legacy_id
),
c AS (
  SELECT target_table, legacy_source, reject_source,
         count(*) AS source_rows,
         count(*) FILTER (WHERE in_target) AS target_rows
  FROM mapped
  GROUP BY 1, 2, 3
),
other (target_table, legacy_source, n) AS (
  SELECT 'skus', 'buisy.nav_sku', count(*) FROM skus t
  WHERE t.created_by = 'buisy-migration'
    AND NOT EXISTS (SELECT 1 FROM mapped m WHERE m.target_table = 'skus' AND m.target_id = t.id)
  UNION ALL
  SELECT 'agent_skus', 'buisy.users', count(*) FROM agent_skus t
  WHERE t.created_by = 'buisy-migration'
    AND NOT EXISTS (SELECT 1 FROM mapped m WHERE m.target_table = 'agent_skus' AND m.target_id = t.id)
  UNION ALL
  SELECT 'location_users', 'buisy.users', count(*) FROM location_users t
  WHERE t.createdby = 'buisy-migration'
    AND NOT EXISTS (SELECT 1 FROM mapped m WHERE m.target_table = 'location_users' AND m.target_id = t.id)
  UNION ALL
  SELECT 'inventory_products', t.metadata ->> 'legacy_source', count(*) FROM inventory_products t
  WHERE t.created_by = 'buisy-migration'
    AND NOT EXISTS (SELECT 1 FROM mapped m WHERE m.target_table = 'inventory_products' AND m.target_id = t.id)
  GROUP BY 2
)
SELECT c.target_table,
       c.legacy_source,
       c.source_rows,
       c.target_rows,
       coalesce(r.n, 0) AS reject_rows,
       coalesce(o.n, 0) AS other_target_rows,
       c.source_rows = c.target_rows + coalesce(r.n, 0) AS ok
FROM c
LEFT JOIN rej r ON r.source_table = c.reject_source
LEFT JOIN other o ON o.target_table = c.target_table AND o.legacy_source = c.legacy_source
ORDER BY c.target_table, c.legacy_source;
