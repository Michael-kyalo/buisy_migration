-- deliverydb data that exists before the migration.
INSERT INTO companies (id, name, code, currency_code) VALUES
  (1, 'Burn Manufacturing Kenya', 'BMK', 'KES'),
  (2, 'Burn Uganda',              'BMU', 'UGX');

INSERT INTO locations (id, code, bin, name, company_id) VALUES
  (1, 'NBO-WH', 'BIN-A', 'Nairobi Warehouse A', 1),
  (2, 'NBO-WH', 'BIN-B', 'Nairobi Warehouse B', 1),
  (3, 'MSA-WH', 'BIN-A', 'Mombasa Warehouse',   1), -- BIN-A is shared: resolved with the location code
  (4, 'KSM-WH', 'BIN-K', 'Kisumu',              1),
  (5, 'DUP',    'BIN-D', 'Duplicate one',       1),
  (6, 'DUP',    'BIN-D', 'Duplicate two',       1); -- DUP / BIN-D can't be resolved
SELECT setval('locations_id_seq', 100);

INSERT INTO dimensions (code, dimension_code, name) VALUES ('KE', 'COUNTRY', 'Kenya');

-- Created in Nexus; the migration must never modify these.
INSERT INTO skus (id, code, name, company_id, currency, created_by) VALUES
  (1, 'NATIVE-1', 'Created in Nexus', 1, 'KES', 'nexus-admin'),
  (2, 'ECOA-2',   'Ecoa (Nexus name)', 1, 'KES', 'nexus-admin'); -- Buisy ECOA-2 is adopted
SELECT setval('skus_id_seq', 100);

INSERT INTO inventory_products (sku_id, serial_number, document_no, status, created_by)
VALUES (1, 'SN-NATIVE', 'NX-1', 'SCANNED_IN', 'nexus');

INSERT INTO location_users (location_id, email, external_id, createdby)
VALUES (4, 'bob@burn.com', 'kc-bob', 'nexus'); -- Bob's Kisumu entry is adopted

-- Tables named like Buisy tables; the tool must only ever touch buisy_nexus_staging.<name>.
CREATE TABLE users (id serial PRIMARY KEY, email text);
INSERT INTO users (email) VALUES ('nexus-native@burn.com');
CREATE TABLE nav_inventory (id serial PRIMARY KEY, note text);
INSERT INTO nav_inventory (note) VALUES ('not the Buisy table');
