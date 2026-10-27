# Decisions still to make

Each item below is marked `TODO(decision)` in the SQL (`make todo` lists them). The migration already runs with the current behaviour shown, so nothing is blocked. Every row is a guess to confirm or change. Settled decisions are in [decisions-made.md](decisions-made.md).

**Check the data first.** For most of these, `migrate profile --env=development` shows the real values and counts to decide from. The "Evidence" column says where to look.

## Highest impact

These change what lands in Nexus the most. Please answer these first.

| # | Question | Current behaviour | Evidence |
|---|---|---|---|
| 19 | What does `nav_sale_customer.scust_sales_agent` hold: a user id, an email, a name? | Tried as agent code (`scust_sales_agent_buisy_code`), then user id, then email | profile `pg/03_customers_salesperson` |
| 20 | What phone format does the customer app store (`07…`, `2547…`, `+2547…`)? | Spaces, dashes and brackets removed; otherwise kept as is | profile `mysql/07_customer_phones` |
| 1 | Are `publish = 0` rows deleted in Buisy? | Treated as deleted and not migrated | profile `mysql/01_row_counts` (unpublished column) |
| 2 | Which date formats does Buisy write, and in which time zone? | `YYYY-MM-DD…` and day-first `DD/MM/YYYY…`, read as UTC | profile `mysql/05_date_formats` |
| 12 | Inventory status: is "everything not sold or in transit = SCANNED_IN" right? Is there a source for SCANNED_OUT? | SCANNED_IN; SCANNED_OUT never produced | profile `mysql/02_inventory_status_breakdown` |
| 18 | After Nexus starts scanning migrated items, may a rerun still overwrite status and location? | Yes, reruns overwrite them | none; a go-live policy question |

## SKUs

[transforms/deliverydb/02_skus.sql](../transforms/deliverydb/02_skus.sql)

| # | Question | Current behaviour |
|---|---|---|
| 3 | What unit is `sku_warranty_period` in? | Its leading number is taken as `warranty_period` |
| 4 | What do the `sku_status` codes mean in Nexus terms? | 1 → `ACTIVE`, anything else → `INACTIVE` |
| 5 | What happens to a SKU whose company isn't in Nexus? | Rejected; a blank company loads with no company or currency |
| 6 | Which SKU wins when a company has the same code twice? | Lowest `sku_id` |
| 7 | A SKU that already exists in Nexus: link only, or also update it? | Linked to, never modified |
| 8 | Does `nav_inventory.inv_sku` hold the SKU code? | Yes; numeric values are also tried as `sku_id` |

## Inventory

[transforms/deliverydb/05_inventory_products.sql](../transforms/deliverydb/05_inventory_products.sql)

| # | Question | Current behaviour | Evidence |
|---|---|---|---|
| 9 | Does `nav_inventory__cookware` have the same columns as `nav_inventory`? | Assumed yes; the transform fails clearly if not | `SHOW CREATE TABLE nav_inventory__cookware` |
| 10 | How does an item find its location? | `inv_bin` → `locations.bin` if unique; else bin + `inv_location` → `locations.code`; else the code alone | profile `pg/01_bins_without_location` |
| 11 | Which `inv_sub_status` values mean "in transit"? | `in transit`, `in_transit`, `intransit` (any case) | profile `mysql/02_inventory_status_breakdown` |
| 13 | Should `scan_in_time` come from `inv_entry_date`? | Yes (proposed) | |
| 14 | `document_no` is required in Nexus. What should items without `inv_doc_no` get? | `'BUISY-MIGRATION'` | |
| 15 | Are serials compared case-insensitively? | Yes, trimmed and case-insensitive | profile `mysql/03_duplicate_serials` |
| 16 | When a serial appears twice, which row wins? | nav_inventory over cookware, then latest entry date, then lowest id | profile `mysql/03_duplicate_serials` |
| 17 | Items with neither bin nor location: load without a location, or reject? | Loaded with no location | |

## Customers

[transforms/customerappdb/04_contacts_accounts.sql](../transforms/customerappdb/04_contacts_accounts.sql)

| # | Question | Current behaviour | Evidence |
|---|---|---|---|
| 21 | Buisy has one name field and new contacts need first and last name. How should it be split? | First word → `first_name`, rest → `last_name` (empty if one word) | |
| 22 | When several sales share a phone, which sale's details go on the contact? | The latest sale's | profile `mysql/07_customer_phones` |
| 23 | Customers with no sales agent: load or reject? | Loaded with no salesperson | profile `pg/03_customers_salesperson` |
| 24 | Two sales with the same `delivery_code`: which keeps the account? | Lowest `scust_id`; the other is rejected | profile `mysql/09_delivery_codes` |
| 25 | A delivery code that belongs to another contact's account: reject or link? | Rejected (`account_contact_mismatch`) | |
| 26 | Which field is the account's serial number? | `scust_scanned_out_serial`, else `scust_stove_serials` | |
| 27 | A serial that's too long (over 20) or already used by another account? | Left empty; the Buisy value is kept in metadata; the lowest `scust_id` keeps a shared serial | |
| 28 | For accounts the migration creates: `account_type`, `status` and `delivery_date`? | Default `PAYGO`; status and delivery date empty | |
| 29 | Should Buisy consent data (`consent_str`, `consent_signature_final*`) fill `accounts.consent_string` / `consent_path`? | Not migrated (no `TODO` marker yet) | |

## When a decision is made

1. Change the SQL at the `TODO(decision)` comment and remove the marker.
2. If the status rule changes (#11, #12), make the same change in [validate/deliverydb/02_check_inventory_status.sql](../validate/deliverydb/02_check_inventory_status.sql). It restates the rule on purpose, so validation tests the transform.
3. Move the item to [decisions-made.md](decisions-made.md).
4. Run `make test-integration`.
