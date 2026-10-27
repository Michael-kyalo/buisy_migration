package copy

import (
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		dataType, columnType string
		kind                 kind
		pgType               string
	}{
		{"tinyint", "tinyint(1)", kindInt, "smallint"},
		{"tinyint", "tinyint(3) unsigned", kindInt, "smallint"},
		{"smallint", "smallint(5) unsigned", kindInt, "integer"},
		{"int", "int(10) unsigned", kindInt, "bigint"},
		{"bigint", "bigint(20)", kindInt, "bigint"},
		{"bigint", "bigint(20) unsigned", kindUint64, "numeric(20,0)"},
		{"decimal", "decimal(12,2)", kindDecimal, "numeric"},
		{"double", "double", kindFloat, "double precision"},
		{"varchar", "varchar(64)", kindText, "text"},
		{"enum", "enum('a','b')", kindText, "text"},
		{"time", "time", kindText, "text"},
		{"date", "date", kindDate, "date"},
		{"datetime", "datetime", kindDateTime, "timestamp"},
		{"timestamp", "timestamp", kindDateTime, "timestamp"},
		{"longblob", "longblob", kindBytes, "bytea"},
		{"bit", "bit(1)", kindBytes, "bytea"},
		{"json", "json", kindJSON, "jsonb"},
	}
	for _, c := range cases {
		k, pg, known := classify(Column{DataType: c.dataType, ColumnType: c.columnType})
		if k != c.kind || pg != c.pgType || !known {
			t.Errorf("%s: got (%v, %q, %v), want (%v, %q)", c.columnType, k, pg, known, c.kind, c.pgType)
		}
	}
	if _, pg, known := classify(Column{DataType: "vector"}); known || pg != "text" {
		t.Errorf("unknown type: got %q known=%v", pg, known)
	}
}

func TestConvertTime(t *testing.T) {
	ts := time.Date(2024, 3, 5, 10, 30, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   sql.NullTime
		want any
	}{
		{"null", sql.NullTime{}, nil},
		{"mysql zero date", sql.NullTime{Time: time.Time{}, Valid: true}, nil},
		{"real", sql.NullTime{Time: ts, Valid: true}, ts},
	}
	for _, c := range cases {
		if got := convertTime(c.in); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	nairobi := time.FixedZone("EAT", 3*3600)
	if got := convertTime(sql.NullTime{Time: ts.In(nairobi), Valid: true}); got != ts {
		t.Errorf("non-UTC time should be normalised to UTC, got %v", got)
	}
}

func TestConvertDecimalIsExact(t *testing.T) {
	got, err := convertDecimal(sql.NullString{String: "12345678901234567890.0123456789", Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	n := got.(pgtype.Numeric)
	v, _ := n.Value()
	if v != "12345678901234567890.0123456789" {
		t.Errorf("decimal lost precision: %v", v)
	}
	if got, err := convertDecimal(sql.NullString{}); got != nil || err != nil {
		t.Errorf("NULL decimal: got %v, %v", got, err)
	}
	if _, err := convertDecimal(sql.NullString{String: "abc", Valid: true}); err == nil {
		t.Error("expected error for non-numeric decimal")
	}
}

func TestConvertUnsignedBigint(t *testing.T) {
	got, err := convertDecimal(sql.NullString{String: "18446744073709551615", Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := got.(pgtype.Numeric).Value()
	if v != "18446744073709551615" {
		t.Errorf("got %v", v)
	}
}

func TestConvertText(t *testing.T) {
	if v, fixes := convertText(sql.NullString{}); v != nil || fixes != 0 {
		t.Errorf("NULL: got %v, %d", v, fixes)
	}
	if v, fixes := convertText(sql.NullString{String: "ok", Valid: true}); v != "ok" || fixes != 0 {
		t.Errorf("plain: got %v, %d", v, fixes)
	}
	v, fixes := convertText(sql.NullString{String: "a\x00b\xffc", Valid: true})
	if v != "ab�c" || fixes != 2 {
		t.Errorf("NUL + invalid UTF-8: got %q, %d", v, fixes)
	}
	if v, _ := convertText(sql.NullString{String: "", Valid: true}); v != "" {
		t.Errorf("empty string must stay empty, not NULL: got %v", v)
	}
}

func TestConvertIntBytesFloat(t *testing.T) {
	if convertInt(sql.NullInt64{}) != nil || convertInt(sql.NullInt64{Int64: 1, Valid: true}) != int64(1) {
		t.Error("convertInt")
	}
	if convertFloat(sql.NullFloat64{}) != nil {
		t.Error("convertFloat NULL")
	}
	if convertBytes(nil) != nil {
		t.Error("nil bytes must be NULL")
	}
	if b, ok := convertBytes([]byte{}).([]byte); !ok || b == nil {
		t.Error("empty bytes must stay non-NULL")
	}
}

func TestScannerTinyintIsCopiedAsIs(t *testing.T) {
	k, _, _ := classify(Column{DataType: "tinyint", ColumnType: "tinyint(1)"})
	s := newScanner(k)
	*s.dest.(*sql.NullInt64) = sql.NullInt64{Int64: 1, Valid: true}
	v, _, err := s.value()
	if err != nil || v != int64(1) {
		t.Errorf("got %v, %v; tinyint(1) must stay an integer", v, err)
	}
}

func TestBatchQueryUsesKeysetNotOffset(t *testing.T) {
	tbl := Table{
		Source: "Nav_Inventory", Name: "nav_inventory",
		Columns: []Column{{Name: "inv_id", DataType: "int"}, {Name: "inv_entry_date", DataType: "datetime"}},
		Key:     []string{"inv_id"},
	}
	first, next := tbl.batchQuery(true), tbl.batchQuery(false)
	for _, q := range []string{first, next} {
		if strings.Contains(strings.ToUpper(q), "OFFSET") || !strings.HasSuffix(q, "ORDER BY `inv_id` LIMIT ?") {
			t.Errorf("bad batch query: %s", q)
		}
		if !strings.Contains(q, "FROM `Nav_Inventory`") {
			t.Errorf("query must use the source table name: %s", q)
		}
	}
	if !strings.Contains(next, "WHERE (`inv_id`) > (?)") || strings.Contains(first, "WHERE") {
		t.Errorf("keyset bound wrong:\n%s\n%s", first, next)
	}
	if !strings.Contains(first, "MONTH(`inv_entry_date`) = 0") {
		t.Errorf("date columns must null out zero dates: %s", first)
	}
}

func TestApplySince(t *testing.T) {
	tbl := Table{
		Source: "nav_inventory", Name: "nav_inventory",
		Columns: []Column{{Name: "inv_id", DataType: "int"}, {Name: "inv_entry_date", DataType: "datetime"}, {Name: "inv_sku", DataType: "varchar"}},
		Key:     []string{"inv_id"},
	}
	since := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := tbl.applySince("inv_entry_date", since); err != nil {
		t.Fatal(err)
	}
	if len(tbl.FilterArgs) != 1 || tbl.FilterArgs[0] != "2024-03-01" {
		t.Errorf("filter args = %v", tbl.FilterArgs)
	}
	first, next := tbl.batchQuery(true), tbl.batchQuery(false)
	wantFilter := "WHERE `inv_entry_date` >= ? AND NOT (`inv_entry_date` IS NULL OR MONTH(`inv_entry_date`) = 0 OR DAYOFMONTH(`inv_entry_date`) = 0)"
	if !strings.Contains(first, wantFilter+" ORDER BY `inv_id` LIMIT ?") {
		t.Errorf("first batch must apply the filter:\n%s", first)
	}
	if !strings.Contains(next, wantFilter+" AND (`inv_id`) > (?) ORDER BY") {
		t.Errorf("later batches must apply the filter and the keyset bound, in argument order:\n%s", next)
	}
	if !strings.HasSuffix(tbl.fullQuery(), wantFilter) {
		t.Errorf("unkeyed read must apply the filter: %s", tbl.fullQuery())
	}

	if err := tbl.applySince("created_at", since); err == nil || !strings.Contains(err.Error(), "DateColumns") {
		t.Errorf("missing column must point at DateColumns, got %v", err)
	}
	if err := tbl.applySince("inv_id", since); err == nil {
		t.Error("a numeric column must be refused")
	}
	unfiltered := Table{Source: "users", Name: "users", Columns: tbl.Columns, Key: tbl.Key}
	if strings.Contains(unfiltered.batchQuery(true), "WHERE") {
		t.Error("a table without --since must not be filtered")
	}
}

func TestPartialOptions(t *testing.T) {
	if (Options{}).partial() {
		t.Error("no --since and no --limit is a full copy")
	}
	if !(Options{Limit: 10}).partial() || !(Options{Since: time.Now()}).partial() {
		t.Error("--limit or --since makes a partial copy")
	}
}

// Buisy stores most dates as varchar; --since parses them in MySQL.
func TestApplySinceOnTextDate(t *testing.T) {
	tbl := Table{
		Source: "nav_sale_customer", Name: "nav_sale_customer",
		Columns: []Column{{Name: "scust_id", DataType: "int"}, {Name: "scust_entry_date", DataType: "varchar"}},
		Key:     []string{"scust_id"},
	}
	if err := tbl.applySince("scust_entry_date", time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"'%Y-%m-%d %H:%i:%s'", "'%Y-%m-%d'", "'%d/%m/%Y %H:%i'", "'%d/%m/%Y'"} {
		if !strings.Contains(tbl.DateExpr, format) {
			t.Errorf("text date expression must accept %s: %s", format, tbl.DateExpr)
		}
	}
	if !strings.HasPrefix(tbl.Filter, tbl.DateExpr+" >= ? AND NOT ("+tbl.DateExpr+" IS NULL") {
		t.Errorf("filter must compare and null-check the parsed date: %s", tbl.Filter)
	}
	enum := Table{Name: "x", Columns: []Column{{Name: "kind", DataType: "enum"}}}
	if err := enum.applySince("kind", time.Now()); err == nil {
		t.Error("an enum column must be refused")
	}
}

func TestDateColumnsOnlyFilterInventoryAndCustomers(t *testing.T) {
	for _, tbl := range []string{"nav_sku", "users", "nav_dimension"} {
		if _, ok := DateColumns[tbl]; ok {
			t.Errorf("%s is referenced by filtered tables and must always copy in full", tbl)
		}
	}
	for tbl := range DateColumns {
		if !slices.Contains(SourceTables, tbl) {
			t.Errorf("DateColumns names unknown table %s", tbl)
		}
	}
}

func TestCreateDDLIsUnloggedWithoutConstraints(t *testing.T) {
	tbl := Table{Name: "users", Columns: []Column{{Name: "user_id", DataType: "int"}, {Name: "email", DataType: "varchar"}}}
	ddl := tbl.createDDL()
	want := `CREATE UNLOGGED TABLE "buisy_nexus_staging"."users" ("user_id" bigint, "email" text)`
	if ddl != want {
		t.Errorf("got  %s\nwant %s", ddl, want)
	}
}
