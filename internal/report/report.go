// Package report prints query results as aligned text tables.
package report

import (
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"
)

const maxCell = 60

type Table struct {
	Columns []string
	Rows    [][]string
}

func (t *Table) Add(values []any) {
	row := make([]string, len(values))
	for i, v := range values {
		row[i] = Format(v)
	}
	t.Rows = append(t.Rows, row)
}

func (t *Table) Print(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  "+strings.Join(t.Columns, "\t"))
	for _, r := range t.Rows {
		fmt.Fprintln(tw, "  "+strings.Join(r, "\t"))
	}
	tw.Flush()
}

// Format renders a database value for display.
func Format(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		s = x
	case []byte:
		if utf8.Valid(x) {
			s = string(x)
		} else {
			s = "\\x" + hex.EncodeToString(x)
		}
	case time.Time:
		s = x.UTC().Format(time.RFC3339)
	case [16]byte:
		s = fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])
	case driver.Valuer:
		dv, err := x.Value()
		if err != nil {
			s = fmt.Sprint(x)
		} else {
			return Format(dv)
		}
	default:
		s = fmt.Sprint(x)
	}
	s = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
	if utf8.RuneCountInString(s) > maxCell {
		s = string([]rune(s)[:maxCell-1]) + "…"
	}
	return s
}
