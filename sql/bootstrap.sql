-- The buisy_nexus_staging schema and control tables. Idempotent; runs at the start of every
-- invocation. Raw copies of Buisy tables are created by the copy step (UNLOGGED,
-- no indexes). The control tables below are logged: they must survive a crash.

CREATE SCHEMA IF NOT EXISTS buisy_nexus_staging;

CREATE TABLE IF NOT EXISTS buisy_nexus_staging.migration_runs (
  id            bigserial PRIMARY KEY,
  run_id        uuid        NOT NULL,
  env           text        NOT NULL,
  step          text        NOT NULL,
  detail        text,
  started_at    timestamptz NOT NULL DEFAULT now(),
  finished_at   timestamptz,
  rows_read     bigint,
  rows_written  bigint,
  rows_rejected bigint,
  status        text        NOT NULL DEFAULT 'running',
  error         text
);

-- The --since date and --limit of a partial run; NULL for a full run.
ALTER TABLE buisy_nexus_staging.migration_runs ADD COLUMN IF NOT EXISTS since date;
ALTER TABLE buisy_nexus_staging.migration_runs ADD COLUMN IF NOT EXISTS row_limit bigint;

CREATE TABLE IF NOT EXISTS buisy_nexus_staging.id_map (
  entity        text NOT NULL,
  legacy_source text NOT NULL,
  legacy_id     text NOT NULL,
  new_id        text NOT NULL,
  PRIMARY KEY (entity, legacy_source, legacy_id)
);

CREATE TABLE IF NOT EXISTS buisy_nexus_staging.migration_rejects (
  id           bigserial PRIMARY KEY,
  run_id       uuid        NOT NULL,
  source_table text        NOT NULL,
  legacy_id    text,
  reason       text        NOT NULL,
  payload      jsonb,
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS migration_rejects_run_idx
  ON buisy_nexus_staging.migration_rejects (run_id, source_table);

-- Buisy stores dates as varchar. Accepted formats:
--   YYYY-MM-DD[ HH:MM[:SS]]   and   DD/MM/YYYY[ HH:MM[:SS]]   (day first)
-- Anything else, including zero dates, returns NULL. Must match
-- copy.buisyDateExpr (the MySQL side of --since) and profile/mysql/07.
-- TODO(decision): confirm the formats Buisy actually writes (see migrate profile).
CREATE OR REPLACE FUNCTION buisy_nexus_staging.parse_buisy_ts(v text)
RETURNS timestamp LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  s text := btrim(v);
BEGIN
  IF s ~ '^\d{4}-\d{1,2}-\d{1,2}([ T]\d{1,2}:\d{2}(:\d{2})?)?' THEN
    RETURN left(s, 19)::timestamp;
  ELSIF s ~ '^\d{1,2}/\d{1,2}/\d{4}$' THEN
    RETURN to_timestamp(s, 'DD/MM/YYYY')::timestamp;
  ELSIF s ~ '^\d{1,2}/\d{1,2}/\d{4} \d{1,2}:\d{2}$' THEN
    RETURN to_timestamp(s, 'DD/MM/YYYY HH24:MI')::timestamp;
  ELSIF s ~ '^\d{1,2}/\d{1,2}/\d{4} \d{1,2}:\d{2}:\d{2}$' THEN
    RETURN to_timestamp(s, 'DD/MM/YYYY HH24:MI:SS')::timestamp;
  END IF;
  RETURN NULL;
EXCEPTION WHEN others THEN
  RETURN NULL; -- e.g. 0000-00-00, 2024-02-31
END
$$;

-- Leading integer of a free-text number such as '12' or '12 months'; NULL otherwise.
CREATE OR REPLACE FUNCTION buisy_nexus_staging.parse_buisy_int(v text)
RETURNS bigint LANGUAGE sql IMMUTABLE AS $$
  SELECT (substring(btrim(v) FROM '^\d{1,15}'))::bigint
$$;

-- Loaded from Keycloak during the copy step; a copy like the Buisy tables.
CREATE UNLOGGED TABLE IF NOT EXISTS buisy_nexus_staging.keycloak_users (
  id        text NOT NULL,
  username  text,
  email     text,
  enabled   boolean,
  loaded_at timestamptz NOT NULL DEFAULT now()
);
