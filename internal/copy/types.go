package copy

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

// Column is one source column as discovered from information_schema.
type Column struct {
	Name       string // lower-cased; used as-is in staging
	DataType   string // information_schema DATA_TYPE, e.g. "tinyint"
	ColumnType string // information_schema COLUMN_TYPE, e.g. "tinyint(1) unsigned"
	Nullable   bool
}

func (c Column) unsigned() bool { return strings.Contains(c.ColumnType, "unsigned") }

type kind int

const (
	kindInt kind = iota
	kindUint64
	kindDecimal
	kindFloat
	kindText
	kindDate
	kindDateTime
	kindBytes
	kindJSON
)

// classify maps a MySQL column to how it is scanned and its staging type.
// Values are copied as-is (TINYINT(1) stays an integer); conversion happens in SQL.
func classify(c Column) (k kind, pgType string, known bool) {
	switch strings.ToLower(c.DataType) {
	case "tinyint", "year":
		return kindInt, "smallint", true
	case "smallint", "mediumint":
		return kindInt, "integer", true
	case "int", "integer":
		return kindInt, "bigint", true
	case "bigint":
		if c.unsigned() {
			return kindUint64, "numeric(20,0)", true
		}
		return kindInt, "bigint", true
	case "decimal", "numeric":
		return kindDecimal, "numeric", true
	case "float", "double", "real":
		return kindFloat, "double precision", true
	case "char", "varchar", "tinytext", "text", "mediumtext", "longtext", "enum", "set", "time":
		return kindText, "text", true
	case "date":
		return kindDate, "date", true
	case "datetime", "timestamp":
		return kindDateTime, "timestamp", true
	case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob", "bit",
		"geometry", "point", "linestring", "polygon", "multipoint", "multilinestring",
		"multipolygon", "geometrycollection":
		return kindBytes, "bytea", true
	case "json":
		return kindJSON, "jsonb", true
	}
	return kindText, "text", false
}

// scanner holds the scan destination for one column and converts it to the
// value handed to pgx CopyFrom.
type scanner struct {
	kind kind
	dest any
}

func newScanner(k kind) *scanner {
	s := &scanner{kind: k}
	switch k {
	case kindInt:
		s.dest = new(sql.NullInt64)
	case kindFloat:
		s.dest = new(sql.NullFloat64)
	case kindDate, kindDateTime:
		s.dest = new(sql.NullTime)
	case kindBytes:
		s.dest = new([]byte)
	default: // text, json, decimal, uint64: all read as strings
		s.dest = new(sql.NullString)
	}
	return s
}

// value returns the converted value and how many text fix-ups were applied.
func (s *scanner) value() (any, int, error) {
	switch s.kind {
	case kindInt:
		return convertInt(*s.dest.(*sql.NullInt64)), 0, nil
	case kindFloat:
		return convertFloat(*s.dest.(*sql.NullFloat64)), 0, nil
	case kindDate, kindDateTime:
		return convertTime(*s.dest.(*sql.NullTime)), 0, nil
	case kindBytes:
		return convertBytes(*s.dest.(*[]byte)), 0, nil
	case kindDecimal, kindUint64:
		v, err := convertDecimal(*s.dest.(*sql.NullString))
		return v, 0, err
	default:
		v, fixes := convertText(*s.dest.(*sql.NullString))
		return v, fixes, nil
	}
}

// keyValue returns the raw value used as a keyset pagination bound.
func (s *scanner) keyValue() any {
	switch d := s.dest.(type) {
	case *sql.NullInt64:
		return d.Int64
	case *sql.NullFloat64:
		return d.Float64
	case *sql.NullTime:
		return d.Time
	case *[]byte:
		return *d
	case *sql.NullString:
		return d.String
	}
	return nil
}

func convertInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func convertFloat(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return v.Float64
}

// convertTime turns NULL, MySQL zero dates and Go's zero time into NULL.
func convertTime(v sql.NullTime) any {
	if !v.Valid || v.Time.IsZero() {
		return nil
	}
	return v.Time.UTC()
}

func convertBytes(b []byte) any {
	if b == nil {
		return nil
	}
	return b
}

// convertDecimal parses DECIMAL text exactly; it never goes through float64.
func convertDecimal(v sql.NullString) (any, error) {
	if !v.Valid {
		return nil, nil
	}
	var n pgtype.Numeric
	if err := n.Scan(strings.TrimSpace(v.String)); err != nil {
		return nil, fmt.Errorf("decimal %q: %w", v.String, err)
	}
	return n, nil
}

// convertText strips NUL bytes (Postgres text rejects them) and replaces
// invalid UTF-8. fixes counts the values that needed either.
func convertText(v sql.NullString) (any, int) {
	if !v.Valid {
		return nil, 0
	}
	s, fixes := v.String, 0
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
		fixes++
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
		fixes++
	}
	return s, fixes
}
