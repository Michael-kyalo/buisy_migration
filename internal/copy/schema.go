package copy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/burnmfg/buisy-migration/internal/source"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Table is a source table's discovered shape.
type Table struct {
	Source  string   // table name in MySQL, as configured
	Name    string   // lower-cased; the staging table name
	Columns []Column // in ordinal order
	Key     []string // primary key, or a NOT NULL unique key; empty if none

	// Optional row filter (see applySince), ANDed into every read.
	Filter     string
	FilterArgs []any
	// The date expression --since filters on; set by applySince.
	DateExpr string
}

// buisyDateExpr parses a Buisy date stored as text, in the formats accepted by
// buisy_nexus_staging.parse_buisy_ts (sql/bootstrap.sql): YYYY-MM-DD[ HH:MM[:SS]]
// and DD/MM/YYYY[ HH:MM[:SS]]. Anything else is NULL. Keep the two in step.
func buisyDateExpr(col string) string {
	c := "TRIM(" + quoteMySQL(col) + ")"
	return "COALESCE(" +
		"STR_TO_DATE(LEFT(" + c + ", 19), '%Y-%m-%d %H:%i:%s'), " +
		"STR_TO_DATE(LEFT(" + c + ", 16), '%Y-%m-%d %H:%i'), " +
		"STR_TO_DATE(LEFT(" + c + ", 10), '%Y-%m-%d'), " +
		"STR_TO_DATE(" + c + ", '%d/%m/%Y %H:%i:%s'), " +
		"STR_TO_DATE(" + c + ", '%d/%m/%Y %H:%i'), " +
		"STR_TO_DATE(" + c + ", '%d/%m/%Y'))"
}

// noDate is true when a date expression is NULL, unparseable or zero (0000-00-00, 2020-00-15).
func noDate(expr string) string {
	return fmt.Sprintf("(%[1]s IS NULL OR MONTH(%[1]s) = 0 OR DAYOFMONTH(%[1]s) = 0)", expr)
}

// applySince limits reads to rows whose col is on or after since. col may be a
// DATE/DATETIME or a text date (Buisy stores most dates as varchar). Rows with
// no usable date are excluded too; they are counted and reported instead.
func (t *Table) applySince(col string, since time.Time) error {
	i := slices.IndexFunc(t.Columns, func(c Column) bool { return c.Name == col })
	if i < 0 {
		return fmt.Errorf("--since: %s has no column %s; set copy.DateColumns to its date column", t.Name, col)
	}
	switch k, _, _ := classify(t.Columns[i]); {
	case k == kindDate || k == kindDateTime:
		t.DateExpr = quoteMySQL(col)
	case k == kindText && t.Columns[i].DataType != "enum" && t.Columns[i].DataType != "set":
		t.DateExpr = buisyDateExpr(col)
	default:
		return fmt.Errorf("--since: %s.%s is %s, not a date or date text", t.Name, col, t.Columns[i].ColumnType)
	}
	t.Filter = t.DateExpr + " >= ? AND NOT " + noDate(t.DateExpr)
	t.FilterArgs = []any{since.Format(time.DateOnly)}
	return nil
}

// where joins the row filter with extra conditions into a WHERE clause.
func (t Table) where(extra ...string) string {
	var conds []string
	if t.Filter != "" {
		conds = append(conds, t.Filter)
	}
	conds = append(conds, extra...)
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}

// Discover reads a table's columns and pagination key from information_schema.
func Discover(ctx context.Context, snap *source.Snapshot, database, table string) (Table, error) {
	t := Table{Source: table, Name: strings.ToLower(table)}
	rows, err := snap.Query(ctx, `
		SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, IS_NULLABLE
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`, database, table)
	if err != nil {
		return t, err
	}
	for rows.Next() {
		var c Column
		var nullable string
		if err := rows.Scan(&c.Name, &c.DataType, &c.ColumnType, &nullable); err != nil {
			rows.Close()
			return t, err
		}
		c.Name = strings.ToLower(c.Name)
		c.DataType = strings.ToLower(c.DataType)
		c.ColumnType = strings.ToLower(c.ColumnType)
		c.Nullable = nullable == "YES"
		t.Columns = append(t.Columns, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return t, err
	}
	if len(t.Columns) == 0 {
		return t, fmt.Errorf("table %s.%s not found or has no columns", database, table)
	}

	// PRIMARY first, then other unique keys; take the first with no nullable column.
	rows, err = snap.Query(ctx, `
		SELECT INDEX_NAME, COLUMN_NAME, NULLABLE
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND NON_UNIQUE = 0
		ORDER BY INDEX_NAME = 'PRIMARY' DESC, INDEX_NAME, SEQ_IN_INDEX`, database, table)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	type key struct {
		cols     []string
		nullable bool
	}
	var order []string
	keys := map[string]*key{}
	for rows.Next() {
		var idx, col, nullable string
		if err := rows.Scan(&idx, &col, &nullable); err != nil {
			return t, err
		}
		k, ok := keys[idx]
		if !ok {
			k = &key{}
			keys[idx] = k
			order = append(order, idx)
		}
		k.cols = append(k.cols, strings.ToLower(col))
		k.nullable = k.nullable || nullable == "YES"
	}
	if err := rows.Err(); err != nil {
		return t, err
	}
	for _, idx := range order {
		if !keys[idx].nullable {
			t.Key = keys[idx].cols
			break
		}
	}
	return t, nil
}

func quoteMySQL(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }

// selectList builds the column list. Date columns with a zero month or day
// (0000-00-00, 2020-00-15) come back NULL: go-sql-driver would otherwise
// silently normalise partial zero dates into a different real date.
func (t Table) selectList() string {
	parts := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		q := quoteMySQL(c.Name)
		if k, _, _ := classify(c); k == kindDate || k == kindDateTime {
			parts[i] = fmt.Sprintf("CASE WHEN MONTH(%[1]s) = 0 OR DAYOFMONTH(%[1]s) = 0 THEN NULL ELSE %[1]s END AS %[1]s", q)
		} else {
			parts[i] = q
		}
	}
	return strings.Join(parts, ", ")
}

// batchQuery returns the keyset-paginated query; first omits the lower bound.
// Its arguments are FilterArgs, then the key bound (unless first), then the limit.
func (t Table) batchQuery(first bool) string {
	keys := make([]string, len(t.Key))
	marks := make([]string, len(t.Key))
	for i, k := range t.Key {
		keys[i] = quoteMySQL(k)
		marks[i] = "?"
	}
	var bound []string
	if !first {
		bound = append(bound, "("+strings.Join(keys, ", ")+") > ("+strings.Join(marks, ", ")+")")
	}
	return "SELECT " + t.selectList() + " FROM " + quoteMySQL(t.Source) + t.where(bound...) +
		" ORDER BY " + strings.Join(keys, ", ") + " LIMIT ?"
}

// fullQuery reads the whole (filtered) table in one streaming query, for tables without a key.
func (t Table) fullQuery() string {
	return "SELECT " + t.selectList() + " FROM " + quoteMySQL(t.Source) + t.where()
}

func (t Table) columnNames() []string {
	names := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		names[i] = c.Name
	}
	return names
}

func (t Table) ident() pgx.Identifier { return pgx.Identifier{"buisy_nexus_staging", t.Name} }

// createDDL is the staging table: UNLOGGED, no constraints, no indexes.
func (t Table) createDDL() string {
	cols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		_, pgType, _ := classify(c)
		cols[i] = pgx.Identifier{c.Name}.Sanitize() + " " + pgType
	}
	return "CREATE UNLOGGED TABLE " + t.ident().Sanitize() + " (" + strings.Join(cols, ", ") + ")"
}

func (t Table) fingerprint() string {
	sum := sha256.Sum256([]byte(t.createDDL()))
	return "buisy-copy:" + hex.EncodeToString(sum[:8])
}

// prepareStaging truncates the staging table if its shape still matches the
// source, otherwise drops and recreates it. Only ever touches schema buisy_nexus_staging.
func prepareStaging(ctx context.Context, pg *pgxpool.Pool, t Table) (recreated bool, err error) {
	var comment *string
	if err := pg.QueryRow(ctx,
		"SELECT obj_description(to_regclass($1), 'pg_class')", t.ident().Sanitize()).Scan(&comment); err != nil {
		return false, err
	}
	if comment != nil && *comment == t.fingerprint() {
		_, err := pg.Exec(ctx, "TRUNCATE "+t.ident().Sanitize())
		return false, err
	}
	tx, err := pg.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS " + t.ident().Sanitize(),
		t.createDDL(),
		"COMMENT ON TABLE " + t.ident().Sanitize() + " IS '" + t.fingerprint() + "'",
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return false, fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return true, tx.Commit(ctx)
}
