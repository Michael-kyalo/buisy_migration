-- Customers with no agent, an agent that isn't a Buisy user, or an agent whose
-- email doesn't resolve to one Keycloak user. Same rule as
-- transforms/customerappdb/04_contacts_accounts.sql: agent code, then user id, then email.
WITH u AS (
  SELECT u.user_id, upper(nullif(trim(u.agent_code_buisy), '')) AS agent_code,
         lower(nullif(trim(u.user_email), '')) AS email,
         (SELECT count(*) FROM profile_keycloak_users k
          WHERE lower(trim(k.email)) = lower(nullif(trim(u.user_email), ''))) = 1 AS in_keycloak
  FROM profile_users u
),
c AS (
  SELECT c.*,
         coalesce(
           (SELECT u.user_id FROM u WHERE u.agent_code = upper(nullif(trim(c.scust_sales_agent_buisy_code), '')) LIMIT 1),
           (SELECT u.user_id FROM u WHERE u.user_id = trim(c.scust_sales_agent) LIMIT 1),
           (SELECT u.user_id FROM u WHERE u.email = lower(nullif(trim(c.scust_sales_agent), '')) LIMIT 1)
         ) AS agent_user_id
  FROM profile_customers c
)
SELECT *
FROM (
  SELECT c.scust_id, c.scust_customer_name, c.scust_sales_agent, c.scust_sales_agent_buisy_code,
         c.agent_user_id,
         CASE
           WHEN nullif(trim(c.scust_sales_agent), '') IS NULL
            AND nullif(trim(c.scust_sales_agent_buisy_code), '') IS NULL THEN 'no_agent'
           WHEN c.agent_user_id IS NULL THEN 'agent_not_a_buisy_user'
           WHEN NOT (SELECT in_keycloak FROM u WHERE u.user_id = c.agent_user_id) THEN 'agent_not_in_keycloak'
         END AS problem
  FROM c
) x
WHERE x.problem IS NOT NULL
ORDER BY x.problem, x.scust_id::bigint
