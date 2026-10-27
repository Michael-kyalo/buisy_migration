-- Rows created by the migration whose references don't resolve. Every count should be 0.
SELECT x.target_table, x.reference, x.unresolved, x.unresolved = 0 AS ok
FROM (
  SELECT 'contacts' AS target_table, 'salesperson_id -> keycloak user' AS reference, count(*) AS unresolved
  FROM contacts t
  WHERE t.created_by = 'buisy-migration' AND t.salesperson_id IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM buisy_nexus_staging.keycloak_users k WHERE k.id = t.salesperson_id)
  UNION ALL
  SELECT 'accounts', 'contact_id -> contacts', count(*)
  FROM accounts t
  WHERE t.created_by = 'buisy-migration'
    AND NOT EXISTS (SELECT 1 FROM contacts c WHERE c.id = t.contact_id)
  UNION ALL
  SELECT 'accounts', 'sales_agent_id -> keycloak user', count(*)
  FROM accounts t
  WHERE t.created_by = 'buisy-migration' AND t.sales_agent_id IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM buisy_nexus_staging.keycloak_users k WHERE k.id = t.sales_agent_id)
) x
ORDER BY 1, 2;
