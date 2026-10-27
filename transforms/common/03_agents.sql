-- users → Keycloak user map. Runs in every target database: deliverydb needs it
-- for location_users.external_id, customerappdb for contacts.salesperson_id and
-- accounts.sales_agent_id. Both databases therefore resolve users identically.
--
-- Matching is by email only (users.user_email = Keycloak email, case-insensitive);
-- Buisy users have no username to fall back to. More than one Keycloak user with
-- the same email is ambiguous and rejected.

DROP TABLE IF EXISTS buisy_nexus_staging.w_users;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_users AS
WITH u AS (
  SELECT u.id::text                              AS legacy_id,
         lower(nullif(trim(u.user_email), ''))   AS email,
         upper(nullif(trim(u.agent_code_buisy), '')) AS agent_code,
         u.publish,
         to_jsonb(u)                             AS payload
  FROM buisy_nexus_staging.users u
),
by_email AS (
  SELECT u.legacy_id, min(k.id) AS keycloak_id, count(*) AS matches
  FROM u
  JOIN buisy_nexus_staging.keycloak_users k ON lower(trim(k.email)) = u.email
  GROUP BY u.legacy_id
)
SELECT u.legacy_id,
       u.email,
       u.agent_code,
       u.publish,
       u.payload,
       CASE WHEN be.matches = 1 THEN be.keycloak_id END AS keycloak_id,
       CASE WHEN u.email IS NULL THEN 'email_missing'
            WHEN be.matches IS NULL THEN 'keycloak_unmatched'
            WHEN be.matches > 1 THEN 'keycloak_ambiguous_email'
       END AS reject_reason
FROM u
LEFT JOIN by_email be USING (legacy_id);

INSERT INTO buisy_nexus_staging.migration_rejects (run_id, source_table, legacy_id, reason, payload)
SELECT current_setting('migrate.run_id')::uuid, 'users', legacy_id, reject_reason, payload
FROM buisy_nexus_staging.w_users
WHERE reject_reason IS NOT NULL;

-- The agent map is derived entirely from this run, so a user who no longer
-- resolves must not keep a stale mapping.
DELETE FROM buisy_nexus_staging.id_map WHERE entity = 'agent';
INSERT INTO buisy_nexus_staging.id_map (entity, legacy_source, legacy_id, new_id)
SELECT 'agent', 'buisy.users', legacy_id, keycloak_id
FROM buisy_nexus_staging.w_users
WHERE keycloak_id IS NOT NULL;
