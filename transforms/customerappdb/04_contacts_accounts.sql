-- nav_sale_customer → contacts, accounts
--
-- Buisy customers are expected to exist in the customer app already. Each Buisy
-- row is a sale. The phone is the contact's identity, so repeat buyers share one
-- contact; each sale belongs to one account.
--   - Existing contact/account (created in Nexus): only empty fields are filled in;
--     values Nexus already has are never overwritten.
--   - Contact/account the migration created earlier: updated with Buisy values.
--   - No match in Nexus: created.
--
-- contacts.salesperson_id and accounts.sales_agent_id hold the agent's Keycloak id,
-- resolved through w_users (common/03_agents.sql).

-- 1. The agent behind each sale. scust_sales_agent_buisy_code is matched to
-- users.agent_code_buisy first, then scust_sales_agent to users.id, then to the email.
-- TODO(decision): confirm what scust_sales_agent holds.
DROP TABLE IF EXISTS buisy_nexus_staging.w_agent_lookup;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_agent_lookup AS
SELECT kind, match_key, min(keycloak_id) AS keycloak_id, count(*) AS candidates
FROM (
  SELECT 'code'::text AS kind, agent_code AS match_key, keycloak_id FROM buisy_nexus_staging.w_users WHERE agent_code IS NOT NULL
  UNION ALL
  SELECT 'id', legacy_id, keycloak_id FROM buisy_nexus_staging.w_users
  UNION ALL
  SELECT 'email', email, keycloak_id FROM buisy_nexus_staging.w_users WHERE email IS NOT NULL
) k
GROUP BY kind, match_key;

-- 2. One row per Buisy customer (sale).
DROP TABLE IF EXISTS buisy_nexus_staging.w_customers;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_customers AS
WITH c AS (
  SELECT c.*,
         nullif(regexp_replace(trim(c.scust_customer_name), '\s+', ' ', 'g'), '') AS full_name,
         -- TODO(decision): phone normalisation; spaces, dashes and brackets are removed.
         -- Confirm the format the customer app stores (e.g. 07…, 2547…, +2547…).
         nullif(regexp_replace(coalesce(c.scust_customer_main_tel, ''), '[^0-9+]', '', 'g'), '') AS phone,
         nullif(regexp_replace(coalesce(c.scust_customer_alt_tel, ''), '[^0-9+]', '', 'g'), '')  AS alt_phone,
         nullif(trim(c.scust_sales_agent_buisy_code), '') IS NOT NULL
           OR nullif(trim(c.scust_sales_agent), '') IS NOT NULL AS has_agent
  FROM buisy_nexus_staging.nav_sale_customer c
)
SELECT c.scust_id::text AS legacy_id,
       -- TODO(decision): Buisy has one name field. First word → first_name, the rest →
       -- last_name ('' for a single word, since both are required in Nexus).
       left(split_part(c.full_name, ' ', 1), 255)                                 AS first_name,
       left(coalesce(nullif(substr(c.full_name, length(split_part(c.full_name, ' ', 1)) + 2), ''), ''), 255) AS last_name,
       c.phone,
       left(c.alt_phone, 20)                                                      AS alt_phone,
       coalesce('phone:' || c.phone, 'scust:' || c.scust_id)                      AS contact_key,
       left(nullif(trim(c.scust_id_number), ''), 255)                             AS identification_number,
       left(nullif(trim(c.gender), ''), 6)                                        AS gender,
       left(nullif(trim(c.scust_county_name), ''), 100)                           AS region,
       left(nullif(trim(c.scust_sub_county), ''), 100)                            AS sub_region,
       left(nullif(trim(c.scust_home_address), ''), 255)                          AS address,
       left(nullif(trim(c.scust_village), ''), 100)                               AS location,
       left(nullif(trim(c.scust_country), ''), 255)                               AS countryname,
       coalesce(ac.keycloak_id, ai.keycloak_id, ae.keycloak_id)                   AS salesperson_id,
       -- accounts.external_id is the Buisy delivery code.
       nullif(trim(c.delivery_code), '')                                          AS external_id,
       -- TODO(decision): the account serial is scust_scanned_out_serial, else scust_stove_serials.
       nullif(trim(coalesce(nullif(trim(c.scust_scanned_out_serial), ''), c.scust_stove_serials)), '') AS serial,
       left(nullif(trim(c.sales_order_no), ''), 55)                               AS sales_order_no,
       left(nullif(trim(c.scust_sales_model), ''), 20)                            AS sales_model,
       buisy_nexus_staging.parse_buisy_ts(c.scust_entry_date)                     AS created_at,
       jsonb_strip_nulls(jsonb_build_object(
         'ward', c.scust_ward, 'county_id', nullif(c.scust_county_id, 0),
         'carbon_consent', c.scust_carbon_consent, 'distribution_channel', c.scust_distribution_channel,
         'sales_agent', c.scust_sales_agent, 'sales_agent_name', c.scust_sales_agent_name,
         'sales_agent_buisy_code', c.scust_sales_agent_buisy_code, 'stove_serials', c.scust_stove_serials,
         'scanned_out_serial', c.scust_scanned_out_serial, 'external_sale_id', c.external_sale_id,
         'delivery_code', c.delivery_code, 'entry_date', c.scust_entry_date, 'status', c.scust_status,
         'verified', c.scust_verified, 'sale_complete_status', c.scust_sale_complete_status,
         'kc_id', c.kc_id, 'pid', c.pid))                                       AS buisy,
       CASE
         -- TODO(decision): publish = 0 rows are treated as deleted in Buisy and not migrated.
         WHEN c.publish = 0 THEN 'unpublished_in_buisy'
         WHEN c.full_name IS NULL THEN 'customer_name_missing'
         WHEN length(c.phone) > 20 THEN 'phone_too_long'
         -- TODO(decision): customers with no agent at all load with salesperson_id NULL.
         WHEN c.has_agent AND coalesce(ac.keycloak_id, ai.keycloak_id, ae.keycloak_id) IS NULL THEN 'salesperson_unresolved'
       END AS reject_reason,
       to_jsonb(c) - 'consent_signature_final' - 'consent_signature_final_merge' - 'consent_str'
         - 'customer_with_stove_final' AS payload
FROM c
LEFT JOIN buisy_nexus_staging.w_agent_lookup ac
  ON ac.kind = 'code' AND ac.match_key = upper(nullif(trim(c.scust_sales_agent_buisy_code), '')) AND ac.candidates = 1
LEFT JOIN buisy_nexus_staging.w_agent_lookup ai
  ON ai.kind = 'id' AND ai.match_key = trim(c.scust_sales_agent)
LEFT JOIN buisy_nexus_staging.w_agent_lookup ae
  ON ae.kind = 'email' AND ae.match_key = lower(nullif(trim(c.scust_sales_agent), '')) AND ae.candidates = 1;

INSERT INTO buisy_nexus_staging.migration_rejects (run_id, source_table, legacy_id, reason, payload)
SELECT current_setting('migrate.run_id')::uuid, 'nav_sale_customer', legacy_id, reject_reason, payload
FROM buisy_nexus_staging.w_customers
WHERE reject_reason IS NOT NULL;

-- 3. contacts: one per contact_key. The phone is the contact's identity; customers
-- without a phone get a contact per sale.
-- TODO(decision): when several sales share a phone, the latest sale supplies the
-- contact's details.
DROP TABLE IF EXISTS buisy_nexus_staging.w_contacts;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_contacts AS
SELECT DISTINCT ON (contact_key)
       contact_key, first_name, last_name, phone, alt_phone, identification_number, gender,
       region, sub_region, address, location, countryname, salesperson_id, created_at,
       NULL::integer AS existing_id,
       NULL::text    AS existing_created_by,
       NULL::integer AS new_id,
       NULL::text    AS action
FROM buisy_nexus_staging.w_customers
WHERE reject_reason IS NULL
ORDER BY contact_key, created_at DESC NULLS LAST, legacy_id::bigint DESC;

-- A contact this migration created (found by its key, so it survives a dropped
-- staging schema), else the existing Nexus contact with the phone. Phones are
-- compared with spaces, dashes and brackets removed on both sides.
UPDATE buisy_nexus_staging.w_contacts w
SET existing_id = t.id, existing_created_by = t.created_by
FROM contacts t
WHERE t.created_by = 'buisy-migration' AND t.metadata ->> 'buisy_contact_key' = w.contact_key;

UPDATE buisy_nexus_staging.w_contacts w
SET existing_id = e.id, existing_created_by = e.created_by
FROM (
  SELECT DISTINCT ON (regexp_replace(phone, '[^0-9+]', '', 'g'))
         id, created_by, regexp_replace(phone, '[^0-9+]', '', 'g') AS phone
  FROM contacts
  WHERE nullif(trim(phone), '') IS NOT NULL
  ORDER BY regexp_replace(phone, '[^0-9+]', '', 'g'), id
) e
WHERE w.existing_id IS NULL AND w.phone IS NOT NULL AND e.phone = w.phone;

-- insert  no contact has the phone
-- update  a contact this migration created: Buisy values are applied
-- fill    a contact created in Nexus (expected for most customers): only its empty
--         fields are filled in; values Nexus already has are never overwritten
UPDATE buisy_nexus_staging.w_contacts
SET action = CASE WHEN existing_id IS NULL THEN 'insert'
                  WHEN existing_created_by = 'buisy-migration' THEN 'update'
                  ELSE 'fill' END;

UPDATE contacts t
SET first_name = w.first_name, last_name = w.last_name, alt_msisdn = w.alt_phone,
    identification_number = w.identification_number, gender = w.gender, region = w.region,
    sub_region = w.sub_region, address = w.address, "location" = w.location,
    countryname = w.countryname, salesperson_id = w.salesperson_id,
    updated_at = now(), updated_by = 'buisy-migration'
FROM buisy_nexus_staging.w_contacts w
WHERE w.action = 'update' AND t.id = w.existing_id;

UPDATE contacts t
SET alt_msisdn            = coalesce(nullif(t.alt_msisdn, ''), w.alt_phone),
    identification_number = coalesce(nullif(t.identification_number, ''), w.identification_number),
    gender                = coalesce(nullif(t.gender, ''), w.gender),
    region                = coalesce(nullif(t.region, ''), w.region),
    sub_region            = coalesce(nullif(t.sub_region, ''), w.sub_region),
    address               = coalesce(nullif(t.address, ''), w.address),
    "location"            = coalesce(nullif(t."location", ''), w.location),
    countryname           = coalesce(nullif(t.countryname, ''), w.countryname),
    salesperson_id        = coalesce(nullif(t.salesperson_id, ''), w.salesperson_id),
    updated_at = now(), updated_by = 'buisy-migration'
FROM buisy_nexus_staging.w_contacts w
WHERE w.action = 'fill' AND t.id = w.existing_id;

UPDATE buisy_nexus_staging.w_contacts SET new_id = existing_id WHERE action IN ('update', 'fill');

WITH ins AS (
  INSERT INTO contacts (first_name, last_name, phone, alt_msisdn, identification_number, gender,
                        region, sub_region, address, "location", countryname, salesperson_id,
                        metadata, created_at, created_by, updated_at, updated_by)
  SELECT first_name, last_name, phone, alt_phone, identification_number, gender,
         region, sub_region, address, location, countryname, salesperson_id,
         jsonb_build_object('legacy_source', 'buisy.nav_sale_customer', 'buisy_contact_key', contact_key),
         coalesce(created_at, now()), 'buisy-migration', now(), 'buisy-migration'
  FROM buisy_nexus_staging.w_contacts
  WHERE action = 'insert'
  RETURNING id, metadata ->> 'buisy_contact_key' AS contact_key
)
UPDATE buisy_nexus_staging.w_contacts w
SET new_id = ins.id
FROM ins
WHERE w.action = 'insert' AND w.contact_key = ins.contact_key;

INSERT INTO buisy_nexus_staging.id_map (entity, legacy_source, legacy_id, new_id)
SELECT 'contact', 'buisy.nav_sale_customer', c.legacy_id, w.new_id::text
FROM buisy_nexus_staging.w_customers c
JOIN buisy_nexus_staging.w_contacts w USING (contact_key)
WHERE c.reject_reason IS NULL AND w.new_id IS NOT NULL
ON CONFLICT (entity, legacy_source, legacy_id) DO UPDATE SET new_id = EXCLUDED.new_id;

-- 4. accounts: one per sale. accounts.external_id is the sale's delivery_code.
-- A sale finds its account in this order:
--   1. an account this migration created for the same sale (metadata legacy_id);
--   2. the account whose external_id is the sale's delivery_code;
--   3. its contact's only account, when the contact has exactly one account not
--      created by the migration and not matched in step 2, and exactly one Buisy
--      sale still unmatched;
--   4. none: when the contact has no such accounts, a new account is created with
--      external_id = delivery_code; otherwise the sale is ambiguous and rejected.
DROP TABLE IF EXISTS buisy_nexus_staging.w_accounts;
CREATE UNLOGGED TABLE buisy_nexus_staging.w_accounts AS
SELECT c.legacy_id, w.new_id AS contact_id, c.external_id, c.serial AS buisy_serial,
       c.serial, c.sales_order_no, c.sales_model, c.salesperson_id AS sales_agent_id, c.created_at,
       jsonb_build_object('legacy_source', 'buisy.nav_sale_customer', 'legacy_id', c.legacy_id, 'buisy', c.buisy) AS metadata,
       NULL::integer AS existing_id,
       NULL::text    AS action,
       NULL::integer AS new_id,
       NULL::text    AS reject_reason,
       c.buisy AS payload
FROM buisy_nexus_staging.w_customers c
JOIN buisy_nexus_staging.w_contacts w USING (contact_key)
WHERE c.reject_reason IS NULL;

-- TODO(decision): two sales with the same delivery_code can't share an account;
-- the lowest scust_id keeps it.
UPDATE buisy_nexus_staging.w_accounts w
SET action = 'reject', reject_reason = 'external_id_duplicate'
FROM (
  SELECT legacy_id, row_number() OVER (PARTITION BY external_id ORDER BY legacy_id::bigint) AS rn
  FROM buisy_nexus_staging.w_accounts
  WHERE external_id IS NOT NULL
) d
WHERE d.legacy_id = w.legacy_id AND d.rn > 1;

-- 1. Our own account for this sale.
UPDATE buisy_nexus_staging.w_accounts w
SET existing_id = t.id, action = 'update'
FROM accounts t
WHERE w.action IS NULL
  AND t.created_by = 'buisy-migration'
  AND t.metadata ->> 'legacy_source' = 'buisy.nav_sale_customer'
  AND t.metadata ->> 'legacy_id' = w.legacy_id;

-- 2. The account with the sale's delivery code.
-- TODO(decision): an account found by delivery code that belongs to a different
-- contact than the sale's phone is rejected rather than linked.
UPDATE buisy_nexus_staging.w_accounts w
SET existing_id = t.id,
    action = CASE WHEN t.contact_id IS DISTINCT FROM w.contact_id THEN 'reject'
                  WHEN t.created_by = 'buisy-migration' THEN 'reject' -- migrated for another sale
                  ELSE 'fill' END,
    reject_reason = CASE WHEN t.contact_id IS DISTINCT FROM w.contact_id THEN 'account_contact_mismatch'
                         WHEN t.created_by = 'buisy-migration' THEN 'external_id_duplicate' END
FROM accounts t
WHERE w.action IS NULL AND w.external_id IS NOT NULL AND t.external_id = w.external_id;

-- 3 and 4. The contact's only remaining account, or a new one.
WITH sales AS (
  SELECT contact_id, count(*) AS unmatched_sales
  FROM buisy_nexus_staging.w_accounts
  WHERE action IS NULL
  GROUP BY contact_id
),
nexus AS (
  SELECT s.contact_id, count(t.id) AS nexus_accounts, min(t.id) AS nexus_account_id
  FROM sales s
  LEFT JOIN accounts t
    ON t.contact_id = s.contact_id
   AND t.created_by IS DISTINCT FROM 'buisy-migration'
   AND NOT EXISTS (SELECT 1 FROM buisy_nexus_staging.w_accounts x
                   WHERE x.existing_id = t.id AND x.action <> 'reject')
  GROUP BY s.contact_id
)
UPDATE buisy_nexus_staging.w_accounts w
SET existing_id = CASE WHEN n.nexus_accounts = 1 AND s.unmatched_sales = 1 THEN n.nexus_account_id END,
    action = CASE WHEN n.nexus_accounts = 0 THEN 'insert'
                  WHEN n.nexus_accounts = 1 AND s.unmatched_sales = 1 THEN 'fill'
                  ELSE 'reject' END,
    reject_reason = CASE WHEN n.nexus_accounts > 0 AND NOT (n.nexus_accounts = 1 AND s.unmatched_sales = 1)
                         THEN 'account_ambiguous' END
FROM sales s
JOIN nexus n USING (contact_id)
WHERE w.action IS NULL AND w.contact_id = s.contact_id;

-- A new account needs its delivery code as external_id (required in Nexus).
UPDATE buisy_nexus_staging.w_accounts
SET action = 'reject', reject_reason = 'delivery_code_missing'
WHERE action = 'insert' AND external_id IS NULL;

-- accounts.serial_number is varchar(20) and unique when non-empty. A serial that
-- is too long, or already used by another account, is left empty and kept in
-- metadata.buisy.stove_serials / scanned_out_serial.
-- TODO(decision): which sale keeps a shared serial (current: the lowest scust_id).
UPDATE buisy_nexus_staging.w_accounts SET serial = NULL WHERE length(serial) > 20;
UPDATE buisy_nexus_staging.w_accounts w
SET serial = NULL
FROM (
  SELECT legacy_id, row_number() OVER (PARTITION BY serial ORDER BY legacy_id::bigint) AS rn
  FROM buisy_nexus_staging.w_accounts
  WHERE action <> 'reject' AND serial IS NOT NULL
) d
WHERE d.legacy_id = w.legacy_id AND d.rn > 1;
UPDATE buisy_nexus_staging.w_accounts w
SET serial = NULL
WHERE w.action <> 'reject' AND w.serial IS NOT NULL
  AND EXISTS (SELECT 1 FROM accounts t
              WHERE t.serial_number = w.serial AND t.id IS DISTINCT FROM w.existing_id);

INSERT INTO buisy_nexus_staging.migration_rejects (run_id, source_table, legacy_id, reason, payload)
SELECT current_setting('migrate.run_id')::uuid, 'nav_sale_customer.accounts', legacy_id, reject_reason, payload
FROM buisy_nexus_staging.w_accounts
WHERE action = 'reject';

UPDATE accounts t
SET contact_id = w.contact_id, serial_number = w.serial, sales_order_no = w.sales_order_no,
    sales_model = coalesce(w.sales_model, t.sales_model), sales_agent_id = w.sales_agent_id,
    metadata = coalesce(t.metadata, '{}'::jsonb) || w.metadata,
    updated_at = now(), updated_by = 'buisy-migration'
FROM buisy_nexus_staging.w_accounts w
WHERE w.action = 'update' AND t.id = w.existing_id;

-- An account created in Nexus: fill empty fields only. Buisy's details are added
-- to metadata without replacing any key Nexus already set.
UPDATE accounts t
SET serial_number  = coalesce(nullif(t.serial_number, ''), w.serial),
    sales_order_no = coalesce(nullif(t.sales_order_no, ''), w.sales_order_no),
    sales_agent_id = coalesce(nullif(t.sales_agent_id, ''), w.sales_agent_id),
    metadata       = w.metadata || coalesce(t.metadata, '{}'::jsonb),
    updated_at = now(), updated_by = 'buisy-migration'
FROM buisy_nexus_staging.w_accounts w
WHERE w.action = 'fill' AND t.id = w.existing_id;

UPDATE buisy_nexus_staging.w_accounts SET new_id = existing_id WHERE action IN ('update', 'fill');

-- TODO(decision): new accounts keep the default account_type (PAYGO); status and
-- delivery_date are left empty. Confirm what legacy Buisy sales should be.
WITH ins AS (
  INSERT INTO accounts (contact_id, external_id, serial_number, sales_order_no, sales_model,
                        sales_agent_id, metadata, created_at, created_by, updated_at, updated_by)
  SELECT contact_id, external_id, serial, sales_order_no, coalesce(sales_model, 'LEGACY'),
         sales_agent_id, metadata, coalesce(created_at, now()), 'buisy-migration', now(), 'buisy-migration'
  FROM buisy_nexus_staging.w_accounts
  WHERE action = 'insert'
  RETURNING id, external_id
)
UPDATE buisy_nexus_staging.w_accounts w
SET new_id = ins.id
FROM ins
WHERE w.action = 'insert' AND w.external_id = ins.external_id;

INSERT INTO buisy_nexus_staging.id_map (entity, legacy_source, legacy_id, new_id)
SELECT 'account', 'buisy.nav_sale_customer', legacy_id, new_id::text
FROM buisy_nexus_staging.w_accounts
WHERE new_id IS NOT NULL
ON CONFLICT (entity, legacy_source, legacy_id) DO UPDATE SET new_id = EXCLUDED.new_id;
