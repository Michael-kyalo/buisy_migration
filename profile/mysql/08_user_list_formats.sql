-- Examples of users.skus and users.locations, which the transforms split into
-- agent_skus and location_users rows. Confirms the list format (comma separated?
-- JSON array?) and whether entries are codes, ids or names.
SELECT id, user_email,
       LEFT(skus, 80) AS skus_example,
       LEFT(locations, 80) AS locations_example
FROM users
WHERE publish <> 0
  AND (NULLIF(TRIM(skus), '') IS NOT NULL OR NULLIF(TRIM(locations), '') IS NOT NULL)
ORDER BY id
