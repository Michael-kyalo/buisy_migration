# Decisions made

What has been settled for the Buisy → Nexus migration. The open questions are in [decisions-open.md](decisions-open.md).

## Scope and targets

| Decision | Detail |
|---|---|
| Two target databases | **deliverydb** receives skus, agent_skus, location_users and inventory_products. **customerappdb** receives contacts and accounts. |
| Separate environments | development, training and live each have their own Buisy source, deliverydb, customerappdb and Keycloak. Connection settings never fall back from one env to another. |
| Staging location | A `buisy_nexus_staging` schema inside each target database. No separate staging database. |
| Schemas | The SQL is written against the actual table definitions in [schema/](../schema/). The one assumption left: `nav_inventory__cookware` has the same columns as `nav_inventory`. |

## How the tool works

| Decision | Detail |
|---|---|
| Copy, then transform in SQL | Go copies Buisy tables unchanged into staging. All reshaping happens in versioned SQL under [transforms/](../transforms/). |
| One consistent snapshot | All Buisy tables are read in one read-only MySQL transaction, so both targets reflect the same moment. Parallelism is on the Postgres write side. |
| Credentials stay in Buisy | `users.user_password`, `token`, `token_date`, `user_six_digit_code` and `firebase` are never read. |
| Rejects never stop a run | Rows that fail a rule go to `buisy_nexus_staging.migration_rejects` with the reason and the original row. |

## Matching and rerun safety

| Decision | Detail |
|---|---|
| No schema changes to Nexus | The target tables get no legacy columns. Rows are matched on keys the tables already have. |
| Natural keys | skus `(company_id, code)`; agent_skus `(agent_email, sku_id)`; location_users `(location_id, email)`; inventory_products `serial_number`; contacts `phone`; accounts `external_id`. |
| Migration marker | Every row the migration inserts gets `created_by = 'buisy-migration'` (`createdby` on location_users). inventory_products, contacts and accounts also store their Buisy key in `metadata`. A rerun updates only rows with the marker, even after staging has been dropped. |
| Nexus rows are protected | A matching SKU, location user or agent SKU created in Nexus is linked to and never modified. A serial number already in Nexus rejects the Buisy item rather than overwriting it. |

## Data rules

| Decision | Detail |
|---|---|
| Keycloak | Users match by `users.user_email` = Keycloak email, case-insensitive. There's no username fallback (Buisy has none). |
| Companies | The Buisy company text matches `companies.name`, else `companies.code`. |
| SKU currency | New SKUs take their company's `currency_code`. |
| Agent SKUs | `users.skus` is a comma-separated list of SKU codes, one `agent_skus` row per SKU, keyed by email. |
| User locations | `users.locations` is a comma-separated list of bins, matched to `locations.bin`. |
| Contact identity | The phone number identifies the contact. Repeat buyers share one contact. |
| Customers already exist | Buisy customers are expected to exist in the customer app. On existing contacts and accounts only empty fields are filled in. Values Nexus already has are never overwritten. |
| Customers not in Nexus | Contacts and accounts are created. |
| Account external id | `accounts.external_id` is `nav_sale_customer.delivery_code`. |
| Finding a sale's account | First the account whose `external_id` is the sale's delivery code; else the contact's only remaining account; else a new account if the contact has none; otherwise the sale is rejected as ambiguous. |
| Buisy dates | Buisy stores dates as text. `YYYY-MM-DD[ HH:MM[:SS]]` and `DD/MM/YYYY[ HH:MM[:SS]]` are parsed. Anything else becomes NULL and is reported. |

## Partial migration (`--since`, `--limit`)

| Decision | Detail |
|---|---|
| Purpose | Migrate a subset of recent data. It doesn't sync changes. |
| Flag | `--since=YYYY-MM-DD` only, with no end date. |
| Filtered tables | Inventory by `inv_entry_date`, customers by `scust_entry_date`. SKUs, users, dimensions and Keycloak users always copy in full. |
| Rows with no date | Excluded, counted, and listed in a report after the copy. |
| Row limit | `--limit=N` copies at most N rows from each inventory table and from customers, lowest primary key first, inside the `--since` window if one is given. Reference tables still copy in full. |
