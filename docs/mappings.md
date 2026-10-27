# Buisy → Nexus mappings

This document describes, field by field, what the transforms write to Nexus, where each value comes from in Buisy, and when a row is rejected instead. The authoritative source is the SQL in [transforms/](../transforms/). Open questions about these rules are in [decisions-open.md](decisions-open.md) (#n below).

## Overview

| Buisy source | Nexus target | Database | Grain |
|---|---|---|---|
| `nav_sku` | `skus` | deliverydb | one SKU per Buisy SKU |
| `users.skus` | `agent_skus` | deliverydb | one row per user × SKU code |
| `users.locations` | `location_users` | deliverydb | one row per user × bin |
| `nav_inventory`, `nav_inventory__cookware` | `inventory_products` | deliverydb | one product per serial |
| `nav_sale_customer` | `contacts` | customerappdb | one contact per phone number |
| `nav_sale_customer` | `accounts` | customerappdb | one account per sale |
| `users` + Keycloak | user → Keycloak id map | both | one per Buisy user |
| `nav_dimension` | none | deliverydb | copied to staging only; dimensions are already migrated |

### Rules that apply everywhere

**Rows from Buisy**
- **Unpublished rows** (`publish = 0`) are treated as deleted and rejected as `unpublished_in_buisy` (#1).
- **Text:** values are trimmed, and empty strings become NULL.
- **Text matching** is case-insensitive.
- **Dates** are text in Buisy. They're parsed as `YYYY-MM-DD[ HH:MM[:SS]]` or day-first `DD/MM/YYYY[ HH:MM[:SS]]`, read as UTC. Anything else becomes NULL (#2).

**Rows in Nexus**
- **Migration marker:** every row the migration inserts gets `created_by = 'buisy-migration'` (`createdby` on location_users) and `created_at` / `updated_at`.
- **How existing rows are treated:**

  | Row found in Nexus | What happens |
  |---|---|
  | Created by the migration | Updated with Buisy values |
  | SKU, location user or agent SKU created in Nexus | Linked to, never changed |
  | Contact or account created in Nexus | Only its empty fields are filled in |
  | Serial number already in Nexus | The Buisy item is rejected |

**Bookkeeping**
- Every Buisy row's Nexus id is recorded in `buisy_nexus_staging.id_map`.
- Every rejected row is recorded in `buisy_nexus_staging.migration_rejects`, with its reason and the original row.

---

## Lookups shared by several targets

### Buisy user → Keycloak id
[transforms/common/03_agents.sql](../transforms/common/03_agents.sql), run in both databases.

| Step | Rule |
|---|---|
| Match | `users.user_email` = Keycloak user email, case-insensitive. No username fallback. |
| Result | The Keycloak user id. It's used for `location_users.external_id`, `contacts.salesperson_id` and `accounts.sales_agent_id`. |
| Rejected (`users`) | `email_missing`, `keycloak_unmatched`, `keycloak_ambiguous_email` (two Keycloak users with that email) |

A rejected user isn't removed from the migration. Anything that needs their Keycloak id is rejected in turn.

### Company
[transforms/deliverydb/01_locations_check.sql](../transforms/deliverydb/01_locations_check.sql)

Buisy company text (`nav_sku.company`, `nav_inventory.inv_company`) → `companies.id`, matched on `companies.name`, else `companies.code`. The company also supplies `currency_code` for new SKUs.

### Location
Matched against `locations.bin` and `locations.code`. A key matching more than one location is ambiguous and never used.

| Buisy value | Matched to |
|---|---|
| each entry in `users.locations` | `locations.bin` |
| `nav_inventory.inv_bin` / `inv_location` | the bin if unique, else bin + `locations.code`, else `locations.code` alone (#10) |

### SKU reference
`users.skus` entries and `nav_inventory.inv_sku` → `skus.id`:
- Matched on the SKU code (`nav_sku.sku_sku`).
- A numeric value that isn't a code is tried as the Buisy `sku_id`.
- A code used by SKUs in two companies is ambiguous (#8).

### Customer's sales agent
`nav_sale_customer` → Buisy user → Keycloak id. Tried in order (#19):
1. `scust_sales_agent_buisy_code` = `users.agent_code_buisy`
2. `scust_sales_agent` = `users.id`
3. `scust_sales_agent` = `users.user_email`

---

## skus ← nav_sku
[transforms/deliverydb/02_skus.sql](../transforms/deliverydb/02_skus.sql)

**Natural key:** `(company_id, code)`. A SKU already in Nexus with the same key is linked to and not changed (#7).

| Nexus column | Buisy source | Rule |
|---|---|---|
| `code` | `sku_sku` | trimmed |
| `name` | `sku_itemname` | |
| `generation` | `sku_productgeneration` | |
| `classification` | `sku_productionclassification` | |
| `color` | `sku_color` | |
| `base_unit` | `sku_baseunit` | |
| `category_type` | `sku_category` | |
| `serial_number_length` | `sku_serialnumberlength` | leading number of the text (`'10'` → 10) |
| `stock` | `sku_stock` | leading number; non-numeric → NULL |
| `has_warranty` | `sku_has_warranty` | `1` → true |
| `warranty_period` | `sku_warranty_period` | leading number (`'24 months'` → 24) (#3) |
| `status` | `sku_status` | `1` → `ACTIVE`, else `INACTIVE` (#4) |
| `company_id` | `company` | company lookup |
| `currency` | `companies.currency_code` | on insert only, so a value corrected in Nexus survives reruns |
| `created_at` | `sku_entry_date` | parsed; now() if missing |

**Not set (Nexus defaults apply):** `price`, `is_paygo`, `tracking_mode`, `is_bundled`, `gs1_code`.

**On rerun:** SKUs created by the migration get all columns above updated, except `company_id`, `currency` and `created_at`.

**Rejected (`nav_sku`):**
- `unpublished_in_buisy`
- `sku_code_missing`
- `company_unmatched`: company text set but not found (#5)
- `sku_code_duplicate`: same code twice in a company; the lowest `sku_id` wins (#6)

## agent_skus ← users.skus
[transforms/deliverydb/03_users_agent_skus_location_users.sql](../transforms/deliverydb/03_users_agent_skus_location_users.sql)

`users.skus` (for example `'JIKO-1, ECOA-2'`) is split on commas into one row per user × SKU code.

**Natural key:** `(agent_email, sku_id)`. A pair that already exists in Nexus is simply linked; there's nothing to update.

| Nexus column | Buisy source | Rule |
|---|---|---|
| `agent_email` | `users.user_email` | lower-cased |
| `sku_id` | SKU code from the list | SKU reference lookup |

Keycloak isn't needed: the table is keyed by email. Two Buisy users with the same email collapse into one row per SKU.

**Rejected (`users.agent_skus`):**
- `unpublished_in_buisy` (user)
- `agent_email_missing`
- `sku_unresolved`
- `sku_ambiguous`

## location_users ← users.locations
[same file](../transforms/deliverydb/03_users_agent_skus_location_users.sql)

`users.locations` (for example `'BIN-K, BIN-B'`) is split on commas into one row per user × bin.

**Natural key:** `(location_id, email)`, the table's unique constraint.

| Nexus column | Buisy source | Rule |
|---|---|---|
| `location_id` | bin from the list | `locations.bin`, must identify one location |
| `email` | `users.user_email` | lower-cased |
| `external_id` | the user's Keycloak id | user → Keycloak lookup |

**Existing rows:**
- Created by the migration: `external_id` is updated.
- Created in Nexus: linked to, not changed.

**Rejected (`users.location_users`):**
- `unpublished_in_buisy`
- `agent_email_missing`
- `location_unmatched`: no location has that bin
- `location_ambiguous`: several locations share the bin
- `agent_unresolved`: no Keycloak user

## inventory_products ← nav_inventory + nav_inventory__cookware
[transforms/deliverydb/05_inventory_products.sql](../transforms/deliverydb/05_inventory_products.sql)

Both tables load into one target and are told apart by `metadata.legacy_source`. The cookware table is assumed to have the same columns (#9).

**Natural key:** `serial_number`, unique across all of inventory_products.
- A serial already in Nexus from elsewhere: the Buisy row is rejected, never overwritten.
- A serial created by the migration for the same Buisy row: updated.

| Nexus column | Buisy source | Rule |
|---|---|---|
| `serial_number` | `inv_serial` | trimmed |
| `sku_id` | `inv_sku` | SKU reference lookup |
| `location_id` | `inv_bin`, `inv_location` | location rule (#10); NULL when both are empty (#17) |
| `company_id` | `inv_company` | company lookup |
| `status` | `is_sold`, `sales_order_no`, `inv_sub_status` | see the status rule below |
| `scan_in_time` | `inv_entry_date` | parsed, UTC (#13) |
| `manufacture_date` | `inv_manufacture_date` | parsed |
| `document_no` | `inv_doc_no` | `'BUISY-MIGRATION'` when empty; Nexus requires it (#14) |
| `metadata` | the Buisy row | `legacy_source`, `legacy_id`, plus a `buisy` object (below) |

**Status rule** (#11, #12):

| Condition, first match wins | Status |
|---|---|
| `is_sold = 1` or `sales_order_no` set | `SOLD` |
| `inv_sub_status` is "in transit" (`in transit` / `in_transit` / `intransit`, any case) | `IN_TRANSIT` |
| anything else | `SCANNED_IN` |

`SCANNED_OUT` is never produced. The numeric `inv_status` is kept in metadata.

**`metadata.buisy`** keeps Buisy fields Nexus has no column for:
- name, sku, generation, classification, color;
- location, bin, base_unit, country, company;
- main and sub department, doc_no;
- entry and manufacture dates as written in Buisy;
- inv_status, sub_status, is_sold, sales_order_no, product_name;
- cost, profit and product centres, carbon project, R&D project;
- directors, category.

**Not set:** `scan_out_time` and the other `scan_out_*` columns, `serial_type`, `scan_in_role`, `scan_in_by`, `agent`, `dimension_country_code`, `destination_role`, `sah_status`, `product_condition`. `is_return`, `is_quarantined` and `lost` keep their defaults (false).

**On rerun:** products created by the migration are updated in full, and their metadata is merged (#18).

**Rejected (`nav_inventory` / `nav_inventory__cookware`):**
- `unpublished_in_buisy`
- `serial_missing`
- `sku_unresolved`
- `sku_ambiguous`
- `location_unmatched`
- `company_unmatched`
- `duplicate_serial`: the same serial in Buisy twice; nav_inventory wins over cookware, then the latest entry date, then the lowest id (#16)
- `serial_exists_in_nexus`

## contacts ← nav_sale_customer
[transforms/customerappdb/04_contacts_accounts.sql](../transforms/customerappdb/04_contacts_accounts.sql)

Each Buisy customer row is a sale. **The phone number identifies the contact**, so repeat buyers share one contact. Customers without a phone get one contact per sale.

**Finding the contact:**
1. A contact the migration created for this phone (found through `metadata.buisy_contact_key`).
2. Otherwise, a contact in Nexus with the same phone. Spaces, dashes and brackets are ignored on both sides (#20).
3. Otherwise, a new contact.

When several sales share a phone, the latest sale supplies the details (#22).

| Nexus column | Buisy source | Rule |
|---|---|---|
| `phone` | `scust_customer_main_tel` | spaces, dashes and brackets removed |
| `first_name` | `scust_customer_name` | first word (#21) |
| `last_name` | `scust_customer_name` | the rest; empty if one word |
| `alt_msisdn` | `scust_customer_alt_tel` | cleaned like the phone |
| `identification_number` | `scust_id_number` | |
| `gender` | `gender` | first 6 characters (Nexus limit) |
| `region` | `scust_county_name` | |
| `sub_region` | `scust_sub_county` | |
| `address` | `scust_home_address` | |
| `location` | `scust_village` | |
| `countryname` | `scust_country` | |
| `salesperson_id` | the sale's agent | customer's sales agent → Keycloak id (#23: NULL when no agent) |
| `created_at` | `scust_entry_date` | parsed; now() if missing |
| `metadata` | | `legacy_source`, `buisy_contact_key` (new contacts only) |

**Existing contacts:**
- Created in Nexus: only empty `alt_msisdn`, `identification_number`, `gender`, `region`, `sub_region`, `address`, `location`, `countryname` and `salesperson_id` are filled in. Names and phone are never changed.
- Created by the migration: updated with Buisy values.

**Not set:** `country_id`, `status`, `general_status` (default), `occupation`, the GPS and home location fields, `labels`, and the other customer-app fields.

**Rejected (`nav_sale_customer`):** these block both the contact and the account for that sale.
- `unpublished_in_buisy`
- `customer_name_missing`
- `phone_too_long`: over 20 characters
- `salesperson_unresolved`: an agent is set but doesn't resolve to a Keycloak user

## accounts ← nav_sale_customer
[same file](../transforms/customerappdb/04_contacts_accounts.sql)

One account per sale. Customers are expected to have accounts in Nexus already.

**Finding the account:**

| Step | Rule | Outcome |
|---|---|---|
| 1 | An account the migration created for this sale (`metadata.legacy_id`) | updated |
| 2 | The account whose `external_id` = the sale's `delivery_code` | its empty fields are filled; rejected as `account_contact_mismatch` if it belongs to another contact (#25) |
| 3 | The contact's only remaining account, when exactly one Buisy sale for that contact is still unmatched | its empty fields are filled |
| 4 | The contact has no remaining accounts | a new account is created |
| – | Otherwise | rejected as `account_ambiguous` |

| Nexus column | Buisy source | Rule |
|---|---|---|
| `contact_id` | | the sale's contact |
| `external_id` | `delivery_code` | new accounts only; required and unique |
| `serial_number` | `scust_scanned_out_serial`, else `scust_stove_serials` | left empty if over 20 characters or used by another account (#26, #27) |
| `sales_order_no` | `sales_order_no` | first 55 characters |
| `sales_model` | `scust_sales_model` | `LEGACY` if empty |
| `sales_agent_id` | the sale's agent | customer's sales agent → Keycloak id |
| `created_at` | `scust_entry_date` | parsed; now() if missing |
| `metadata` | the Buisy row | `legacy_source`, `legacy_id`, plus a `buisy` object (below) |

**`metadata.buisy`** keeps:
- ward, county_id, carbon_consent, distribution_channel;
- the sales agent's id, name and Buisy code;
- stove serials, scanned-out serial, external_sale_id, delivery_code;
- entry date, status, verified, sale_complete_status, kc_id, pid.

**Existing accounts:**
- Created in Nexus: only empty `serial_number`, `sales_order_no` and `sales_agent_id` are filled in. Buisy metadata is added without replacing any key Nexus already has.
- Created by the migration: updated in full.

**Not set** on new accounts (#28): `account_type` (default `PAYGO`), `status`, `delivery_date`, `product_id`, `payplan`, `warranty_end_date` (default), `delivery_method`, and the consent and verification fields.

**Rejected (`nav_sale_customer.accounts`):** these block only the account; the contact is still migrated.
- `delivery_code_missing`: a new account is needed but the sale has no delivery code
- `external_id_duplicate`: two sales share a delivery code; the lowest `scust_id` keeps it (#24)
- `account_contact_mismatch`
- `account_ambiguous`

---

## Not migrated

| Buisy data | Why |
|---|---|
| `nav_dimension` | Dimensions are already migrated; the table is copied to staging only. |
| `users` columns other than id, email, agent code, skus, locations, publish | No Nexus target. Credentials (`user_password`, `token`, `token_date`, `user_six_digit_code`, `firebase`) aren't even copied to staging. |
| `nav_sale_customer` consent fields (`consent_str`, `consent_signature_final*`, `customer_with_stove_final`) | Not mapped. `accounts.consent_string` / `consent_path` exist in Nexus; worth deciding whether they should be filled. |
| `nav_sale_customer` call-centre fields (`cx_*`), feedback, payment verification, NAV payloads | Not mapped. |
| `nav_inventory` fields without a Nexus column | Kept in `inventory_products.metadata.buisy`, not in columns. |
