-- Rows created by the migration whose references don't resolve. Every count should be 0.
SELECT x.target_table, x.reference, x.unresolved, x.unresolved = 0 AS ok
FROM (
  SELECT 'inventory_products' AS target_table, 'sku_id -> skus' AS reference, count(*) AS unresolved
  FROM inventory_products t
  WHERE t.created_by = 'buisy-migration'
    AND NOT EXISTS (SELECT 1 FROM skus s WHERE s.id = t.sku_id)
  UNION ALL
  -- Buisy always has inv_location; the item must have a Nexus location.
  SELECT 'inventory_products', 'location_id -> locations (Buisy bin/location set)', count(*)
  FROM inventory_products t
  WHERE t.created_by = 'buisy-migration'
    AND coalesce(nullif(trim(t.metadata -> 'buisy' ->> 'bin'), ''), nullif(trim(t.metadata -> 'buisy' ->> 'location'), '')) IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM locations l WHERE l.id = t.location_id)
  UNION ALL
  SELECT 'agent_skus', 'sku_id -> skus', count(*)
  FROM agent_skus t
  WHERE t.created_by = 'buisy-migration'
    AND NOT EXISTS (SELECT 1 FROM skus s WHERE s.id = t.sku_id)
  UNION ALL
  SELECT 'agent_skus', 'agent_email set', count(*)
  FROM agent_skus t
  WHERE t.created_by = 'buisy-migration' AND nullif(trim(t.agent_email), '') IS NULL
  UNION ALL
  SELECT 'location_users', 'location_id -> locations', count(*)
  FROM location_users t
  WHERE t.createdby = 'buisy-migration'
    AND NOT EXISTS (SELECT 1 FROM locations l WHERE l.id = t.location_id)
  UNION ALL
  SELECT 'location_users', 'external_id -> keycloak user', count(*)
  FROM location_users t
  WHERE t.createdby = 'buisy-migration'
    AND NOT EXISTS (SELECT 1 FROM buisy_nexus_staging.keycloak_users k WHERE k.id = t.external_id)
) x
ORDER BY 1, 2;
