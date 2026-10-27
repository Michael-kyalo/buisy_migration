package sqlfiles

import (
	"testing"
	"testing/fstest"
)

func TestListSortsByName(t *testing.T) {
	fsys := fstest.MapFS{
		"transforms/10_late.sql":  {Data: []byte("select 10")},
		"transforms/02_skus.sql":  {Data: []byte("select 2")},
		"transforms/01_first.sql": {Data: []byte("select 1")},
		"transforms/README.md":    {Data: []byte("not sql")},
	}
	files, err := List(fsys, "transforms")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	want := []string{"01_first.sql", "02_skus.sql", "10_late.sql"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}

func TestListMerged(t *testing.T) {
	fsys := fstest.MapFS{
		"transforms/common/03_agents.sql":          {Data: []byte("select 3")},
		"transforms/deliverydb/02_skus.sql":        {Data: []byte("select 2")},
		"transforms/deliverydb/05_inventory.sql":   {Data: []byte("select 5")},
		"transforms/customerappdb/04_contacts.sql": {Data: []byte("select 4")},
	}
	files, err := ListMerged(fsys, "transforms/common", "transforms/deliverydb", "transforms/missing")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Path())
	}
	want := []string{"transforms/deliverydb/02_skus.sql", "transforms/common/03_agents.sql", "transforms/deliverydb/05_inventory.sql"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	fsys["transforms/deliverydb/03_agents.sql"] = &fstest.MapFile{Data: []byte("select 33")}
	if _, err := ListMerged(fsys, "transforms/common", "transforms/deliverydb"); err == nil {
		t.Fatal("the same file name in two directories must be an error")
	}
}

func TestQueryAndWrap(t *testing.T) {
	f := File{Name: "x.sql", SQL: "-- profile:all\nSELECT 1 -- trailing comment;\n;\n"}
	if !f.ShowAll() {
		t.Error("ShowAll should detect the directive")
	}
	q := Wrap("SELECT count(*) FROM (", f.Query(), ") q")
	want := "SELECT count(*) FROM (\n-- profile:all\nSELECT 1 -- trailing comment\n) q"
	if q != want {
		t.Errorf("got  %q\nwant %q", q, want)
	}
}
