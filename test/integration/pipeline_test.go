//go:build integration

// Package integration runs copy → transform → validate against throwaway MySQL
// and Postgres containers built from the real schemas in schema/, seeded from
// testdata/. Every seed row's expected outcome is noted in the seed files.
//
//	go test -tags integration ./test/integration/ -v
package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/burnmfg/buisy-migration/internal/app"
	"github.com/burnmfg/buisy-migration/internal/config"
	copystep "github.com/burnmfg/buisy-migration/internal/copy"
	"github.com/burnmfg/buisy-migration/internal/keycloak"
	"github.com/burnmfg/buisy-migration/internal/runlog"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var keycloakUsers = keycloak.Static{
	{ID: "kc-alice", Username: "alice.a", Email: "alice@burn.com", Enabled: true},
	{ID: "kc-bob", Username: "bob", Email: "bob@burn.com", Enabled: true},
	{ID: "kc-dave-1", Username: "dave1", Email: "dave@burn.com", Enabled: true},
	{ID: "kc-dave-2", Username: "dave2", Email: "DAVE@burn.com", Enabled: true},
	{ID: "kc-erin", Username: "erin", Email: "erin@burn.com", Enabled: true},
}

// Rejects common/03_agents.sql records, in both databases.
var userRejects = map[string]int64{
	"users/keycloak_unmatched":       2, // carol, frank
	"users/keycloak_ambiguous_email": 1, // dave
	"users/email_missing":            1, // eve
}

func TestPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	root := filepath.Join("..", "..")

	// KEEP_CONTAINERS=1 leaves the databases (and an Adminer web UI) running after
	// the test so the seeded and migrated data can be inspected.
	keep := os.Getenv("KEEP_CONTAINERS") != ""
	cleanup := func(c testcontainers.Container) {
		if !keep {
			testcontainers.CleanupContainer(t, c)
		}
	}
	if keep {
		// The reaper would remove the containers when the test process exits.
		t.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}

	my, err := tcmysql.Run(ctx, "mysql:8.0",
		tcmysql.WithDatabase(config.DefaultSourceDatabase),
		tcmysql.WithUsername("buisy"),
		tcmysql.WithPassword("buisy"),
		tcmysql.WithScripts(
			filepath.Join(root, "schema", "buisy.sql"),
			filepath.Join("testdata", "buisy_seed_data.sql"),
		),
	)
	cleanup(my)
	if err != nil {
		t.Fatal(err)
	}
	pgc, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("development"),
		tcpostgres.WithUsername("nexus"),
		tcpostgres.WithPassword("nexus"),
		tcpostgres.WithOrderedInitScripts(
			filepath.Join("testdata", "deliverydb_prereqs.sql"),
			filepath.Join(root, "schema", "deliverydb.sql"),
			filepath.Join("testdata", "deliverydb_seed_data.sql"),
		),
		tcpostgres.BasicWaitStrategies(),
	)
	cleanup(pgc)
	if err != nil {
		t.Fatal(err)
	}

	myDSN, err := my.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pgURL, err := pgc.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	// customerappdb is a second database; in the test it shares the server.
	custURL := createCustomerAppDB(t, ctx, pgURL, root)

	if keep {
		adminer, mysqlLink, pgLinks, err := startAdminer(ctx, my, pgc, "development", "customerappdb")
		if err != nil {
			t.Fatalf("start adminer: %v", err)
		}
		ids := []string{my.GetContainerID()[:12], pgc.GetContainerID()[:12], adminer.GetContainerID()[:12]}
		t.Cleanup(func() {
			fmt.Printf("\nContainers kept for inspection.\n"+
				"  Web UI (Adminer) - open a link, password is the same as the username:\n"+
				"    Buisy MySQL              %s   (password: buisy)\n"+
				"    Nexus deliverydb         %s   (password: nexus)\n"+
				"    Nexus customerappdb      %s   (password: nexus)\n"+
				"  Terminal:\n"+
				"    docker exec -it %s mysql -ubuisy -pbuisy %s\n"+
				"    docker exec -it %s psql -U nexus -d development     (or -d customerappdb)\n"+
				"  Postgres URLs for pgAdmin/DBeaver:\n    %s\n    %s\n"+
				"  Remove with: docker rm -f %s\n",
				mysqlLink, pgLinks[0], pgLinks[1],
				ids[0], config.DefaultSourceDatabase, ids[1], pgURL, custURL,
				strings.Join(ids, " "))
		})
	}

	source, err := config.NormalizeMySQL(myDSN)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := config.ParsePostgres(pgURL)
	if err != nil {
		t.Fatal(err)
	}
	customer, err := config.ParsePostgres(custURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Env: config.Development, Source: source, Targets: []config.Target{
		{Name: config.DeliveryDB, Postgres: delivery},
		{Name: config.CustomerAppDB, Postgres: customer},
	}}
	kc := func(context.Context) (keycloak.Source, error) { return keycloakUsers, nil }

	var out bytes.Buffer
	opts := app.Options{
		Steps:     app.Steps,
		Tables:    append(slices.Clone(copystep.SourceTables), copystep.KeycloakTable),
		BatchSize: 3, // small, so every table needs several keyset batches
		Workers:   2,
		SQL:       os.DirFS(root),
		Out:       &out,
		Log:       runlog.NewLogger(os.Stderr, "text"),
	}
	run := func(name string, o app.Options) {
		t.Helper()
		out.Reset()
		if err := app.Run(ctx, cfg, kc, o); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out.String())
		}
		t.Logf("%s output:\n%s", name, out.String())
	}

	conn, err := pgx.Connect(ctx, pgURL) // deliverydb
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	cconn, err := pgx.Connect(ctx, custURL) // customerappdb
	if err != nil {
		t.Fatal(err)
	}
	defer cconn.Close(ctx)

	out.Reset()
	if err := app.Profile(ctx, cfg, kc, opts); err != nil {
		t.Fatalf("profile: %v\n%s", err, out.String())
	}
	t.Logf("profile output:\n%s", out.String())
	// Profile must flag the duplicates the transform rejects: SN001 / 'sn001 ' and SN002 across tables.
	if !strings.Contains(out.String(), "03_duplicate_serials: 2 row(s)") {
		t.Error("profile did not report the two duplicate serials")
	}

	run("first run", opts)
	first := snapshot(t, ctx, conn, cconn)
	want := map[string]int64{
		"skus":                     2, // JIKO-1, POT-1 (ECOA-2 adopted)
		"agent_skus":               4,
		"location_users":           1, // Bob's Kisumu entry adopted
		"inventory_products":       8,
		"contacts":                 6, // customers 7, 11 and 13 matched existing Nexus contacts
		"accounts":                 4,
		"native_skus":              2,
		"native_inventory":         1,
		"native_location_users":    1,
		"native_contacts":          2,
		"native_accounts":          4,
		"same_named_public_tables": 2,
	}
	for k, n := range want {
		if first[k] != n {
			t.Errorf("%s: got %d, want %d", k, first[k], n)
		}
	}
	for name, c := range map[string]*pgx.Conn{"deliverydb": conn, "customerappdb": cconn} {
		rows, err := c.Query(ctx, `SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE '%staging%'`)
		if err != nil {
			t.Fatal(err)
		}
		schemas, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(schemas, []string{"buisy_nexus_staging"}) {
			t.Errorf("%s: the tool must only create schema buisy_nexus_staging, found %v", name, schemas)
		}
	}

	// Each database only receives the Buisy tables it needs.
	assertStaged(t, ctx, conn, "deliverydb",
		[]string{"nav_sku", "users", "nav_inventory", "nav_inventory__cookware", "nav_dimension", "keycloak_users"},
		[]string{"nav_sale_customer"})
	assertStaged(t, ctx, cconn, "customerappdb",
		[]string{"users", "nav_sale_customer", "keycloak_users"},
		[]string{"nav_sku", "nav_inventory", "nav_inventory__cookware", "nav_dimension"})

	// Credentials are never copied out of Buisy.
	var leaked int64
	mustScan(t, conn.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'buisy_nexus_staging' AND table_name = 'users'
		  AND column_name IN ('user_password', 'token', 'token_date', 'user_six_digit_code', 'firebase')`), &leaked)
	if leaked != 0 {
		t.Errorf("%d credential columns were copied into staging.users", leaked)
	}

	wantDelivery := merge(userRejects, map[string]int64{
		"nav_sku/sku_code_duplicate":               1,
		"nav_sku/sku_code_missing":                 1,
		"nav_sku/unpublished_in_buisy":             1,
		"nav_sku/company_unmatched":                1,
		"users.agent_skus/sku_unresolved":          1,
		"users.agent_skus/agent_email_missing":     1,
		"users.agent_skus/unpublished_in_buisy":    1,
		"users.location_users/location_unmatched":  1,
		"users.location_users/location_ambiguous":  1,
		"users.location_users/agent_email_missing": 1,
		"users.location_users/agent_unresolved":    1,
		"nav_inventory/duplicate_serial":           1,
		"nav_inventory/sku_unresolved":             1,
		"nav_inventory/location_unmatched":         2,
		"nav_inventory/serial_missing":             1,
		"nav_inventory/serial_exists_in_nexus":     1,
		"nav_inventory/unpublished_in_buisy":       1,
		"nav_inventory/company_unmatched":          1,
		"nav_inventory__cookware/duplicate_serial": 1,
	})
	if got := latestRejects(t, ctx, conn); !maps.Equal(got, wantDelivery) {
		t.Errorf("deliverydb rejects:\n got  %v\n want %v", got, wantDelivery)
	}
	wantCustomer := merge(userRejects, map[string]int64{
		"nav_sale_customer/salesperson_unresolved":            1,
		"nav_sale_customer/customer_name_missing":             1,
		"nav_sale_customer/unpublished_in_buisy":              1,
		"nav_sale_customer.accounts/delivery_code_missing":    1, // customer 2
		"nav_sale_customer.accounts/external_id_duplicate":    1, // customer 8
		"nav_sale_customer.accounts/account_ambiguous":        1, // customer 11
		"nav_sale_customer.accounts/account_contact_mismatch": 1, // customer 12
	})
	if got := latestRejects(t, ctx, cconn); !maps.Equal(got, wantCustomer) {
		t.Errorf("customerappdb rejects:\n got  %v\n want %v", got, wantCustomer)
	}

	checkMappings(t, ctx, conn, cconn)

	run("rerun", opts)
	if second := snapshot(t, ctx, conn, cconn); !maps.Equal(first, second) {
		t.Errorf("rerun changed row counts (duplicates?):\n first  %v\n second %v", first, second)
	}

	dry := opts
	dry.DryRun = true
	run("dry run", dry)
	if third := snapshot(t, ctx, conn, cconn); !maps.Equal(first, third) {
		t.Errorf("dry run changed the target:\n before %v\n after  %v", first, third)
	}

	// After dropping staging, a rerun must still recognise its own rows
	// (created_by / metadata markers) instead of inserting duplicates.
	for _, c := range []*pgx.Conn{conn, cconn} {
		if _, err := c.Exec(ctx, "DROP SCHEMA buisy_nexus_staging CASCADE"); err != nil {
			t.Fatal(err)
		}
	}
	run("rerun after DROP SCHEMA", opts)
	if fourth := snapshot(t, ctx, conn, cconn); !maps.Equal(first, fourth) {
		t.Errorf("rerun after dropping staging changed row counts:\n first %v\n now   %v", first, fourth)
	}

	// Transform-only rerun after an unchanged copy must also be a no-op.
	transformOnly := opts
	transformOnly.Steps = []string{"transform", "validate"}
	run("transform only", transformOnly)
	if fifth := snapshot(t, ctx, conn, cconn); !maps.Equal(first, fifth) {
		t.Errorf("transform-only rerun changed row counts:\n first %v\n now   %v", first, fifth)
	}

	testPartialRun(t, ctx, run, opts, &out, conn, cconn, first)
	testLimitRuns(t, ctx, run, opts, &out, conn, cconn, first)
}

// Rejects that don't depend on --since / --limit: SKUs, users, agent SKUs and
// location users always copy in full.
var fullTableRejects = merge(userRejects, map[string]int64{
	"nav_sku/sku_code_duplicate":               1,
	"nav_sku/sku_code_missing":                 1,
	"nav_sku/unpublished_in_buisy":             1,
	"nav_sku/company_unmatched":                1,
	"users.agent_skus/sku_unresolved":          1,
	"users.agent_skus/agent_email_missing":     1,
	"users.agent_skus/unpublished_in_buisy":    1,
	"users.location_users/location_unmatched":  1,
	"users.location_users/location_ambiguous":  1,
	"users.location_users/agent_email_missing": 1,
	"users.location_users/agent_unresolved":    1,
})

// testLimitRuns copies the first two rows of each inventory table and of the
// customers, alone and inside a --since window. Both must validate and leave
// the target's row counts unchanged.
func testLimitRuns(t *testing.T, ctx context.Context, run func(string, app.Options), opts app.Options, out *bytes.Buffer,
	conn, cconn *pgx.Conn, full map[string]int64) {
	t.Helper()
	for _, tc := range []struct {
		name       string
		since      time.Time
		report     [][]string // table, date_column, source_rows, copied, before_since, no_date_skipped, over_limit
		customers  []string   // staged scust_ids
		inventory  []string   // staged nav_inventory inv_ids
		wantReject map[string]int64
	}{
		{
			name: "--limit=2",
			report: [][]string{
				{"nav_inventory", "inv_entry_date", "14", "2", "0", "0", "12"},
				{"nav_inventory__cookware", "inv_entry_date", "3", "2", "0", "0", "1"},
				{"nav_sale_customer", "scust_entry_date", "13", "2", "0", "0", "11"},
			},
			customers: []string{"1", "2"},
			inventory: []string{"1", "2"},
		},
		{
			// Customer 1 is before the date, so the first two in the window are 2 and 3.
			name:  "--since=2023-03-02 --limit=2",
			since: time.Date(2023, 3, 2, 0, 0, 0, 0, time.UTC),
			report: [][]string{
				{"nav_inventory", "inv_entry_date", "14", "2", "1", "1", "10"},
				{"nav_inventory__cookware", "inv_entry_date", "3", "2", "0", "0", "1"},
				{"nav_sale_customer", "scust_entry_date", "13", "2", "1", "3", "7"},
			},
			customers: []string{"2", "3"},
			inventory: []string{"1", "2"},
		},
	} {
		o := opts
		o.Since = tc.since
		o.Limit = 2
		run(tc.name, o)

		for _, want := range tc.report {
			found := false
			for _, line := range strings.Split(out.String(), "\n") {
				if slices.Equal(strings.Fields(line), want) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: report has no row %v:\n%s", tc.name, want, out.String())
			}
		}

		ids := func(c *pgx.Conn, query string) []string {
			rows, err := c.Query(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			got, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				t.Fatal(err)
			}
			return got
		}
		if got := ids(cconn, "SELECT scust_id::text FROM buisy_nexus_staging.nav_sale_customer ORDER BY scust_id"); !slices.Equal(got, tc.customers) {
			t.Errorf("%s: staged customers %v, want %v", tc.name, got, tc.customers)
		}
		if got := ids(conn, "SELECT inv_id::text FROM buisy_nexus_staging.nav_inventory ORDER BY inv_id"); !slices.Equal(got, tc.inventory) {
			t.Errorf("%s: staged inventory %v, want %v", tc.name, got, tc.inventory)
		}

		// Inventory 1 and 2 load; cookware 2 repeats nav_inventory 2's serial.
		// Customer 2 has no delivery code, so its account can't be created.
		wantDelivery := merge(fullTableRejects, map[string]int64{"nav_inventory__cookware/duplicate_serial": 1})
		if got := latestRejects(t, ctx, conn); !maps.Equal(got, wantDelivery) {
			t.Errorf("%s: deliverydb rejects:\n got  %v\n want %v", tc.name, got, wantDelivery)
		}
		wantCustomer := merge(userRejects, map[string]int64{"nav_sale_customer.accounts/delivery_code_missing": 1})
		if got := latestRejects(t, ctx, cconn); !maps.Equal(got, wantCustomer) {
			t.Errorf("%s: customerappdb rejects:\n got  %v\n want %v", tc.name, got, wantCustomer)
		}

		if after := snapshot(t, ctx, conn, cconn); !maps.Equal(full, after) {
			t.Errorf("%s changed row counts:\n full    %v\n limited %v", tc.name, full, after)
		}
		var limit int64
		mustScan(t, conn.QueryRow(ctx, `
			SELECT row_limit FROM buisy_nexus_staging.migration_runs
			WHERE step = 'copy' AND detail = 'nav_inventory' ORDER BY id DESC LIMIT 1`), &limit)
		if limit != 2 {
			t.Errorf("%s: migration_runs.row_limit = %d", tc.name, limit)
		}
	}
}

// checkMappings spot-checks how individual rows landed.
func checkMappings(t *testing.T, ctx context.Context, conn, cconn *pgx.Conn) {
	t.Helper()
	type row struct {
		query string
		want  string
	}
	for _, r := range []row{
		// skus: real columns, parsed text values, adopted SKU untouched.
		{`SELECT concat_ws('|', name, generation, serial_number_length, has_warranty, warranty_period, status, company_id, created_by)
		  FROM skus WHERE code = 'JIKO-1'`, "Jikokoa|G2|5|t|24|ACTIVE|1|buisy-migration"},
		// POT-1's company is given by code ('bmk'); currency comes from the company.
		{`SELECT concat_ws('|', created_at::date, company_id, currency) FROM skus WHERE code = 'POT-1'`, "2023-03-15|1|KES"},
		{`SELECT concat_ws('|', name, created_by) FROM skus WHERE code = 'ECOA-2'`, "Ecoa (Nexus name)|nexus-admin"},
		// inventory: location rules, status, dates, document number, metadata.
		{`SELECT string_agg(serial_number || '=' || coalesce(location_id::text, 'null') || ':' || status, ',' ORDER BY serial_number)
		  FROM inventory_products WHERE created_by = 'buisy-migration'`,
			"P001=4:SCANNED_IN,P003=4:SOLD,SN001=2:SCANNED_IN,SN002=1:SOLD,SN003=3:SOLD,SN004=4:IN_TRANSIT,SN010=4:SCANNED_IN,SN011=4:IN_TRANSIT"},
		{`SELECT (scan_in_time AT TIME ZONE 'UTC')::text FROM inventory_products WHERE serial_number = 'SN004'`, "2024-01-05 10:00:00"},
		{`SELECT concat_ws('|', coalesce(scan_in_time::text, 'null'), document_no) FROM inventory_products WHERE serial_number = 'SN010'`, "null|BUISY-MIGRATION"},
		{`SELECT coalesce(manufacture_date::text, 'null') FROM inventory_products WHERE serial_number = 'SN011'`, "null"},
		{`SELECT concat_ws('|', metadata ->> 'legacy_source', metadata ->> 'legacy_id', metadata -> 'buisy' ->> 'doc_no')
		  FROM inventory_products WHERE serial_number = 'P001'`, "buisy.nav_inventory__cookware|1|CDOC1"},
		{`SELECT concat_ws('|', status, created_by) FROM inventory_products WHERE serial_number = 'SN-NATIVE'`, "SCANNED_IN|nexus"},
		// agent_skus / location_users keyed by email.
		{`SELECT string_agg(agent_email || ':' || s.code, ',' ORDER BY agent_email, s.code)
		  FROM agent_skus a JOIN skus s ON s.id = a.sku_id WHERE a.created_by = 'buisy-migration'`,
			"alice@burn.com:ECOA-2,alice@burn.com:JIKO-1,bob@burn.com:JIKO-1,carol@burn.com:POT-1"},
		{`SELECT concat_ws('|', location_id, email, external_id) FROM location_users WHERE createdby = 'buisy-migration'`,
			"2|alice@burn.com|kc-alice"},
	} {
		var got string
		mustScan(t, conn.QueryRow(ctx, r.query), &got)
		if got != r.want {
			t.Errorf("deliverydb:\n%s\n got  %q\n want %q", r.query, got, r.want)
		}
	}
	for _, r := range []row{
		// Repeat buyer: one contact, latest sale's details, two accounts.
		{`SELECT concat_ws('|', first_name, last_name, salesperson_id, created_by) FROM contacts WHERE phone = '0712345678'`,
			"Jane|W. Doe|kc-alice|buisy-migration"},
		{`SELECT string_agg(a.external_id || '=' || coalesce(a.serial_number, 'null'), ',' ORDER BY a.external_id)
		  FROM accounts a JOIN contacts c ON c.id = a.contact_id WHERE c.phone = '0712345678'`, "DC001=SN001,DC003=null"},
		// Customer 2 has no delivery code: its contact is created, its account can't be.
		{`SELECT concat_ws('|', first_name, last_name, alt_msisdn, salesperson_id) FROM contacts WHERE phone = '0722000000'`,
			"John||0733111222|kc-bob"},
		{`SELECT count(*)::text FROM accounts WHERE metadata ->> 'legacy_id' = '2'`, "0"},
		{`SELECT concat_ws('|', external_id, coalesce(sales_agent_id, 'null'), sales_model, created_by) FROM accounts
		  WHERE external_id = 'DC005'`, "DC005|null|LEGACY|buisy-migration"},
		{`SELECT coalesce(salesperson_id, 'null') FROM contacts WHERE phone = '0744000000'`, "null"},
		{`SELECT concat_ws('|', first_name, created_by) FROM contacts WHERE metadata ->> 'buisy_contact_key' = 'scust:10'`,
			"Phoneless|buisy-migration"},
		// Existing Nexus contact and its account (found by delivery code): empty fields
		// filled from Buisy (region, serial, agent); values already set (names,
		// salesperson, order) kept.
		{`SELECT concat_ws('|', first_name, last_name, region, salesperson_id, created_by) FROM contacts WHERE phone = '0799000000'`,
			"Native|Person|Nairobi|kc-native-agent|customer-app"},
		{`SELECT concat_ws('|', external_id, serial_number, sales_order_no, sales_agent_id, metadata ->> 'legacy_id', created_by)
		  FROM accounts WHERE external_id = 'DC-NATIVE'`, "DC-NATIVE|SN777|SO-NX|kc-alice|7|customer-app"},
		// Contact with three accounts: customer 13's delivery code found EXT-N2B; customer
		// 11 (no code) and 12 (another contact's code) touched nothing.
		{`SELECT string_agg(a.external_id || ':' || coalesce(a.sales_agent_id, 'null'), ',' ORDER BY a.external_id)
		  FROM accounts a JOIN contacts c ON c.id = a.contact_id WHERE c.phone = '0788000000'`,
			"EXT-N2A:null,EXT-N2B:kc-alice,EXT-N2C:null"},
	} {
		var got string
		mustScan(t, cconn.QueryRow(ctx, r.query), &got)
		if got != r.want {
			t.Errorf("customerappdb:\n%s\n got  %q\n want %q", r.query, got, r.want)
		}
	}
}

// testPartialRun copies only inventory and customers dated on or after
// 2023-03-02. Seed dates: inventory 5 is 2023-01-01 and 10 has a zero date;
// customer 1 is 2023-03-01, customers 4, 6 and 9 have no date.
func testPartialRun(t *testing.T, ctx context.Context, run func(string, app.Options), opts app.Options, out *bytes.Buffer,
	conn, cconn *pgx.Conn, full map[string]int64) {
	t.Helper()

	dry := opts
	dry.Since = time.Date(2023, 3, 2, 0, 0, 0, 0, time.UTC)
	dry.DryRun = true
	if err := app.Run(ctx, nil, nil, dry); !errors.Is(err, app.ErrFilterNeedsCopy) {
		t.Errorf("--since with --dry-run must be refused before connecting, got %v", err)
	}

	partial := opts
	partial.Since = time.Date(2023, 3, 2, 0, 0, 0, 0, time.UTC)
	run("partial run --since=2023-03-02", partial)

	// The report shows what was copied and skipped:
	// table, date_column, source_rows, copied, before_since, no_date_skipped, over_limit.
	report := out.String()
	if !strings.Contains(report, "PARTIAL COPY --since=2023-03-02") {
		t.Error("partial copy report missing")
	}
	for _, want := range [][]string{
		{"nav_inventory", "inv_entry_date", "14", "12", "1", "1", "0"},
		{"nav_inventory__cookware", "inv_entry_date", "3", "3", "0", "0", "0"},
		{"nav_sale_customer", "scust_entry_date", "13", "9", "1", "3", "0"},
	} {
		found := false
		for _, line := range strings.Split(report, "\n") {
			if slices.Equal(strings.Fields(line), want) {
				found = true
			}
		}
		if !found {
			t.Errorf("partial copy report has no row %v:\n%s", want, report)
		}
	}

	staged := func(c *pgx.Conn, table string) (n int64) {
		mustScan(t, c.QueryRow(ctx, "SELECT count(*) FROM buisy_nexus_staging."+table), &n)
		return n
	}
	for _, s := range []struct {
		c     *pgx.Conn
		table string
		want  int64
	}{
		{conn, "nav_inventory", 12}, {conn, "nav_inventory__cookware", 3}, {conn, "nav_sku", 7}, {conn, "users", 6},
		{cconn, "nav_sale_customer", 9}, {cconn, "users", 6},
	} {
		if got := staged(s.c, s.table); got != s.want {
			t.Errorf("staged %s: got %d rows, want %d", s.table, got, s.want)
		}
	}

	// Rows outside the window stay as migrated; nothing is added or removed.
	if after := snapshot(t, ctx, conn, cconn); !maps.Equal(full, after) {
		t.Errorf("partial run changed row counts:\n full    %v\n partial %v", full, after)
	}

	// Without inventory 5, SN001 is no longer a duplicate. Without customer 1,
	// customer 8's DC001 isn't a duplicate in Buisy, but it finds customer 1's
	// account, which belongs to another contact.
	wantDelivery := merge(userRejects, map[string]int64{
		"nav_sku/sku_code_duplicate":               1,
		"nav_sku/sku_code_missing":                 1,
		"nav_sku/unpublished_in_buisy":             1,
		"nav_sku/company_unmatched":                1,
		"users.agent_skus/sku_unresolved":          1,
		"users.agent_skus/agent_email_missing":     1,
		"users.agent_skus/unpublished_in_buisy":    1,
		"users.location_users/location_unmatched":  1,
		"users.location_users/location_ambiguous":  1,
		"users.location_users/agent_email_missing": 1,
		"users.location_users/agent_unresolved":    1,
		"nav_inventory/sku_unresolved":             1,
		"nav_inventory/location_unmatched":         2,
		"nav_inventory/serial_missing":             1,
		"nav_inventory/serial_exists_in_nexus":     1,
		"nav_inventory/unpublished_in_buisy":       1,
		"nav_inventory/company_unmatched":          1,
		"nav_inventory__cookware/duplicate_serial": 1,
	})
	if got := latestRejects(t, ctx, conn); !maps.Equal(got, wantDelivery) {
		t.Errorf("partial deliverydb rejects:\n got  %v\n want %v", got, wantDelivery)
	}
	wantCustomer := merge(userRejects, map[string]int64{
		"nav_sale_customer.accounts/delivery_code_missing":    1, // customer 2
		"nav_sale_customer.accounts/account_contact_mismatch": 2, // customers 8 and 12
		"nav_sale_customer.accounts/account_ambiguous":        1, // customer 11
	})
	if got := latestRejects(t, ctx, cconn); !maps.Equal(got, wantCustomer) {
		t.Errorf("partial customerappdb rejects:\n got  %v\n want %v", got, wantCustomer)
	}

	var since string
	mustScan(t, conn.QueryRow(ctx, `
		SELECT since::text FROM buisy_nexus_staging.migration_runs
		WHERE step = 'copy' AND detail = 'nav_inventory' ORDER BY id DESC LIMIT 1`), &since)
	if since != "2023-03-02" {
		t.Errorf("migration_runs.since = %q", since)
	}
}

// snapshot counts rows created by the migration and rows created in Nexus, per table.
func snapshot(t *testing.T, ctx context.Context, delivery, customer *pgx.Conn) map[string]int64 {
	t.Helper()
	counts := map[string]int64{}
	count := func(c *pgx.Conn, key, query string) {
		var n int64
		mustScan(t, c.QueryRow(ctx, query), &n)
		counts[key] = n
	}
	for _, table := range []string{"skus", "agent_skus", "inventory_products"} {
		count(delivery, table, "SELECT count(*) FROM "+table+" WHERE created_by = 'buisy-migration'")
	}
	count(delivery, "location_users", "SELECT count(*) FROM location_users WHERE createdby = 'buisy-migration'")
	for _, table := range []string{"contacts", "accounts"} {
		count(customer, table, "SELECT count(*) FROM "+table+" WHERE created_by = 'buisy-migration'")
	}
	count(delivery, "native_skus", "SELECT count(*) FROM skus WHERE created_by IS DISTINCT FROM 'buisy-migration'")
	count(delivery, "native_inventory", "SELECT count(*) FROM inventory_products WHERE created_by IS DISTINCT FROM 'buisy-migration'")
	count(delivery, "native_location_users", "SELECT count(*) FROM location_users WHERE createdby IS DISTINCT FROM 'buisy-migration'")
	count(customer, "native_contacts", "SELECT count(*) FROM contacts WHERE created_by IS DISTINCT FROM 'buisy-migration'")
	count(customer, "native_accounts", "SELECT count(*) FROM accounts WHERE created_by IS DISTINCT FROM 'buisy-migration'")
	// Same-named tables outside staging must keep their single row and shape.
	count(delivery, "same_named_public_tables", `
		SELECT (SELECT count(*) FROM public.users WHERE email = 'nexus-native@burn.com')
		     + (SELECT count(*) FROM public.nav_inventory WHERE note = 'not the Buisy table')`)
	return counts
}

// assertStaged checks which Buisy tables were copied into a database's staging schema.
func assertStaged(t *testing.T, ctx context.Context, c *pgx.Conn, name string, present, absent []string) {
	t.Helper()
	for _, tbl := range append(slices.Clone(present), absent...) {
		var exists bool
		mustScan(t, c.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "buisy_nexus_staging."+tbl), &exists)
		if want := slices.Contains(present, tbl); exists != want {
			t.Errorf("%s: buisy_nexus_staging.%s exists=%v, want %v", name, tbl, exists, want)
		}
	}
}

// createCustomerAppDB creates the customerappdb database next to deliverydb,
// loads its prerequisites, schema and seed, and returns its connection URL.
func createCustomerAppDB(t *testing.T, ctx context.Context, deliveryURL, root string) string {
	t.Helper()
	admin, err := pgx.Connect(ctx, deliveryURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE customerappdb"); err != nil {
		t.Fatal(err)
	}
	custURL := strings.Replace(deliveryURL, "/development?", "/customerappdb?", 1)
	c, err := pgx.Connect(ctx, custURL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	for _, file := range []string{
		filepath.Join("testdata", "customerappdb_prereqs.sql"),
		filepath.Join(root, "schema", "customerappdb.sql"),
		filepath.Join("testdata", "customerappdb_seed_data.sql"),
	} {
		sql, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.PgConn().Exec(ctx, string(sql)).ReadAll(); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	return custURL
}

func latestRejects(t *testing.T, ctx context.Context, conn *pgx.Conn) map[string]int64 {
	t.Helper()
	rows, err := conn.Query(ctx, `
		SELECT source_table || '/' || reason, count(*)
		FROM buisy_nexus_staging.migration_rejects
		WHERE run_id = (SELECT run_id FROM buisy_nexus_staging.migration_runs
		                WHERE step = 'transform' AND detail IS NULL AND status = 'ok'
		                ORDER BY started_at DESC LIMIT 1)
		GROUP BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		got[k] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func merge(a, b map[string]int64) map[string]int64 {
	out := maps.Clone(a)
	maps.Copy(out, b)
	return out
}

func mustScan(t *testing.T, row pgx.Row, dest ...any) {
	t.Helper()
	if err := row.Scan(dest...); err != nil {
		t.Fatal(err)
	}
}
