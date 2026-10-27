# buisy-migration

Migrates data from the legacy Buisy MySQL database (`burnmfgsystems_db`) into two Nexus Postgres databases:

| Target database | Tables it receives |
|---|---|
| **deliverydb** | skus, agent_skus, location_users, inventory_products |
| **customerappdb** | contacts, accounts |

The Go tool copies raw data and runs the other steps in order. All reshaping happens in versioned SQL.

Each target database is handled the same way: its own `buisy_nexus_staging` schema, its own transforms and its own validation. Postgres can't query across databases, so each one receives copies of the Buisy tables it needs.

| Step | What it does |
|---|---|
| **copy** | Streams each Buisy table, unchanged except that credential columns are left out, into `buisy_nexus_staging` of every target database that needs it. It also loads Keycloak users into `buisy_nexus_staging.keycloak_users` in both. |
| **transform** | For each target database, runs `transforms/common/*.sql` plus `transforms/<db>/*.sql` in file-name order, each file in its own transaction. Each file moves data from `buisy_nexus_staging` into the real tables. |
| **validate** | For each target database, runs `validate/common/*.sql` plus `validate/<db>/*.sql` and prints a report. Exits non-zero if any check fails in either database. |

`migrate profile` runs read-only data-quality checks before you migrate.

> **Schemas.** The SQL is written against the actual table definitions in [schema/buisy.sql](schema/buisy.sql), [schema/deliverydb.sql](schema/deliverydb.sql) and [schema/customerappdb.sql](schema/customerappdb.sql). One piece is still assumed:
>
> - `nav_inventory__cookware`, assumed to have the same columns as `nav_inventory`.
>
> Each database has a `00_preflight.sql` ([deliverydb](transforms/deliverydb/00_preflight.sql), [customerappdb](transforms/customerappdb/00_preflight.sql)) listing every column its transforms use. If the real database differs, it aborts the transform and names the missing columns.

## Layout

```
cmd/migrate/            CLI
internal/config         env vars + AWS Secrets Manager, live guard
internal/source         read-only MySQL access (consistent snapshot)
internal/copy           schema discovery, type mapping, keyset streaming, COPY into buisy_nexus_staging
internal/transform      ordered SQL runner, dry run
internal/validate       validation report
internal/profile        profiling checks
internal/keycloak       Keycloak users from its database or its admin API
internal/app            wires the steps together; Specs says which Buisy tables each database receives
sql/bootstrap.sql       buisy_nexus_staging schema + control tables (run in every target database)
transforms/common/      runs in every target database: 03_agents (Buisy user → Keycloak id)
transforms/deliverydb/  00_preflight, 01_locations_check, 02_skus, 03_users_agent_skus_location_users, 05_inventory_products
transforms/customerappdb/  00_preflight, 04_contacts_accounts
validate/common/        04_report_rejects
validate/<db>/          *_check_* files must return a boolean "ok" column; others are reports
profile/mysql/*.sql     checks run on Buisy
profile/extract/*.sql   key sets copied into session TEMP tables for cross-checks
profile/pg/*.sql        checks run on deliverydb against those temp tables
schema/                 actual table definitions (also the integration-test fixtures)
```

Files from `common/` and `<db>/` run as one sequence sorted by file name. The same file name in both folders is an error, so the run order is never ambiguous.

The SQL is embedded into the binary at build time. Use `--sql-dir=.` to run the SQL in your working copy instead.

## Setup

Requires Go 1.26. Docker is only needed for the integration test and the image build.

```sh
make build              # or: go build -o bin/migrate ./cmd/migrate
make test               # unit tests
make test-integration   # MySQL + Postgres containers: copy → transform → validate, reruns, dry run, profile
make docker
```

There's no `make` on Windows; run the `go` commands from the [Makefile](Makefile) directly.

To browse the test data afterwards, run the integration test with `KEEP_CONTAINERS=1`:

```sh
KEEP_CONTAINERS=1 go test -tags integration -count=1 -v ./test/integration/
```

The databases stay up, plus an [Adminer](https://www.adminer.org/) web UI. At the end, the test prints:

- login links for Buisy MySQL (password `buisy`), deliverydb and customerappdb (password `nexus`; they open on the `buisy_nexus_staging` schema, switch to `public` for the migrated tables);
- the `docker exec` and pgAdmin/DBeaver connection details;
- the `docker rm -f …` command to remove everything.

If the integration test fails with `reaper: ... could not start container` (seen on Docker Desktop for Windows), set `TESTCONTAINERS_RYUK_DISABLED=true`. The test removes its own containers when it finishes.

## Usage

```sh
migrate --env=development|training|live \
        [--steps=copy,transform,validate] \
        [--tables=nav_sku,users,nav_sale_customer,nav_inventory,nav_inventory__cookware,nav_dimension,keycloak] \
        [--since=YYYY-MM-DD] [--limit=N] [--dry-run] [--confirm-live] \
        [--batch-size=10000] [--workers=2] [--log-format=json|text] [--sql-dir=DIR]

migrate profile --env=... [--confirm-live]
```

- Both target databases (host and name) are printed before anything else happens.
- `--env=live` is refused without `--confirm-live`.
- After connecting, the tool checks that `current_database()` matches the configured name, for each target.
- A Postgres advisory lock in each target stops two runs from migrating the same database at once.
- All targets are connected, checked, locked and bootstrapped before any step runs. Transforms then run for deliverydb, then customerappdb.
- `--dry-run` runs each database's transforms in one transaction, then rolls it back. One transaction is needed so later files see earlier files' writes. Before rolling back, it prints the rows each table would insert and update, and the would-be rejects grouped by reason. In a dry run, copy and validate are skipped.
- `--tables` limits which tables the copy step refreshes, in every database that receives them. `keycloak` selects the Keycloak user load.
- `--since` and `--limit` migrate a subset, by date and/or row count. See [Partial migration](#partial-migration---since---limit).
- Exit codes: `0` success, `1` failure (including failed validation checks), `2` usage error or live not confirmed.

## Partial migration (`--since`, `--limit`)

`--since=YYYY-MM-DD` migrates only recent data. For example, `migrate --env=development --since=2024-01-01` loads inventory entered and customers created on or after 1 January 2024.

| Buisy table | Filtered on | With `--since` |
|---|---|---|
| nav_inventory, nav_inventory__cookware | `inv_entry_date` | Only rows on or after the date |
| nav_sale_customer | `scust_entry_date` | Only rows on or after the date |
| nav_sku, users, nav_dimension, Keycloak users | none | Always copied in full; filtered inventory and contacts still reference them |

`--limit=N` copies at most N rows from each of the same tables, which is useful for testing with a known amount of data. For example, `migrate --env=development --limit=1000` copies 1,000 rows of `nav_inventory`, 1,000 of `nav_inventory__cookware` and 1,000 customers.
- Rows are taken in primary-key order (lowest `inv_id` / `scust_id` first), so repeating a run with the same limit migrates the same rows.
- Combined with `--since`, it's the first N rows inside the date window.
- SKUs, users, dimensions and Keycloak users still copy in full, so the sample resolves its references exactly as a full run would.
- The partial-copy report adds an `over_limit` column: rows that matched but were left out by the limit.

- **Dates are text in Buisy.** Both filter columns are `varchar`, so MySQL parses them with the same formats as the transforms (see [Buisy dates](#buisy-dates)).
- **Rows with no date** (empty, unparseable, `0000-00-00`) are excluded and counted. They're never dropped silently: after the copy, a report lists each filtered table's source rows, copied rows, rows before the date and rows skipped for having no date. The no-date count is also logged as a warning.
- **The filter is applied while reading from Buisy.** Transforms and upserts are unchanged, so a partial run is as safe to repeat as a full one, and widening the window later just adds the extra rows.
- **Validation covers the rows staged in this run.** `01_check_counts` checks that every staged row is either migrated or rejected. Its `other_target_rows` column reports migrated rows outside the window, for information only.
- **Run history.** `migration_runs.since` and `migration_runs.row_limit` record the filters, so partial runs are visible later.
- **Copy step required.** `--since` and `--limit` need the copy step and can't be combined with `--dry-run`, which skips the copy. To preview a subset, run `--since=… --limit=… --steps=copy`, then `--dry-run --steps=transform`.
- **What it doesn't do.** `--since` is for migrating a subset, not for keeping Buisy and Nexus in sync. It filters on the date a row was created, so changes made later to older rows (for example, an item sold after the date) are not picked up.
- **Changing the filter.** The date columns are defined in `DateColumns` in [internal/copy/copy.go](internal/copy/copy.go).

## Environment variables

Connection and credential variables always carry the env suffix: `_DEVELOPMENT`, `_TRAINING` or `_LIVE`. They never fall back to another env's value or to an unsuffixed name. Each env has its own Buisy source, deliverydb, customerappdb and Keycloak, and both target databases must be configured for every run.

| Variable | Purpose |
|---|---|
| `MIGRATE_DELIVERYDB_URL_<ENV>` | deliverydb Postgres URL, e.g. `postgres://user:pw@host:5432/development?sslmode=require` |
| `MIGRATE_DELIVERYDB_SECRET_ARN_<ENV>` | Alternative: RDS-style JSON secret (`host`, `port`, `username`, `password`, `dbname`) |
| `MIGRATE_DELIVERYDB_DATABASE_<ENV>` | Optional: overrides the secret's `dbname` |
| `MIGRATE_CUSTOMERAPPDB_URL_<ENV>` | customerappdb Postgres URL |
| `MIGRATE_CUSTOMERAPPDB_SECRET_ARN_<ENV>` | Alternative: RDS-style JSON secret |
| `MIGRATE_CUSTOMERAPPDB_DATABASE_<ENV>` | Optional: overrides the secret's `dbname`. Lets both databases share one server secret, e.g. `customerappdb` |
| `MIGRATE_PG_SSLMODE` | `sslmode` used when a URL is built from a secret (default `require`) |
| `BUISY_MYSQL_DSN_<ENV>` | Buisy MySQL DSN, e.g. `ro_user:pw@tcp(host:3306)/burnmfgsystems_db` |
| `BUISY_MYSQL_SECRET_ARN_<ENV>` | Alternative: RDS-style JSON secret |
| `BUISY_MYSQL_TLS` | Optional go-sql-driver `tls` value when the DSN is built from a secret |
| `KEYCLOAK_MODE` | `db` or `api` (may also be set per env) |
| `KEYCLOAK_REALM` | Realm whose users are mapped (may also be set per env) |
| `KEYCLOAK_DB_URL_<ENV>` / `KEYCLOAK_DB_SECRET_ARN_<ENV>` | db mode: Keycloak Postgres |
| `KEYCLOAK_BASE_URL_<ENV>` | api mode: e.g. `https://auth.example.com` |
| `KEYCLOAK_CLIENT_ID` | api mode: confidential client with the realm-management `view-users` role |
| `KEYCLOAK_CLIENT_SECRET_<ENV>` / `KEYCLOAK_CLIENT_SECRET_SECRET_ARN_<ENV>` | api mode: client secret |
| `KEYCLOAK_TOKEN_REALM` | api mode: realm the client authenticates in (default: `KEYCLOAK_REALM`) |

The DSN is always forced to `parseTime=true`, `charset=utf8mb4` and `loc=UTC`.

Secrets are read with the default AWS credential chain; on ECS, that is the task role.

Never hardcode credentials. The MySQL user should be read-only. On top of that, the tool opens every MySQL session as `READ ONLY` and has no code path that writes to MySQL.

## How it works

### Copy

- **One snapshot.** All tables are read through one MySQL connection opened with `START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY`, so every table, in both target databases, reflects the same moment.
- **Routing.** Each table is read once, and every batch goes to each database that needs it (`Specs` in [internal/app/app.go](internal/app/app.go)):

  | Buisy table | deliverydb | customerappdb |
  |---|---|---|
  | nav_sku, nav_inventory, nav_inventory__cookware, nav_dimension | ✓ | |
  | users, Keycloak users | ✓ | ✓ |
  | nav_sale_customer | | ✓ |

- **Where the parallelism is.** Reading is sequential. The parallelism is on the Postgres side: `--workers` goroutines load batches with `CopyFrom` while the reader fetches the next batch.
- **Column discovery.** Columns are discovered from `information_schema` at runtime.
- **Credentials stay in Buisy.** `users.user_password`, `token`, `token_date`, `user_six_digit_code` and `firebase` are never read (`ExcludedColumns` in [internal/copy/copy.go](internal/copy/copy.go)).
- **Staging tables.** Each staging table is created `UNLOGGED` with no indexes. On each copy:
  - If the table's shape still matches the source, it is truncated.
  - If not, it is dropped and recreated.

  Only `buisy_nexus_staging` is ever truncated.
- **Paging.** Rows are read in keyset batches: `WHERE (pk) > (?) ORDER BY pk LIMIT n`, never OFFSET. A table with no primary key or NOT NULL unique key is streamed with a single query instead, which still never holds the whole table in memory.
- **Type handling:**

  | Source value | Staging value |
  |---|---|
  | Zero dates (`0000-00-00`, and partial zeros like `2020-00-15`) and Go's zero time | `NULL` |
  | `DECIMAL` | Read as a string, stored as an exact `numeric` (never float64) |
  | `TINYINT(1)` | Copied as an integer; the conversion happens in SQL |
  | NUL bytes and invalid UTF-8 in text | NULs stripped, invalid bytes replaced; the count of fixed values is logged |

### Transform

The target tables have no legacy columns, so rows are matched on the tables' own natural keys:

| Target | Natural key | Buisy source | Database |
|---|---|---|---|
| skus | `(company_id, code)` | nav_sku (`sku_sku`, `company`) | deliverydb |
| agent_skus | `(agent_email, sku_id)` | one row per SKU in the comma-separated `users.skus` | deliverydb |
| location_users | `(location_id, email)` | one row per bin in the comma-separated `users.locations`, matched to `locations.bin` | deliverydb |
| inventory_products | `serial_number` (unique across all SKUs) | nav_inventory + nav_inventory__cookware | deliverydb |
| contacts | `phone`, the contact's identity; customers without a phone get their own contact | nav_sale_customer (repeat buyers share a contact) | customerappdb |
| accounts | `external_id` = the sale's `delivery_code`; else the contact's only account | nav_sale_customer, one per sale | customerappdb |

Keys are compared trimmed and case-insensitive; phones with spaces, dashes and brackets removed on both sides.

The Buisy company text (`nav_sku.company`, `inv_company`) is matched to `companies.name`, else `companies.code`. The company also gives new SKUs their currency (`companies.currency_code`).

**Customers already exist in the customer app.** Each Buisy sale finds its contact by phone. It finds its account in this order:
1. The account whose `external_id` is the sale's `delivery_code`. If that account belongs to a different contact, the sale is rejected as `account_contact_mismatch`.
2. Otherwise, the contact's only remaining account. That requires exactly one account left on the contact, and exactly one Buisy sale for that phone still unmatched.
3. Otherwise, if the contact has no remaining accounts, a new account is created with `external_id` = `delivery_code`. Without a delivery code it is rejected as `delivery_code_missing`.
4. Otherwise the sale is rejected as `account_ambiguous`.

On existing contacts and accounts only empty fields are filled in. Values Nexus already has are never overwritten. A customer with no contact in Nexus gets one created.

**Every transform follows the same pattern:**
1. Build a `buisy_nexus_staging.w_*` work table: one row per source row, with its natural key, `reject_reason`, and the target row it matches.
2. Decide an action for each row:

   | Action | When | What happens |
   |---|---|---|
   | **insert** | No target row has the key | Row inserted |
   | **update** | The matching row has `created_by = 'buisy-migration'` | The migration created it, so it is updated |
   | **adopt** | The matching row was created in Nexus (skus, location_users, agent_skus) | Buisy data links to it; it is never modified |
   | **fill** | The matching row was created in Nexus (contacts, accounts) | Buisy data links to it; only its empty fields are filled in |
   | **reject** | A rule failed, or the matching row is a Nexus-native serial | Written to `migration_rejects`; never overwritten. Rejects never stop a run |

3. Record old → new ids in `buisy_nexus_staging.id_map`.

**Rerun safety.** Every inserted row gets `created_by = 'buisy-migration'` (`createdby` on location_users). inventory_products, contacts and accounts also record their Buisy key in `metadata`. That's how a rerun recognises its own rows, even after `buisy_nexus_staging` is dropped, instead of inserting duplicates.

The transform files read `current_setting('migrate.run_id')` to tag their rejects. One run uses the same run id in both databases.

Both databases need the Buisy user → Keycloak mapping: deliverydb for `location_users.external_id`, customerappdb for `contacts.salesperson_id` and `accounts.sales_agent_id`. It lives once, in [transforms/common/03_agents.sql](transforms/common/03_agents.sql), and runs in both, so the two can't disagree:
- Users match on `users.user_email` = the Keycloak email, case-insensitive. Buisy has no username to fall back to.
- Unmatched and ambiguous users are rejected, so they show up in both databases' reports.
- agent_skus is keyed by email and doesn't need Keycloak.

**Unpublished rows.** `publish = 0` rows are treated as deleted in Buisy and rejected as `unpublished_in_buisy`.

#### Buisy dates

Buisy stores dates as `varchar`. `buisy_nexus_staging.parse_buisy_ts` (in [sql/bootstrap.sql](sql/bootstrap.sql)) accepts:
- `YYYY-MM-DD[ HH:MM[:SS]]`
- day-first `DD/MM/YYYY[ HH:MM[:SS]]`

Anything else becomes NULL. Times are read as UTC.

To check coverage:
- `migrate profile` shows, for every date column, how many values parse and an example of one that doesn't.
- The validation report lists unparsed dates after each run.

`buisy_nexus_staging.migration_runs` in each database records each step per table or file:

- row counts and status;
- the `--since` date for partial runs (NULL for full runs);
- one summary row per step, which has `detail` NULL.

### Validate

The validation report has one section per database and shows:

- every staged source row is migrated (inserted, updated or adopted, via `id_map`) or rejected, i.e. source rows = target rows + rejects, per target table and source (both databases);
- inventory status by source rule vs target (deliverydb);
- unresolved references on migrated rows, each count should be 0 (both databases);
- rejects grouped by reason (both databases);
- 20 random serials, Buisy values beside Nexus values (deliverydb);
- Buisy dates that couldn't be parsed (deliverydb);
- how sales landed: contacts inserted, updated or linked; account serials left empty because they were too long or shared (customerappdb).

Each database validates its latest successful transform run for the env.

## Run order: development → training → live

For each env, in order:

1. **Profile.** `migrate profile --env=development`. Review:
   - date formats;
   - duplicate and badly sized serials;
   - shared and missing phones;
   - the format of the users' SKU and location lists;
   - unmatched locations, users and salespeople.
2. **Dry run.** `migrate --env=development --dry-run --steps=copy,transform`. The copy is skipped in a dry run, so run a real copy first if buisy_nexus_staging is empty: `--steps=copy`.
3. **Real run.** `migrate --env=development`. Check that the report passes and the rejects are the ones you expect.
4. **Rerun once.** `migrate --env=development` again. Target counts must not change.
5. **Repeat for training**, then sign off on the rejects.
6. **Live.** `migrate --env=live --confirm-live`, after `migrate profile --env=live --confirm-live` and `migrate --env=live --confirm-live --dry-run`.

To refresh only some tables, use `--steps=copy --tables=nav_inventory,nav_inventory__cookware`, then `--steps=transform,validate`.

## Clean retry

Staging is disposable. Run this in **both** deliverydb and customerappdb:

```sql
DROP SCHEMA buisy_nexus_staging CASCADE;
```

This drops the raw copies, work tables, `id_map`, rejects and run history. The next run bootstraps buisy_nexus_staging again. Migrated rows carry `created_by = 'buisy-migration'` and, where the table has `metadata`, their Buisy key. The next transform therefore finds the rows it loaded before, updates them instead of duplicating them, and rebuilds `id_map`. The integration test checks this. The migrated data itself is never deleted by the tool.

## Decisions and mappings

- [docs/mappings.md](docs/mappings.md): field by field, what each Buisy table becomes in Nexus, how existing rows are matched, and every reject reason.

- [docs/decisions-made.md](docs/decisions-made.md): what has been settled, from targets and matching keys to customer rules.
- [docs/decisions-open.md](docs/decisions-open.md): the open questions. Each is marked `TODO(decision)` in the SQL and listed with its current behaviour and where `migrate profile` shows the evidence. `make todo` lists them.
