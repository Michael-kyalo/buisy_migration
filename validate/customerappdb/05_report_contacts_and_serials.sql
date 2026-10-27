-- How migrated sales landed: contacts inserted, updated, or linked to an existing
-- Nexus contact by phone; and account serials left empty because they were too
-- long or already used by another account (the Buisy value stays in metadata).
SELECT 'contacts' AS what, action AS outcome, count(*) AS n
FROM buisy_nexus_staging.w_contacts
GROUP BY action
UNION ALL
SELECT 'accounts', action, count(*)
FROM buisy_nexus_staging.w_accounts
GROUP BY action
UNION ALL
SELECT 'account serial', 'cleared (too long or shared)', count(*)
FROM buisy_nexus_staging.w_accounts
WHERE action <> 'reject' AND buisy_serial IS NOT NULL AND serial IS NULL
ORDER BY 1, 2;
