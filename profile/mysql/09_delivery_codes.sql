-- Published customers whose delivery_code (→ accounts.external_id) is missing or
-- used by more than one sale. A sale without one can still match its contact's
-- only account, but a new account can't be created for it; of a shared code, the
-- lowest scust_id keeps it.
SELECT CASE WHEN delivery_code = '' THEN 'missing' ELSE 'shared' END AS issue,
       delivery_code,
       COUNT(*) AS sales,
       GROUP_CONCAT(scust_id ORDER BY scust_id) AS scust_ids
FROM (
  SELECT scust_id, TRIM(COALESCE(delivery_code, '')) AS delivery_code
  FROM nav_sale_customer
  WHERE publish <> 0
) c
GROUP BY delivery_code
HAVING delivery_code = '' OR COUNT(*) > 1
ORDER BY sales DESC
