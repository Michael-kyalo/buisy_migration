-- Published customers grouped by phone: contacts are keyed by phone, so sales that
-- share a phone become one contact. Shows missing phones, phones longer than
-- contacts.phone allows (20), and phones shared by several sales.
SELECT CASE
         WHEN phone = '' THEN 'missing'
         WHEN CHAR_LENGTH(phone) > 20 THEN 'too_long'
         ELSE 'shared'
       END AS issue,
       phone,
       COUNT(*) AS sales,
       GROUP_CONCAT(scust_id ORDER BY scust_id) AS scust_ids
FROM (
  SELECT scust_id, REGEXP_REPLACE(COALESCE(scust_customer_main_tel, ''), '[^0-9+]', '') AS phone
  FROM nav_sale_customer
  WHERE publish <> 0
) c
GROUP BY phone
HAVING phone = '' OR CHAR_LENGTH(phone) > 20 OR COUNT(*) > 1
ORDER BY sales DESC
