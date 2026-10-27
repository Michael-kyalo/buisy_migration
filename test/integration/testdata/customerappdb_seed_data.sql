-- customerappdb data that exists before the migration. Buisy customers are
-- expected to be here already; the migration only fills in empty fields on them.

-- Buisy customer 7: account found by its delivery code (DC-NATIVE). The contact's
-- region and the account's serial and agent are empty (filled from Buisy);
-- salesperson_id and sales_order_no are set (kept).
INSERT INTO contacts (first_name, last_name, phone, salesperson_id, created_by)
VALUES ('Native', 'Person', '0799000000', 'kc-native-agent', 'customer-app');
INSERT INTO accounts (contact_id, external_id, sales_order_no, created_by)
SELECT id, 'DC-NATIVE', 'SO-NX', 'customer-app' FROM contacts WHERE phone = '0799000000';

-- Buisy customers 11 and 13 share this phone. 13's delivery code finds EXT-N2B;
-- 11 has no code and two accounts remain, so it can't pick one. Customer 12's
-- code EXT-N2A belongs to this contact, not 12's.
INSERT INTO contacts (first_name, last_name, phone, created_by)
VALUES ('Many', 'Accounts', '0788000000', 'customer-app');
INSERT INTO accounts (contact_id, external_id, created_by)
SELECT id, x, 'customer-app' FROM contacts, (VALUES ('EXT-N2A'), ('EXT-N2B'), ('EXT-N2C')) v (x) WHERE phone = '0788000000';
