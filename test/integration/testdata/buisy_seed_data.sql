-- Buisy fixture rows on the real schema (schema/buisy.sql). Each row exercises a
-- rule; the expected outcome is noted. Dates are varchar in Buisy, so a mix of
-- formats is used on purpose.

-- Not in the provided schema; assumed to match nav_inventory.
CREATE TABLE nav_inventory__cookware LIKE nav_inventory;

INSERT INTO nav_sku (sku_id, sku_sku, sku_itemname, sku_productgeneration, sku_productionclassification, sku_color,
                     sku_serialnumberlength, sku_baseunit, sku_stock, sku_entry_date, sku_status, sku_category,
                     sku_has_warranty, sku_warranty_period, company, publish) VALUES
  (1, 'JIKO-1',  'Jikokoa', 'G2', 'Stove', 'Black', '5', 'PCS', '100', '2023-01-01 08:00:00', 1, 'stoves', 1, '24 months', 'Burn Manufacturing Kenya', 1),  -- insert
  (2, 'ECOA-2',  'Ecoa',    'G1', 'Stove', 'Grey',  '5', 'PCS', '50',  '2023-01-02 08:00:00', 1, 'stoves', 0, NULL,        'Burn Manufacturing Kenya', 1),  -- adopt (exists in Nexus)
  (3, 'jiko-1 ', 'Copy',    NULL, NULL,    NULL,    NULL, NULL, NULL,  NULL,                  1, NULL,     0, NULL,        'Burn Manufacturing Kenya', 1),  -- sku_code_duplicate
  (4, '',        NULL,      NULL, NULL,    NULL,    NULL, NULL, NULL,  NULL,                  1, NULL,     0, NULL,        'Burn Manufacturing Kenya', 1),  -- sku_code_missing
  (5, 'POT-1',   'Pot',     NULL, 'Cookware', NULL, '4', 'PCS', 'n/a', '15/03/2023',          1, 'cookware', 0, NULL,      'bmk', 1),                       -- insert; DD/MM date; company by code
  (6, 'OLD-9',   'Retired', NULL, NULL,    NULL,    NULL, NULL, NULL,  NULL,                  0, NULL,     0, NULL,        'Burn Manufacturing Kenya', 0),  -- unpublished_in_buisy
  (7, 'XCO-1',   'Other',   NULL, NULL,    NULL,    NULL, NULL, NULL,  NULL,                  1, NULL,     0, NULL,        'Unknown Co', 1);                -- company_unmatched

INSERT INTO users (id, user_first_name, user_email, user_password, token, agent_code_buisy, skus, locations, publish) VALUES
  (1, 'Alice', 'Alice@Burn.com', 'secret-hash', 'tok', 'AG001', 'JIKO-1, ECOA-2',        'BIN-B',           1), -- Keycloak by email
  (2, 'Bob',   'bob@burn.com',   'secret-hash', NULL,  'AG002', 'JIKO-1,NOPE-9',        'BIN-K, nowhere',  1), -- NOPE-9 unresolved; BIN-K adopted
  (3, 'Carol', 'carol@burn.com', NULL,          NULL,  'AG003', '5',                    'BIN-K',           1), -- keycloak_unmatched; SKU by Buisy id
  (4, 'Dave',  'dave@burn.com',  NULL,          NULL,  'AG004', NULL,                   'BIN-A',           1), -- keycloak_ambiguous_email; BIN-A is shared
  (5, 'Eve',   NULL,             NULL,          NULL,  'AG005', 'JIKO-1',               'BIN-B',           1), -- email_missing
  (6, 'Frank', 'frank@burn.com', NULL,          NULL,  'AG006', 'JIKO-1',               NULL,              0); -- unpublished; keycloak_unmatched

INSERT INTO nav_inventory (inv_id, inv_sku, inv_serial, inv_serial_length, inv_location, inv_bin, inv_doc_no, inv_company,
                           inv_entry_date, inv_manufacture_date, inv_status, is_sold, sales_order_no, inv_sub_status, publish) VALUES
  (1,  'JIKO-1', 'SN001',     5, 'NBO-WH',  'BIN-B', 'DOC1', 'Burn Manufacturing Kenya', '2024-01-02 10:00:00', '2023-12-01', 1, 0, NULL,   NULL,          1), -- SCANNED_IN; bin unique
  (2,  'JIKO-1', 'SN002',     5, 'NBO-WH',  'BIN-A', 'DOC2', 'Burn Manufacturing Kenya', '2024-01-03 10:00:00', NULL,         2, 1, NULL,   NULL,          1), -- SOLD; shared bin narrowed by code
  (3,  'ECOA-2', 'SN003',     5, 'MSA-WH',  'BIN-A', 'DOC3', 'Burn Manufacturing Kenya', '2024-01-04 10:00:00', NULL,         1, 0, 'SO-1', NULL,          1), -- SOLD; adopted SKU
  (4,  'ECOA-2', 'SN004',     5, 'KSM-WH',  NULL,    'DOC4', 'Burn Manufacturing Kenya', '05/01/2024 10:00',    NULL,         1, 0, '',     'In Transit',  1), -- IN_TRANSIT; DD/MM date
  (5,  'JIKO-1', 'sn001 ',    5, 'NBO-WH',  'BIN-B', 'DOC5', 'Burn Manufacturing Kenya', '2023-01-01 10:00:00', NULL,         1, 0, NULL,   NULL,          1), -- duplicate_serial (older)
  (6,  'NOPE-9', 'SN006',     5, 'KSM-WH',  NULL,    'DOC6', 'Burn Manufacturing Kenya', '2024-01-06 10:00:00', NULL,         1, 0, NULL,   NULL,          1), -- sku_unresolved
  (7,  'JIKO-1', 'SN007',     5, 'NOWHERE', 'BIN-X', 'DOC7', 'Burn Manufacturing Kenya', '2024-01-07 10:00:00', NULL,         1, 0, NULL,   NULL,          1), -- location_unmatched
  (8,  'JIKO-1', '',          5, 'KSM-WH',  NULL,    'DOC8', 'Burn Manufacturing Kenya', '2024-01-08 10:00:00', NULL,         1, 0, NULL,   NULL,          1), -- serial_missing
  (9,  'JIKO-1', 'SN009',     5, 'DUP',     'BIN-D', 'DOC9', 'Burn Manufacturing Kenya', '2024-01-09 10:00:00', NULL,         1, 0, NULL,   NULL,          1), -- location_unmatched (ambiguous)
  (10, 'JIKO-1', 'SN010',     5, 'KSM-WH',  NULL,    NULL,   'Burn Manufacturing Kenya', '0000-00-00 00:00:00', NULL,         1, 0, NULL,   NULL,          1), -- loads; zero date → NULL; no doc no
  (11, 'jiko-1', 'SN011',     6, 'KSM-WH',  NULL,    'DOC11','Burn Manufacturing Kenya', '2024-01-11',          'garbage',    1, 0, NULL,   'in transit ', 1), -- IN_TRANSIT; unparsable manufacture date
  (12, 'POT-1',  'sn-native', 9, 'KSM-WH',  NULL,    'DOC12','Burn Manufacturing Kenya', '2024-01-12 10:00:00', NULL,         1, 0, NULL,   NULL,          1), -- serial_exists_in_nexus
  (13, 'JIKO-1', 'SN013',     5, 'KSM-WH',  NULL,    'DOC13','Burn Manufacturing Kenya', '2024-01-13 10:00:00', NULL,         1, 0, NULL,   NULL,          0), -- unpublished_in_buisy
  (14, 'JIKO-1', 'SN014',     5, 'KSM-WH',  NULL,    'DOC14','Mystery Ltd',              '2024-01-14 10:00:00', NULL,         1, 0, NULL,   NULL,          1); -- company_unmatched

INSERT INTO nav_inventory__cookware (inv_id, inv_sku, inv_serial, inv_serial_length, inv_location, inv_bin, inv_doc_no, inv_company,
                                     inv_entry_date, inv_status, is_sold, publish) VALUES
  (1, 'POT-1',  'P001',  4, 'KSM-WH', NULL, 'CDOC1', 'Burn Manufacturing Kenya', '2024-02-01 10:00:00', 1, 0, 1), -- SCANNED_IN; same inv_id as nav_inventory 1
  (2, 'JIKO-1', 'SN002', 5, 'KSM-WH', NULL, 'CDOC2', 'Burn Manufacturing Kenya', '2024-02-02 10:00:00', 1, 0, 1), -- duplicate_serial; nav_inventory wins
  (3, 'POT-1',  'P003',  4, 'KSM-WH', NULL, 'CDOC3', 'Burn Manufacturing Kenya', '2024-02-03 10:00:00', 1, 1, 1); -- SOLD

INSERT INTO nav_sale_customer (scust_id, scust_customer_name, scust_customer_main_tel, scust_customer_alt_tel, gender,
                               scust_country, scust_county_name, scust_sales_agent, scust_sales_agent_buisy_code,
                               scust_entry_date, scust_scanned_out_serial, scust_stove_serials, sales_order_no,
                               delivery_code, publish) VALUES
  (1,  'Jane Wanjiru Doe', '0712 345 678', NULL,         'Female', 'Kenya', 'Nairobi', NULL,             'AG001', '2023-03-01 09:00:00', 'SN001', NULL, 'SO-1', 'DC001',     1), -- new contact + account
  (2,  'John',             '0722000000',   '0733-111222','Male',   'Kenya', 'Kiambu',  '2',              NULL,    '2023-03-02 09:00:00', NULL,    NULL, NULL,   NULL,        1), -- agent by user id; new contact; delivery_code_missing
  (3,  'Jane W. Doe',      '0712345678',   NULL,         'Female', 'Kenya', 'Nairobi', 'alice@burn.com', NULL,    '2023-04-01 09:00:00', 'SN001', NULL, NULL,   'DC003',     1), -- same phone as 1 → same contact; shared serial cleared
  (4,  'Carol Customer',   '0733000000',   NULL,         NULL,     'Kenya', NULL,      NULL,             'AG003', NULL,                  NULL,    NULL, NULL,   'DC004',     1), -- salesperson_unresolved
  (5,  'No Agent',         '0744000000',   NULL,         NULL,     'Kenya', NULL,      NULL,             NULL,    '05/03/2023',          NULL,    'P009', NULL, 'DC005',     1), -- no agent; salesperson NULL
  (6,  '   ',              '0766000000',   NULL,         NULL,     'Kenya', NULL,      NULL,             'AG001', NULL,                  NULL,    NULL, NULL,   'DC006',     1), -- customer_name_missing
  (7,  'Native Phone',     '0799000000',   NULL,         NULL,     'Kenya', 'Nairobi', NULL,             'AG001', '2023-05-01',          'SN777', NULL, NULL,   'DC-NATIVE', 1), -- existing contact + account (by delivery code): empty fields filled
  (8,  'Dup Delivery',     '0755000000',   NULL,         NULL,     'Kenya', NULL,      NULL,             'AG001', '2023-05-02',          NULL,    NULL, NULL,   'DC001',     1), -- new contact; external_id_duplicate
  (9,  'Gone',             '0777000000',   NULL,         NULL,     'Kenya', NULL,      NULL,             'AG001', NULL,                  NULL,    NULL, NULL,   'DC009',     0), -- unpublished_in_buisy
  (10, 'Phoneless Pat',    NULL,           NULL,         NULL,     'Kenya', NULL,      NULL,             'AG002', '2023-06-01',          NULL,    NULL, NULL,   'DC010',     1), -- contact keyed by sale
  (11, 'Many Accounts',    '0788 000 000', NULL,         NULL,     'Kenya', NULL,      NULL,             'AG001', '2023-07-01',          NULL,    NULL, NULL,   NULL,        1), -- existing contact, several accounts, no code: account_ambiguous
  (12, 'Wrong Contact',    '0711111111',   NULL,         NULL,     'Kenya', NULL,      NULL,             'AG001', '2023-08-01',          NULL,    NULL, NULL,   'EXT-N2A',   1), -- new contact; code belongs to another contact: account_contact_mismatch
  (13, 'Many Accounts B',  '0788000000',   NULL,         NULL,     'Kenya', NULL,      NULL,             'AG001', '2023-09-01',          NULL,    NULL, NULL,   'EXT-N2B',   1); -- existing contact; account found by delivery code: filled

INSERT INTO nav_dimension (dimen_id, dimen_dimensioncode, dimen_code, dimen_name, publish) VALUES
  (1, 'COUNTRY', 'KE', 'Kenya', 1);
