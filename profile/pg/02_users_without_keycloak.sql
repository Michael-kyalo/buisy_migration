-- Buisy users whose email doesn't match exactly one Keycloak user.
-- Same rule as transforms/common/03_agents.sql (email only, case-insensitive).
SELECT *
FROM (
  SELECT u.user_id, u.user_email, u.agent_code_buisy, u.publish,
         (SELECT count(*) FROM profile_keycloak_users k
          WHERE lower(trim(k.email)) = lower(nullif(trim(u.user_email), ''))) AS keycloak_matches
  FROM profile_users u
) m
WHERE m.keycloak_matches <> 1
ORDER BY m.user_id::bigint
