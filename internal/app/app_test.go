package app

import (
	"regexp"
	"slices"
	"testing"

	migration "github.com/burnmfg/buisy-migration"
	"github.com/burnmfg/buisy-migration/internal/config"
	copystep "github.com/burnmfg/buisy-migration/internal/copy"
	"github.com/burnmfg/buisy-migration/internal/sqlfiles"
)

func TestSpecsCoverEveryTargetAndTable(t *testing.T) {
	used := map[string]bool{}
	for _, name := range config.TargetNames {
		spec, ok := Specs[name]
		if !ok {
			t.Fatalf("no Spec for target %s", name)
		}
		for _, tbl := range spec.SourceTables {
			if tbl != copystep.KeycloakTable && !slices.Contains(copystep.SourceTables, tbl) {
				t.Errorf("%s: unknown source table %s", name, tbl)
			}
			used[tbl] = true
		}
		files, err := sqlfiles.ListMerged(migration.SQL, "transforms/common", "transforms/"+name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(files) == 0 || files[0].Name != "00_preflight.sql" {
			t.Errorf("%s: transforms must start with its own 00_preflight.sql", name)
		}
		if _, err := sqlfiles.ListMerged(migration.SQL, "validate/common", "validate/"+name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, tbl := range copystep.SourceTables {
		if !used[tbl] {
			t.Errorf("source table %s is not copied into any target", tbl)
		}
	}
	if len(Specs) != len(config.TargetNames) {
		t.Errorf("Specs has %d entries, config.TargetNames has %d", len(Specs), len(config.TargetNames))
	}
}

var writesContactsRe = regexp.MustCompile(`(?i)\bINTO\s+(contacts|accounts)\b`)

// contacts and accounts belong to customerappdb; their SQL must not run in deliverydb.
func TestContactsOnlyInCustomerAppDB(t *testing.T) {
	if !writesContactsRe.MatchString("INSERT INTO contacts AS t") {
		t.Fatal("pattern no longer detects contact writes")
	}
	for _, name := range config.TargetNames {
		files, _ := sqlfiles.ListMerged(migration.SQL, "transforms/common", "transforms/"+name)
		for _, f := range files {
			writesContacts := writesContactsRe.MatchString(f.SQL)
			if writesContacts && name != config.CustomerAppDB {
				t.Errorf("%s writes contacts/accounts but runs in %s", f.Path(), name)
			}
		}
	}
}
