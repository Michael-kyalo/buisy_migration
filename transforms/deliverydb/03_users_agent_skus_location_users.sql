-- users → agent_skus, location_users
-- Agents come from w_users, built by common/03_agents.sql. See 02_skus.sql for the
-- insert / update / adopt / reject pattern.
--
-- 1. agent_skus: one row per (user, SKU). Natural key: (agent_email, sku_id).
-- users.skus is a comma-separated list of SKU codes.
DROP TABLE IF EXISTS buisy_nexus_staging.w_agent_skus;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_agent_skus AS
WITH pairs AS (
  SELECT DISTINCT u.id::text AS user_legacy_id, upper(trim(tok)) AS sku_ref
  FROM buisy_nexus_staging.users u
  CROSS JOIN LATERAL regexp_split_to_table(coalesce(u.skus, ''), ',') AS tok
  WHERE nullif(trim(tok), '') IS NOT NULL
)
SELECT p.user_legacy_id || ':' || p.sku_ref AS legacy_id,
       wu.email                              AS agent_email,
       CASE WHEN bc.candidates = 1 THEN bc.new_id
            WHEN bc.match_key IS NULL THEN bi.new_id
       END                                   AS sku_id,
       NULL::bigint                          AS existing_id,
       NULL::bigint                          AS new_id,
       NULL::text                            AS action,
       CASE WHEN wu.publish = 0 THEN 'unpublished_in_buisy'
            WHEN wu.email IS NULL THEN 'agent_email_missing'
            WHEN bc.candidates > 1 THEN 'sku_ambiguous'
            WHEN bc.match_key IS NULL AND bi.new_id IS NULL THEN 'sku_unresolved'
       END                                   AS reject_reason,
       jsonb_build_object('user_id', p.user_legacy_id, 'sku', p.sku_ref) AS payload
FROM pairs p
JOIN buisy_nexus_staging.w_users wu ON wu.legacy_id = p.user_legacy_id
LEFT JOIN buisy_nexus_staging.w_sku_lookup bc ON bc.kind = 'code' AND bc.match_key = p.sku_ref
LEFT JOIN buisy_nexus_staging.w_sku_lookup bi ON bi.kind = 'id' AND bi.match_key = p.sku_ref;

UPDATE buisy_nexus_staging.w_agent_skus w
SET existing_id = e.id
FROM (
  SELECT DISTINCT ON (lower(trim(agent_email)), sku_id) id, lower(trim(agent_email)) AS agent_email, sku_id
  FROM agent_skus
  ORDER BY lower(trim(agent_email)), sku_id, id
) e
WHERE w.reject_reason IS NULL AND e.agent_email = w.agent_email AND e.sku_id = w.sku_id;

-- An existing pair has nothing to update, whoever created it.
UPDATE buisy_nexus_staging.w_agent_skus
SET action = CASE WHEN reject_reason IS NOT NULL THEN 'reject'
                  WHEN existing_id IS NULL THEN 'insert'
                  ELSE 'adopt' END;

INSERT INTO buisy_nexus_staging.migration_rejects (run_id, source_table, legacy_id, reason, payload)
SELECT current_setting('migrate.run_id')::uuid, 'users.agent_skus', legacy_id, reject_reason, payload
FROM buisy_nexus_staging.w_agent_skus
WHERE action = 'reject';

UPDATE buisy_nexus_staging.w_agent_skus SET new_id = existing_id WHERE action = 'adopt';

-- Two Buisy users can share an email; their pairs collapse into one row.
WITH ins AS (
  INSERT INTO agent_skus (agent_email, sku_id, created_at, created_by, updated_at, updated_by)
  SELECT DISTINCT agent_email, sku_id, now(), 'buisy-migration', now(), 'buisy-migration'
  FROM buisy_nexus_staging.w_agent_skus
  WHERE action = 'insert'
  RETURNING id, agent_email, sku_id
)
UPDATE buisy_nexus_staging.w_agent_skus w
SET new_id = ins.id
FROM ins
WHERE w.action = 'insert' AND w.agent_email = ins.agent_email AND w.sku_id = ins.sku_id;

INSERT INTO buisy_nexus_staging.id_map (entity, legacy_source, legacy_id, new_id)
SELECT 'agent_sku', 'buisy.users', legacy_id, new_id::text
FROM buisy_nexus_staging.w_agent_skus
WHERE new_id IS NOT NULL
ON CONFLICT (entity, legacy_source, legacy_id) DO UPDATE SET new_id = EXCLUDED.new_id;

-- 2. location_users: one row per (user, location). Natural key: (location_id, email),
-- matching the table's unique constraint. external_id is the user's Keycloak id.
-- users.locations is a comma-separated list of bins, matched to locations.bin.
-- A bin shared by several locations is ambiguous.
DROP TABLE IF EXISTS buisy_nexus_staging.w_location_users;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_location_users AS
WITH entries AS (
  SELECT DISTINCT u.id::text AS user_legacy_id, upper(trim(tok)) AS loc_ref
  FROM buisy_nexus_staging.users u
  CROSS JOIN LATERAL regexp_split_to_table(coalesce(u.locations, ''), ',') AS tok
  WHERE nullif(trim(tok), '') IS NOT NULL
)
SELECT e.user_legacy_id || ':' || e.loc_ref AS legacy_id,
       wu.email,
       wu.keycloak_id                        AS external_id,
       CASE WHEN lb.candidates = 1 THEN lb.location_id END AS location_id,
       NULL::bigint                          AS existing_id,
       NULL::text                            AS existing_created_by,
       NULL::bigint                          AS new_id,
       NULL::text                            AS action,
       CASE WHEN wu.publish = 0 THEN 'unpublished_in_buisy'
            WHEN wu.email IS NULL THEN 'agent_email_missing'
            WHEN lb.candidates > 1 THEN 'location_ambiguous'
            WHEN lb.match_key IS NULL THEN 'location_unmatched'
            WHEN wu.keycloak_id IS NULL THEN 'agent_unresolved'
       END                                   AS reject_reason,
       jsonb_build_object('user_id', e.user_legacy_id, 'bin', e.loc_ref) AS payload
FROM entries e
JOIN buisy_nexus_staging.w_users wu ON wu.legacy_id = e.user_legacy_id
LEFT JOIN buisy_nexus_staging.w_location_lookup lb ON lb.kind = 'bin' AND lb.match_key = e.loc_ref;

UPDATE buisy_nexus_staging.w_location_users w
SET existing_id = e.id, existing_created_by = e.createdby
FROM (
  SELECT DISTINCT ON (location_id, lower(trim(email))) id, location_id, lower(trim(email)) AS email, createdby
  FROM location_users
  ORDER BY location_id, lower(trim(email)), id
) e
WHERE w.reject_reason IS NULL AND e.location_id = w.location_id AND e.email = w.email;

UPDATE buisy_nexus_staging.w_location_users
SET action = CASE WHEN reject_reason IS NOT NULL THEN 'reject'
                  WHEN existing_id IS NULL THEN 'insert'
                  WHEN existing_created_by = 'buisy-migration' THEN 'update'
                  ELSE 'adopt' END;

INSERT INTO buisy_nexus_staging.migration_rejects (run_id, source_table, legacy_id, reason, payload)
SELECT current_setting('migrate.run_id')::uuid, 'users.location_users', legacy_id, reject_reason, payload
FROM buisy_nexus_staging.w_location_users
WHERE action = 'reject';

UPDATE location_users t
SET external_id = w.external_id, updated_at = now(), updatedby = 'buisy-migration'
FROM buisy_nexus_staging.w_location_users w
WHERE w.action = 'update' AND t.id = w.existing_id;

UPDATE buisy_nexus_staging.w_location_users SET new_id = existing_id WHERE action IN ('update', 'adopt');

WITH ins AS (
  INSERT INTO location_users (location_id, email, external_id, created_at, createdby, updated_at, updatedby)
  SELECT DISTINCT ON (location_id, email) location_id, email, external_id,
         now(), 'buisy-migration', now(), 'buisy-migration'
  FROM buisy_nexus_staging.w_location_users
  WHERE action = 'insert'
  ORDER BY location_id, email, legacy_id
  RETURNING id, location_id, email
)
UPDATE buisy_nexus_staging.w_location_users w
SET new_id = ins.id
FROM ins
WHERE w.action = 'insert' AND w.location_id = ins.location_id AND w.email = ins.email;

INSERT INTO buisy_nexus_staging.id_map (entity, legacy_source, legacy_id, new_id)
SELECT 'location_user', 'buisy.users', legacy_id, new_id::text
FROM buisy_nexus_staging.w_location_users
WHERE new_id IS NOT NULL
ON CONFLICT (entity, legacy_source, legacy_id) DO UPDATE SET new_id = EXCLUDED.new_id;
