-- Published Buisy customers, loaded into the session temp table profile_customers.
SELECT scust_id, scust_customer_name, scust_sales_agent, scust_sales_agent_buisy_code
FROM nav_sale_customer
WHERE publish <> 0
